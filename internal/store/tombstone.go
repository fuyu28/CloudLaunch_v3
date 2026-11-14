// ゲームの tombstone を見つけたときの記録（仕様書 3 章「DB に持つもの」・8-T2）の読み書きを行う。
package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/fuyu28/CloudLaunch_v3/internal/metasync"
)

func (s *Store) RemoteDeleted(ctx context.Context, gameID string) (metasync.Tombstone, bool, error) {
	var deletedAt, deviceName sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT remote_deleted_at, remote_deleted_by FROM game_locals WHERE game_id = ?`, gameID,
	).Scan(&deletedAt, &deviceName); err != nil {
		return metasync.Tombstone{}, false, fmt.Errorf("select game_locals: %w", err)
	}
	if !deletedAt.Valid {
		return metasync.Tombstone{}, false, nil
	}
	t, err := time.Parse(timeLayout, deletedAt.String)
	if err != nil {
		return metasync.Tombstone{}, false, fmt.Errorf("parse remote_deleted_at of game %s: %w", gameID, err)
	}
	return metasync.Tombstone{DeletedAt: t, DeviceName: deviceName.String}, true, nil
}

// RecordRemoteDeleted は、DeletedAt を UTC のミリ秒に切り捨てて書く（仕様書 3 章の日時の形）。
func (s *Store) RecordRemoteDeleted(ctx context.Context, gameID string, t metasync.Tombstone) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE game_locals SET remote_deleted_at = ?, remote_deleted_by = ? WHERE game_id = ?`,
		formatTime(t.DeletedAt), t.DeviceName, gameID)
	if err != nil {
		return fmt.Errorf("update remote deleted: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update remote deleted: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("update remote deleted: game %s: %w", gameID, sql.ErrNoRows)
	}
	return nil
}
