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
	created, err := formatTime(createdAt)
	if err != nil {
		return fmt.Errorf("pull operation %s: created_at: %w", opID, err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO pull_operations (op_id, game_id, live_path, stage_path, old_path, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		opID, gameID, livePath, stagePath, oldPath, created); err != nil {
		return fmt.Errorf("insert pull_operations: %w", err)
	}
	return nil
}

// ListPullOperations は、行を書いた順（created_at、同じなら op_id の順）に返す。
func (s *Store) ListPullOperations(ctx context.Context) ([]syncore.PullOperation, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT op_id, game_id, live_path, stage_path, old_path, created_at FROM pull_operations ORDER BY created_at, op_id`)
	if err != nil {
		return nil, fmt.Errorf("select pull_operations: %w", err)
	}
	ops, err := scanAll(rows, func(rows *sql.Rows) (syncore.PullOperation, error) {
		var op syncore.PullOperation
		var createdAt string
		if err := rows.Scan(&op.OpID, &op.GameID, &op.LivePath, &op.StagePath, &op.OldPath, &createdAt); err != nil {
			return op, fmt.Errorf("scan: %w", err)
		}
		var err error
		if op.CreatedAt, err = parseTime(createdAt); err != nil {
			return op, fmt.Errorf("parse created_at of pull operation %s: %w", op.OpID, err)
		}
		return op, nil
	})
	if err != nil {
		return nil, fmt.Errorf("select pull_operations: %w", err)
	}
	return ops, nil
}

// DeletePullOperation は、opID の行がなければ sql.ErrNoRows を返す。
func (s *Store) DeletePullOperation(ctx context.Context, opID string) error {
	return execAffected(ctx, s.db, "delete pull_operations", "op "+opID, `DELETE FROM pull_operations WHERE op_id = ?`, opID)
}
