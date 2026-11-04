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

type Recovery struct {
	OpID   string
	GameID string
	Case   RecoveryCase
}

// Recover は Pull ジャーナルの全行を復旧し、復旧できた行を返す。ある行で失敗しても残りの行を続け、
// 失敗した行はジャーナルに残して、エラーをまとめて返す。行どうしは別のゲームで、互いに影響しないため。
func (c *Core) Recover(ctx context.Context) ([]Recovery, error) {
	release, err := c.acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("recover: %w", err)
	}
	defer release()
	ops, err := c.local.ListPullOperations(ctx)
	if err != nil {
		return nil, fmt.Errorf("list pull journal: %w", err)
	}
	var recovered []Recovery
	var errs []error
	for _, op := range ops {
		rc, err := c.recoverOne(ctx, op)
		if err == nil {
			// ファイルはもう直したので、キャンセルで行が残って失敗と伝わらないようにする
			err = c.local.DeletePullOperation(context.WithoutCancel(ctx), op.OpID)
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
		// stage を先に消す。old を戻した後で stage を消せないと、戻したことが伝わらず、次の復旧で 6-R1 と出るため。
		// 先に消して中断しても、old があり live がない状態のままなので、次も 6-R2 に当たる
		if err := os.RemoveAll(op.StagePath); err != nil {
			return 0, fmt.Errorf("remove stage: %w", err)
		}
		if err := c.rename(op.OldPath, op.LivePath); err != nil {
			return 0, fmt.Errorf("move old back to save folder: %w", err)
		}
		return RecoveryRestoreOld, nil
	case hasOld:
		// base が書けていなくても、ここでは書かずに次の判定で記録させる（仕様書 6 章 6-R3）
		if err := c.backupOld(ctx, op.GameID, op.OldPath, op.CreatedAt, op.OpID); err != nil {
			return 0, err
		}
		return RecoveryBackupOld, nil
	default:
		return RecoveryNothing, nil
	}
}

// exists は、存在しない以外のエラーを、存在しないとみなさずに包んで返す（仕様書 6 章で、パスの状態を確かめられない
// ときは行を残してエラーにするため）。
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
