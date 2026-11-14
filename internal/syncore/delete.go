// ゲームのリモートの削除の手順（仕様書 8 章「ゲームの削除」の 8-X2〜8-X13）。
package syncore

import (
	"bytes"
	"context"
	"encoding/json"
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
	// ErrRemoteDeletionFailed は、リモートの削除（tombstone の PUT・DB への記録・配下の削除）で止まったことを表す。それより
	// 前の手順で止まったときは何も消えていない。PUT で止まったときは、PUT が届いたかは分からない（8-X9）。PUT の後で止まった
	// ときは、ErrDeleteIncomplete でも包む
	ErrRemoteDeletionFailed = errors.New("remote deletion failed")
	// ErrDeleteIncomplete は、deleted.json の PUT に成功した後で止まったことを表す。リモートでは削除済みに見え、記録と
	// 消し残りは次の HandleTombstone が片付ける（仕様書 8-X10）
	ErrDeleteIncomplete = errors.New("game is deleted on remote but the deletion is incomplete")
	// ErrUnregisterFailed は、リモートの削除を終えた後で、この PC の登録の解除に失敗したことを表す。登録だけを解除し直す
	ErrUnregisterFailed = errors.New("remote data is deleted but the game is still registered")
)

type DeleteResult struct {
	// BackupPath は、作ったバックアップのディレクトリ。セーブフォルダがなく作らなかったときは空（仕様書 8-X12）
	BackupPath string
}

// DeleteGame は、同期ロックを握ったまま、Pull ジャーナルと tombstone を確かめ、実行中でなければセーブをバックアップへコピーし、
// tombstone を PUT して DB に記録し、配下を削除してから、登録を解除する（仕様書 8-X2〜8-X7）。tombstone の deletedAt は
// Config.Now、deviceName は Config.DeviceName にする。呼び出し側は、強い確認（8-X4）を取ってから呼ぶ。リモートの削除が
// 失敗したら、登録を残して ErrRemoteDeletionFailed で包んだエラーを返す。
func (c *Core) DeleteGame(ctx context.Context, gameID string) (DeleteResult, error) {
	if err := c.checkDeleteConfig(); err != nil {
		return DeleteResult{}, fmt.Errorf("delete game %s: %w", gameID, err)
	}
	release, err := c.acquire(ctx)
	if err != nil {
		return DeleteResult{}, fmt.Errorf("delete game %s: %w", gameID, err)
	}
	defer release()
	startedAt := c.cfg.Now()

	if err := c.ensureNoPullJournal(ctx, gameID); err != nil {
		return DeleteResult{}, fmt.Errorf("delete game %s: %w", gameID, err)
	}
	if err := c.ensureNotDeletedBeforeDelete(ctx, gameID); err != nil {
		return DeleteResult{}, fmt.Errorf("delete game %s: %w", gameID, err)
	}
	game, err := c.cfg.Local.GameLocal(ctx, gameID)
	if err != nil {
		return DeleteResult{}, fmt.Errorf("read game %s: %w", gameID, err)
	}
	if err := c.ensureNotRunningForDelete(game.ExePath); err != nil {
		return DeleteResult{}, fmt.Errorf("delete game %s: %w", gameID, err)
	}
	backup, err := c.backupForDelete(ctx, gameID, game.SaveFolderPath, startedAt)
	if err != nil {
		return DeleteResult{}, fmt.Errorf("delete game %s: %w", gameID, err)
	}
	res := DeleteResult{BackupPath: backup}

	// コピーは長くかかることがあるので、Push・Pull の転送の後と同じく確かめ直す（7 章）。間にゲームが起動されると、バックアップに
	// 書き込みの途中のセーブが写ったまま、リモートの正しいセーブを消してしまう。tombstone も読み直し、ほかの PC が先に書いた
	// tombstone を上書きする間隔を、PUT の往復だけに縮める（8-X11）
	if err := c.ensureNotRunningForDelete(game.ExePath); err != nil {
		return res, fmt.Errorf("delete game %s: %w", gameID, err)
	}
	if err := c.ensureNotDeletedBeforeDelete(ctx, gameID); err != nil {
		return res, fmt.Errorf("delete game %s: %w", gameID, err)
	}

	if err := c.deleteRemote(ctx, gameID); err != nil {
		return res, fmt.Errorf("delete game %s: %w: %w", gameID, ErrRemoteDeletionFailed, err)
	}
	if err := c.cfg.Local.DeleteGame(ctx, gameID); err != nil {
		return res, fmt.Errorf("unregister game %s: %w: %w", gameID, ErrUnregisterFailed, err)
	}
	return res, nil
}

