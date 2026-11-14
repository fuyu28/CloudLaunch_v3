// リモートで削除されたゲームの上げ直しの排他と前提の確認（仕様書 8 章「リモートで削除されたゲーム」の 8-U2・8-U3）。DB の
// 書き換えは、呼び出し側が reupload として渡す。
package syncore

import (
	"context"
	"errors"
	"fmt"
)

// ErrNotRemoteDeleted は、ゲームの tombstone の記録がないので、上げ直さないことを表す。
var ErrNotRemoteDeleted = errors.New("game is not recorded as deleted on remote")

// ReuploadGame は、同期ロックを握ったまま、Pull ジャーナルが残っていないことと、ゲームの tombstone の記録があることを
// 確かめてから reupload を呼ぶ。記録がなければ ErrNotRemoteDeleted で包んだエラーを返す。記録はリモートを読まずに DB で
// 確かめる。セーブフォルダに触れないので、ゲームの実行中かは確かめない（8-U2）。
func (c *Core) ReuploadGame(ctx context.Context, gameID string, reupload func(ctx context.Context) error) error {
	if reupload == nil {
		return fmt.Errorf("reupload game %s: %w", gameID, errors.New("reupload is not given"))
	}
	release, err := c.acquire(ctx)
	if err != nil {
		return fmt.Errorf("reupload game %s: %w", gameID, err)
	}
	defer release()

	if err := c.ensureNoPullJournal(ctx, gameID); err != nil {
		return fmt.Errorf("reupload game %s: %w", gameID, err)
	}
	if _, deleted, err := c.cfg.Local.RemoteDeleted(ctx, gameID); err != nil {
		return fmt.Errorf("reupload game %s: read remote deleted: %w", gameID, err)
	} else if !deleted {
		return fmt.Errorf("reupload game %s: %w", gameID, ErrNotRemoteDeleted)
	}
	if err := reupload(ctx); err != nil {
		return fmt.Errorf("reupload game %s: %w", gameID, err)
	}
	return nil
}
