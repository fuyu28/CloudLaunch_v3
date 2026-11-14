// ゲームの tombstone（`games/{gameId}/deleted.json`、仕様書 8 章「ゲームの tombstone の確認」）の検証・確認と、tombstone を
// 見つけたときの DB への記録と配下の削除（8-T2）、Push・Pull の拒否（8-T7・8-T8）。
package syncore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"time"
)

// ErrRemoteDeleted は、ゲームがリモートで削除されていて、Push・Pull できないことを表す。呼び出し側は、DB への記録と
// リモートの掃除（仕様書 8-T2）を行い、「リモートで削除されました」と表示する。
var ErrRemoteDeleted = errors.New("game is deleted on remote")

// ErrInvalidTombstone は、deleted.json が仕様書 8-T4〜8-T6 に当たることを表す。削除されたとも、削除されていないとも
// 扱わない。読めないこと（8-T3）とは区別する。
var ErrInvalidTombstone = errors.New("invalid game tombstone")

// maxTombstoneSize は、読む deleted.json の本文の上限。正しい本文は 100 バイトほどなので、超えるものは壊れた object として
// 8-T4 で扱う。
const maxTombstoneSize = 64 << 10

type Tombstone struct {
	DeletedAt  time.Time
	DeviceName string
}

type tombstoneJSON struct {
	DeletedAt  *string `json:"deletedAt"`
	DeviceName *string `json:"deviceName"`
}

func gamePrefix(gameID string) string {
	return "games/" + gameID + "/"
}

func tombstoneKey(gameID string) string {
	return gamePrefix(gameID) + "deleted.json"
}

// parseTombstone は、仕様書 8-T4〜8-T6 で検証する。知らない項目は無視する。
func parseTombstone(body []byte) (Tombstone, error) {
	if len(body) > maxTombstoneSize {
		return Tombstone{}, fmt.Errorf("8-T4: body exceeds %d bytes", maxTombstoneSize)
	}
	var j tombstoneJSON
	if err := json.Unmarshal(body, &j); err != nil {
		return Tombstone{}, fmt.Errorf("8-T4: %w", err)
	}
	if j.DeletedAt == nil || *j.DeletedAt == "" || j.DeviceName == nil || *j.DeviceName == "" {
		return Tombstone{}, errors.New("8-T5: deletedAt or deviceName is missing or empty")
	}
	t, err := time.Parse(timeLayout, *j.DeletedAt)
	// 3 章の固定長の形だけを受け付けるため、書き戻して比べる
	if err != nil || t.UTC().Format(timeLayout) != *j.DeletedAt {
		return Tombstone{}, fmt.Errorf("8-T6: deletedAt %q is not in the spec format", *j.DeletedAt)
	}
	return Tombstone{DeletedAt: t, DeviceName: *j.DeviceName}, nil
}

// ReadRemoteTombstone は、DB の記録を見ずに deleted.json だけを読む。登録していないゲームにも使える。deleted.json がなければ
// false を返す。不正なら ErrInvalidTombstone で包んだエラーを、読めなければそれ以外のエラーを返す。
func (c *Core) ReadRemoteTombstone(ctx context.Context, gameID string) (Tombstone, bool, error) {
	r, err := c.cfg.Remote.Get(ctx, tombstoneKey(gameID))
	if errors.Is(err, fs.ErrNotExist) {
		return Tombstone{}, false, nil
	}
	if err != nil {
		return Tombstone{}, false, fmt.Errorf("read deleted.json of game %s: %w", gameID, err)
	}
	defer func() { _ = r.Close() }()
	body, err := io.ReadAll(io.LimitReader(r, maxTombstoneSize+1))
	if err != nil {
		return Tombstone{}, false, fmt.Errorf("read deleted.json of game %s: %w", gameID, err)
	}
	t, err := parseTombstone(body)
	if err != nil {
		return Tombstone{}, false, fmt.Errorf("deleted.json of game %s: %w: %w", gameID, ErrInvalidTombstone, err)
	}
	return t, true, nil
}

// CheckTombstone は、何も書かずにゲームの tombstone を確かめる。CLI の status と、判定の前・承認の後の読み直しが使う。
// DB に記録があれば、リモートを読まずに記録を返す（8-T8）。
func (c *Core) CheckTombstone(ctx context.Context, gameID string) (Tombstone, bool, error) {
	recorded, ok, err := c.cfg.Local.RemoteDeleted(ctx, gameID)
	if err != nil {
		return Tombstone{}, false, fmt.Errorf("read remote deleted: %w", err)
	}
	if ok {
		return recorded, true, nil
	}
	return c.ReadRemoteTombstone(ctx, gameID)
}

// HandleTombstone は、DB の記録の有無によらず deleted.json を読み、あれば DB に記録してから、配下の deleted.json 以外を
// 削除する（8-T2）。DB に記録があれば、記録は書き換えずに記録を返し、CheckTombstone と同じ値を表示させる。削除に失敗したら、
// tombstone と true とともにエラーを返す。記録は残るので、次の呼び出しで消し残りを削除する（8-X10）。
//
// 記録があれば、deleted.json がない・読めない・不正なときも、何も削除せず、エラーにもせずに記録を返す（8-T8）。リモートで
// tombstone が見えないまま配下を消すと取り返しがつかず、記録のあるゲームは、オフラインでも削除済みとして止めるため。
func (c *Core) HandleTombstone(ctx context.Context, gameID string) (Tombstone, bool, error) {
	recorded, isRecorded, err := c.cfg.Local.RemoteDeleted(ctx, gameID)
	if err != nil {
		return Tombstone{}, false, fmt.Errorf("read remote deleted: %w", err)
	}
	remote, ok, err := c.ReadRemoteTombstone(ctx, gameID)
	if isRecorded && (err != nil || !ok) {
		c.cfg.Logger.Warn("game is recorded as deleted but deleted.json is not usable; keep remote data", "gameId", gameID, "err", err)
		return recorded, true, nil
	}
	if err != nil || !ok {
		return Tombstone{}, false, err
	}
	t := recorded
	if !isRecorded {
		if err := c.cfg.Local.RecordRemoteDeleted(ctx, gameID, remote); err != nil {
			return Tombstone{}, false, fmt.Errorf("record remote deleted: %w", err)
		}
		t = remote
	}
	return t, true, c.deleteGameData(ctx, gameID)
}

// deleteGameData は、`games/{gameId}/` 配下の deleted.json 以外を削除する。接頭辞の一覧で消すので、メタ情報（8 章）の
// キーの形を知らなくてよい。
func (c *Core) deleteGameData(ctx context.Context, gameID string) error {
	keys, err := c.cfg.Remote.List(ctx, gamePrefix(gameID))
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

// ensureNotDeleted は、承認の後に tombstone を読み直す（仕様書 8-T7）。
func (c *Core) ensureNotDeleted(ctx context.Context, gameID string) error {
	t, ok, err := c.CheckTombstone(ctx, gameID)
	if err != nil {
		return err
	}
	if ok {
		return fmt.Errorf("deleted by %s at %s after approval: %w", t.DeviceName, t.DeletedAt.Format(timeLayout), ErrRemoteDeleted)
	}
	return nil
}

// checkNotDeleted は、Status が tombstone を見つけた plan を、上書きでも拒否する（仕様書 8-T8）。
func checkNotDeleted(plan Plan) error {
	if plan.RemoteDeleted != nil {
		return ErrRemoteDeleted
	}
	return nil
}
