// Pull ジャーナル（仕様書 6 章）の読み書きを行う。
package store

import (
	"context"
	"fmt"
	"time"
)

// InsertPullOperation は、同じゲームの行が残っていれば UNIQUE 制約で失敗する。
func (s *Store) InsertPullOperation(ctx context.Context, opID, gameID, livePath, stagePath, oldPath string, createdAt time.Time) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO pull_operations (op_id, game_id, live_path, stage_path, old_path, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		opID, gameID, livePath, stagePath, oldPath, formatTime(createdAt)); err != nil {
		return fmt.Errorf("insert pull_operations: %w", err)
	}
	return nil
}

func (s *Store) DeletePullOperation(ctx context.Context, opID string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM pull_operations WHERE op_id = ?`, opID); err != nil {
		return fmt.Errorf("delete pull_operations: %w", err)
	}
	return nil
}
