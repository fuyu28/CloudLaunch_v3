// ゲームの tombstone（`games/{gameId}/deleted.json`、仕様書 8 章「ゲームの tombstone の確認」）の検証・確認と、
// tombstone を見つけたときの DB への記録と配下の削除（8-T2）。
package metasync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"time"
)

// maxTombstoneSize は、読む deleted.json の本文の上限。正しい本文は 100 バイトほどなので、超えるものは壊れた object として
// 8-T4 で扱う。
const maxTombstoneSize = 64 << 10

type Tombstone struct {
	DeletedAt  time.Time
	DeviceName string
}

func tombstoneKey(gameID string) string {
	return "games/" + gameID + "/deleted.json"
}

// validateTombstone は、仕様書 8-T4〜8-T6 で検証する。知らない項目は無視する。
func validateTombstone(body []byte) (Tombstone, error) {
	if len(body) > maxTombstoneSize {
		return Tombstone{}, fmt.Errorf("8-T4: body exceeds %d bytes", maxTombstoneSize)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return Tombstone{}, fmt.Errorf("8-T4: %w", err)
	}
	var deletedAt, deviceName string
	for _, f := range []struct {
		name string
		dst  *string
	}{{"deletedAt", &deletedAt}, {"deviceName", &deviceName}} {
		if err := unmarshalString(fields, f.name, f.dst); err != nil || *f.dst == "" {
			return Tombstone{}, fmt.Errorf("8-T5: %s %s is missing or empty", f.name, fields[f.name])
		}
	}
	t, ok := parseTimestamp(fields["deletedAt"])
	if !ok {
		return Tombstone{}, fmt.Errorf("8-T6: deletedAt %s is not in the spec format", fields["deletedAt"])
	}
	return Tombstone{DeletedAt: t, DeviceName: deviceName}, nil
}

// readTombstone は、deleted.json がなければ ok に false を返す。読めない（8-T3）・不正（8-T4〜8-T6）ならエラーを返す。
func (c *Core) readTombstone(ctx context.Context, gameID string) (Tombstone, bool, error) {
	body, err := c.getLimited(ctx, tombstoneKey(gameID), maxTombstoneSize)
	if errors.Is(err, fs.ErrNotExist) {
		return Tombstone{}, false, nil
	}
	if err != nil {
		return Tombstone{}, false, err
	}
	t, err := validateTombstone(body)
	if err != nil {
		return Tombstone{}, false, fmt.Errorf("invalid deleted.json of game %s: %w", gameID, err)
	}
	return t, true, nil
}

// CheckTombstone は、何も書かずにゲームの tombstone を確かめる。CLI の status と、同期コアの判定の前・承認の後の読み直しが
// 使う。DB に記録があれば、リモートを読まずに記録を返す（8-T8）。
func (c *Core) CheckTombstone(ctx context.Context, gameID string) (Tombstone, bool, error) {
	recorded, ok, err := c.cfg.Local.RemoteDeleted(ctx, gameID)
	if err != nil || ok {
		return recorded, ok, err
	}
	return c.readTombstone(ctx, gameID)
}

// HandleTombstone は、DB の記録の有無によらず deleted.json を読み、あれば DB に記録してから、配下の deleted.json 以外を
// 削除する（8-T2）。DB に記録があれば、記録は書き換えない。削除が途中で失敗しても記録は残るので、次の呼び出しで消し残りを
// 削除する（8-X10）。
//
// 記録があるのに deleted.json がなければ、何も削除せずに記録を返す。リモートで tombstone が見えないまま配下を消すと、
// 取り返しがつかないため。
func (c *Core) HandleTombstone(ctx context.Context, gameID string) (Tombstone, bool, error) {
	recorded, isRecorded, err := c.cfg.Local.RemoteDeleted(ctx, gameID)
	if err != nil {
		return Tombstone{}, false, fmt.Errorf("read remote deleted: %w", err)
	}
	remote, ok, err := c.readTombstone(ctx, gameID)
	if err != nil {
		return Tombstone{}, false, err
	}
	if !ok {
		if isRecorded {
			c.cfg.Logger.Warn("game is recorded as deleted but deleted.json is missing; keep remote data", "gameId", gameID)
		}
		return recorded, isRecorded, nil
	}
	if !isRecorded {
		if err := c.cfg.Local.RecordRemoteDeleted(ctx, gameID, remote); err != nil {
			return Tombstone{}, false, fmt.Errorf("record remote deleted: %w", err)
		}
	}
	if err := c.deleteGameData(ctx, gameID); err != nil {
		return Tombstone{}, false, err
	}
	return remote, true, nil
}

// deleteGameData は、`games/{gameId}/` 配下の deleted.json 以外を削除する。
func (c *Core) deleteGameData(ctx context.Context, gameID string) error {
	keys, err := c.cfg.Remote.List(ctx, gamesPrefix+gameID+"/")
	if err != nil {
		return fmt.Errorf("list game data: %w", err)
	}
	var targets []string
	for _, k := range keys {
		if k != tombstoneKey(gameID) {
			targets = append(targets, k)
		}
	}
	if len(targets) == 0 {
		return nil
	}
	if err := c.cfg.Remote.DeleteMany(ctx, targets); err != nil {
		return fmt.Errorf("delete game data: %w", err)
	}
	c.cfg.Logger.Info("deleted remote game data", "gameId", gameID, "keys", len(targets))
	return nil
}