// deleteRemote は、tombstone を PUT し、DB に記録してから、配下の deleted.json 以外を削除する（仕様書 8-X5・8-X6）。PUT の
// 後の失敗は ErrDeleteIncomplete で包む。既に tombstone があるかは確かめないので、呼び出し側が先に確かめる（8-X11）。
func (c *Core) deleteRemote(ctx context.Context, gameID string) error {
	t := Tombstone{DeletedAt: c.cfg.Now(), DeviceName: c.cfg.DeviceName}
	body, err := json.Marshal(tombstoneJSON{DeletedAt: t.DeletedAt.UTC().Format(timeLayout), DeviceName: t.DeviceName})
	if err != nil {
		return fmt.Errorf("encode deleted.json: %w", err)
	}
	if err := c.cfg.Remote.Put(ctx, tombstoneKey(gameID), bytes.NewReader(body), int64(len(body))); err != nil {
		return fmt.Errorf("put deleted.json: %w", err)
	}
	if err := c.cfg.Local.RecordRemoteDeleted(ctx, gameID, t); err != nil {
		return fmt.Errorf("record remote deleted: %w: %w", ErrDeleteIncomplete, err)
	}
	if err := c.deleteGameData(ctx, gameID); err != nil {
		return fmt.Errorf("%w: %w", ErrDeleteIncomplete, err)
	}
	return nil
}

func (c *Core) ensureNotDeletedBeforeDelete(ctx context.Context, gameID string) error {
	t, deleted, err := c.CheckTombstone(ctx, gameID)
	if err != nil {
		return err
	}
	if deleted {
		return fmt.Errorf("deleted by %s at %s: %w", t.DeviceName, t.DeletedAt.Format(timeLayout), ErrRemoteDeleted)
	}
	return nil
}

// checkDeleteConfig は、ロックだけを取って何もせずに終わらないよう、手順で使う設定をロックの前に確かめる。
// DeviceName が空のまま tombstone を書くと 8-T5 に当たり、どの PC もそのゲームを確かめられなくなるので、空なら止める。
func (c *Core) checkDeleteConfig() error {
	switch {
	case c.cfg.Remote == nil:
		return errors.New("remote is not configured")
	case c.cfg.DeviceName == "":
		return errors.New("device name is not configured")
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
// 作ったバックアップのパスを返す。セーブフォルダがなければ、何もせずにログを残して空を返す（8-X12）。
func (c *Core) backupForDelete(ctx context.Context, gameID, saveDir string, startedAt time.Time) (string, error) {
	ok, err := exists(saveDir)
	if err != nil {
		return "", err
	}
	if !ok {
		c.cfg.Logger.InfoContext(ctx, "save folder does not exist; skip backup", "game", gameID, "path", saveDir)
		return "", nil
	}
	u, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate op id: %w", err)
	}
	name := backupName(startedAt, u.String())
	dir := filepath.Join(c.cfg.BackupsDir, gameID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create backups directory: %w", err)
	}
	if err := c.copyToBackup(ctx, dir, saveDir, name); err != nil {
		return "", err
	}
	c.pruneBackups(ctx, gameID, name)
	return filepath.Join(dir, name), nil
}
