// プレイ履歴（play_sessions）の読み書きと、メタ情報の同期が使うゲームのローカル情報の読み込みを行う。
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/fuyu28/CloudLaunch_v3/internal/metasync"
)

// CreateSession は、PlayedAt を UTC のミリ秒に切り捨てて書く（仕様書 3 章の日時の形）。
func (s *Store) CreateSession(ctx context.Context, sess metasync.Session) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO play_sessions (id, game_id, played_at, duration_sec) VALUES (?, ?, ?, ?)`,
		sess.ID, sess.GameID, formatTime(sess.PlayedAt), sess.DurationSec); err != nil {
		return fmt.Errorf("insert play_sessions: %w", err)
	}
	return nil
}

// Sessions は、played_at（同じなら id）の順に返す。
func (s *Store) Sessions(ctx context.Context, gameID string) ([]metasync.Session, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, played_at, duration_sec FROM play_sessions WHERE game_id = ? ORDER BY played_at, id`, gameID)
	if err != nil {
		return nil, fmt.Errorf("select play_sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var sessions []metasync.Session
	for rows.Next() {
		sess := metasync.Session{GameID: gameID}
		var playedAt string
		if err := rows.Scan(&sess.ID, &playedAt, &sess.DurationSec); err != nil {
			return nil, fmt.Errorf("scan play_sessions: %w", err)
		}
		if sess.PlayedAt, err = time.Parse(timeLayout, playedAt); err != nil {
			return nil, fmt.Errorf("parse played_at of session %s: %w", sess.ID, err)
		}
		sessions = append(sessions, sess)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("select play_sessions: %w", err)
	}
	return sessions, nil
}

func (s *Store) RemoteDeleted(ctx context.Context, gameID string) (bool, error) {
	var deleted bool
	if err := s.db.QueryRowContext(ctx,
		`SELECT remote_deleted_at IS NOT NULL FROM game_locals WHERE game_id = ?`, gameID,
	).Scan(&deleted); err != nil {
		return false, fmt.Errorf("select game_locals: %w", err)
	}
	return deleted, nil
}
