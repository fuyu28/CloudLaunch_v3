// ゲームのリモートの削除の手順（仕様書 8 章「ゲームの削除」の 8-X2〜8-X13）。tombstone の PUT と配下の削除は、呼び出し側が
// deleteRemote として渡す。
package syncore

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

var (
	// ErrPullPending は、Pull ジャーナルが残っていて、ゲームを削除できないことを表す。復旧（Recover）で片付けてから削除する
	ErrPullPending = errors.New("pull journal remains for the game")
	// ErrRemoteDeletionFailed は、DeleteGame に渡した deleteRemote が失敗したことを表す。それより前の手順で止まったときは
	// 何も消えていないが、これで止まったときは、リモートで削除されたかどうかは deleteRemote のエラーによる
	ErrRemoteDeletionFailed = errors.New("remote deletion failed")
)

// DeleteGame は、同期ロックを握ったまま、Pull ジャーナルと tombstone を確かめ、実行中でなければセーブをバックアップへコピーし、
// deleteRemote（tombstone の PUT・DB への記録・配下の削除）を呼んでから、登録を解除する（仕様書 8-X2〜8-X7）。
// 呼び出し側は、強い確認（8-X4）を取ってから呼ぶ。deleteRemote が失敗したら、登録を残して ErrRemoteDeletionFailed で包んだ
// エラーを返す。
func (c *Core) DeleteGame(ctx context.Context, gameID string, deleteRemote func(ctx context.Context) error) error {
	if err := c.checkDeleteConfig(deleteRemote); err != nil {
		return fmt.Errorf("delete game %s: %w", gameID, err)
	}
	release, err := c.acquire(ctx)
	if err != nil {
		return fmt.Errorf("delete game %s: %w", gameID, err)
	}
	defer release()
	startedAt := c.cfg.Now()

	if err := c.ensureNoPullJournal(ctx, gameID); err != nil {
		return fmt.Errorf("delete game %s: %w", gameID, err)
	}
	t, deleted, err := c.readTombstone(ctx, gameID)
	if err != nil {
		return fmt.Errorf("delete game %s: %w", gameID, err)
	}
	if deleted {
		return fmt.Errorf("delete game %s: deleted by %s at %s: %w", gameID, t.DeviceName, t.DeletedAt.Format(timeLayout), ErrRemoteDeleted)
	}
	game, err := c.cfg.Local.GameLocal(ctx, gameID)
	if err != nil {
		return fmt.Errorf("read game %s: %w", gameID, err)
	}
	if err := c.ensureNotRunningForDelete(game.ExePath); err != nil {
		return fmt.Errorf("delete game %s: %w", gameID, err)
	}
	if err := c.backupForDelete(ctx, gameID, game.SaveFolderPath, startedAt); err != nil {
		return fmt.Errorf("delete game %s: %w", gameID, err)
	}

	if err := deleteRemote(ctx); err != nil {
		return fmt.Errorf("delete game %s: %w: %w", gameID, ErrRemoteDeletionFailed, err)
	}
	if err := c.cfg.Local.DeleteGame(ctx, gameID); err != nil {
		return fmt.Errorf("unregister game %s: %w", gameID, err)
	}
	return nil
}

// checkDeleteConfig は、ロックだけを取って何もせずに終わらないよう、手順で使う設定をロックの前に確かめる。
func (c *Core) checkDeleteConfig(deleteRemote func(ctx context.Context) error) error {
	switch {
	case deleteRemote == nil:
		return errors.New("remote deletion is not given")
	case c.cfg.ReadTombstone == nil:
		return errReadTombstoneNotSet
	case c.cfg.Processes == nil:
		return errors.New("process checker is not configured")
	case c.cfg.BackupsDir == "":
		return errBackupsDirNotSet
	case c.cfg.Rename == nil:
		return errRenameNotSet
	}
	return nil
}

func (c *Core) ensureNoPullJournal(ctx context.Context, gameID string) error {
	ops, err := c.cfg.Local.ListPullOperations(ctx)
	if err != nil {
		return fmt.Errorf("read pull journal: %w", err)
	}
	for _, op := range ops {
		if op.GameID == gameID {
			return fmt.Errorf("op %s: %w", op.OpID, ErrPullPending)
		}
	}
	return nil
}

// ensureNotRunningForDelete は、7 章と違い、実行ファイルがなければ実行中でないとして通す（仕様書 8-X13）。
// ensureGameNotRunning に分岐を足すと、Push・Pull の呼び出しにも 8-X13 の扱いが見えるので、ここで分ける。
func (c *Core) ensureNotRunningForDelete(exePath string) error {
	if _, err := os.Stat(exePath); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return c.ensureGameNotRunning(exePath)
}

// backupForDelete は、セーブフォルダを Pull のバックアップと同じ置き場所・名前の形でコピーし、3 個に保つ（仕様書 8-X4）。
// セーブフォルダがなければ、何もせずにログを残す（8-X12）。
func (c *Core) backupForDelete(ctx context.Context, gameID, saveDir string, startedAt time.Time) error {
	ok, err := exists(saveDir)
	if err != nil {
		return err
	}
	if !ok {
		c.cfg.Logger.InfoContext(ctx, "save folder does not exist; skip backup", "game", gameID, "path", saveDir)
		return nil
	}
	u, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("generate op id: %w", err)
	}
	name := backupName(startedAt, u.String())
	dir := filepath.Join(c.cfg.BackupsDir, gameID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create backups directory: %w", err)
	}
	if err := c.copyToBackup(ctx, dir, saveDir, name); err != nil {
		return err
	}
	c.pruneBackups(ctx, gameID, name)
	return nil
}
