// 起動時の復旧（仕様書 6 章「起動時の復旧」）。
package syncore

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

type PullOperation struct {
	OpID      string
	GameID    string
	LivePath  string
	StagePath string
	OldPath   string
	CreatedAt time.Time
}

// RecoveryCase は、復旧した行が仕様書 6 章の復旧表のどの行に当たったかを表す。
type RecoveryCase int

const (
	RecoveryRemoveStage RecoveryCase = iota + 1 // 6-R1
	RecoveryRestoreOld                          // 6-R2
	RecoveryBackupOld                           // 6-R3
	RecoveryNothing                             // 6-R4
)

// String は仕様書の復旧表の ID を返す。
func (rc RecoveryCase) String() string {
	switch rc {
	case RecoveryRemoveStage:
		return "6-R1"
	case RecoveryRestoreOld:
		return "6-R2"
	case RecoveryBackupOld:
		return "6-R3"
	case RecoveryNothing:
		return "6-R4"
	}
	return fmt.Sprintf("RecoveryCase(%d)", int(rc))
}

type Recovery struct {
	OpID   string
	GameID string
	Case   RecoveryCase
}

// Recover は Pull ジャーナルの全行を復旧し、復旧できた行を返す。ある行で失敗しても残りの行を続け、
// 失敗した行はジャーナルに残して、エラーをまとめて返す。行どうしは別のゲームで、互いに影響しないため。
func (c *Core) Recover(ctx context.Context) ([]Recovery, error) {
	ops, err := c.local.ListPullOperations(ctx)
	if err != nil {
		return nil, fmt.Errorf("list pull journal: %w", err)
	}
	var recovered []Recovery
	var errs []error
	for _, op := range ops {
		rc, err := c.recoverOne(ctx, op)
		if err == nil {
			err = c.local.DeletePullOperation(ctx, op.OpID)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("recover pull %s of game %s: %w", op.OpID, op.GameID, err))
			continue
		}
		c.logger.InfoContext(ctx, "recovered pull", "game", op.GameID, "op", op.OpID, "case", rc)
		recovered = append(recovered, Recovery{OpID: op.OpID, GameID: op.GameID, Case: rc})
	}
	return recovered, errors.Join(errs...)
}

// recoverOne は、記録されたパスの状態から復旧表の行を選んで処理する。
func (c *Core) recoverOne(ctx context.Context, op PullOperation) (RecoveryCase, error) {
	hasStage, err := exists(op.StagePath)
	if err != nil {
		return 0, err
	}
	hasOld, err := exists(op.OldPath)
	if err != nil {
		return 0, err
	}
	hasLive, err := exists(op.LivePath)
	if err != nil {
		return 0, err
	}

	switch {
	case hasOld && hasLive && hasStage:
		return 0, fmt.Errorf("old %s, save folder %s and stage %s all exist; cannot tell which is the right save (6-R5)",
			op.OldPath, op.LivePath, op.StagePath)
	case hasStage && !hasOld:
		if err := os.RemoveAll(op.StagePath); err != nil {
			return 0, fmt.Errorf("remove stage: %w", err)
		}
		return RecoveryRemoveStage, nil
	case hasOld && !hasLive:
		if err := c.rename(op.OldPath, op.LivePath); err != nil {
			return 0, fmt.Errorf("move old back to save folder: %w", err)
		}
		if err := os.RemoveAll(op.StagePath); err != nil {
			return 0, fmt.Errorf("remove stage: %w", err)
		}
		return RecoveryRestoreOld, nil
	case hasOld:
		// base が書けていなくても、ここでは書かずに次の判定で記録させる（仕様書 6 章 6-R3）
		name := backupName(op.CreatedAt, op.OpID)
		if err := c.moveToBackup(ctx, op.GameID, op.OldPath, name); err != nil {
			return 0, fmt.Errorf("move old save folder to backup: %w", err)
		}
		c.pruneBackups(ctx, op.GameID, name)
		return RecoveryBackupOld, nil
	default:
		return RecoveryNothing, nil
	}
}

// exists は、存在しない以外のエラー（仕様書 6 章で行を残してエラーにする場合）をそのまま返す。
func exists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat %s: %w", path, err)
	}
	return true, nil
}
