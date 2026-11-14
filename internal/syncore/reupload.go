// リモートで削除されたゲームの上げ直しの排他（仕様書 8 章「リモートで削除されたゲーム」の 8-U2・8-U3）。DB の書き換えは、
// 呼び出し側が reupload として渡す。
package syncore

import (
	"context"
	"errors"
	"fmt"
)

// ReuploadGame は、同期ロックを握ったまま、Pull ジャーナルが残っていないことを確かめてから reupload を呼ぶ。セーブフォルダに
// 触れないので、ゲームの実行中かは確かめない（8-U2）。
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
	if err := reupload(ctx); err != nil {
		return fmt.Errorf("reupload game %s: %w", gameID, err)
	}
	return nil
}
