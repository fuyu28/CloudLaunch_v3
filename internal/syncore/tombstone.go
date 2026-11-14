// ゲームの tombstone の確認（仕様書 8-T2・8-T7・8-T8）。確認の実体は Config.ReadTombstone で受け取り、DB への記録と
// リモートの掃除は行わない。
package syncore

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrRemoteDeleted は、ゲームがリモートで削除されていて、Push・Pull できないことを表す。呼び出し側は、DB への記録と
// リモートの掃除（仕様書 8-T2）を行い、「リモートで削除されました」と表示する。
var ErrRemoteDeleted = errors.New("game is deleted on remote")

var errReadTombstoneNotSet = errors.New("tombstone check is not configured")

type Tombstone struct {
	DeletedAt  time.Time
	DeviceName string
}

// readTombstone は、ReadTombstone がなければエラーにし、確認を飛ばして判定する道を作らない。飛ばすと、削除済みのゲームに
// Push するため。
func (c *Core) readTombstone(ctx context.Context, gameID string) (Tombstone, bool, error) {
	if c.cfg.ReadTombstone == nil {
		return Tombstone{}, false, errReadTombstoneNotSet
	}
	t, ok, err := c.cfg.ReadTombstone(ctx, gameID)
	if err != nil {
		return Tombstone{}, false, fmt.Errorf("check tombstone of game %s: %w", gameID, err)
	}
	return t, ok, nil
}

// ensureNotDeleted は、承認の後に tombstone を読み直す（仕様書 8-T7）。
func (c *Core) ensureNotDeleted(ctx context.Context, gameID string) error {
	t, ok, err := c.readTombstone(ctx, gameID)
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
