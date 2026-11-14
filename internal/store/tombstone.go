// ゲームの tombstone を見つけたときの記録（仕様書 3 章「DB に持つもの」・8-T2）の読み書きを行う。
package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

func (s *Store) RemoteDeleted(ctx context.Context, gameID string) (syncore.Tombstone, bool, error) {
	var deletedAt, deviceName sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT remote_deleted_at, remote_deleted_by FROM game_locals WHERE game_id = ?`, gameID,
	).Scan(&deletedAt, &deviceName); err != nil {
		return syncore.Tombstone{}, false, fmt.Errorf("select game_locals: %w", err)
	}
	if !deletedAt.Valid {
		return syncore.Tombstone{}, false, nil
	}
	t, err := parseTime(deletedAt.String)
	if err != nil {
		return syncore.Tombstone{}, false, fmt.Errorf("parse remote_deleted_at of game %s: %w", gameID, err)
	}
	return syncore.Tombstone{DeletedAt: t, DeviceName: deviceName.String}, true, nil
}

// IsRemoteDeleted は、メタ情報の同期（8-M4）が使う。metasync は syncore の Tombstone を知らないので、RemoteDeleted とは
// 別に有無だけを返す。
func (s *Store) IsRemoteDeleted(ctx context.Context, gameID string) (bool, error) {
	_, ok, err := s.RemoteDeleted(ctx, gameID)
	return ok, err
}

// RecordRemoteDeleted は、DeletedAt を UTC のミリ秒に切り捨てて書く（仕様書 3 章の日時の形）。
func (s *Store) RecordRemoteDeleted(ctx context.Context, gameID string, t syncore.Tombstone) error {
	deletedAt, err := formatTime(t.DeletedAt)
	if err != nil {
		return fmt.Errorf("game %s: remote_deleted_at: %w", gameID, err)
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE game_locals SET remote_deleted_at = ?, remote_deleted_by = ? WHERE game_id = ?`,
		deletedAt, t.DeviceName, gameID)
	return checkAffected(res, err, "update remote deleted", "game "+gameID)
}
