// Pull ジャーナル（仕様書 6 章）の読み書きを行う。
package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
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

func (s *Store) ListPullOperations(ctx context.Context) ([]syncore.PullOperation, error) {
	return nil, nil
}

// DeletePullOperation は、opID の行がなければ sql.ErrNoRows を返す。
func (s *Store) DeletePullOperation(ctx context.Context, opID string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM pull_operations WHERE op_id = ?`, opID)
	if err != nil {
		return fmt.Errorf("delete pull_operations: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete pull_operations: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("delete pull_operations %s: %w", opID, sql.ErrNoRows)
	}
	return nil
}
