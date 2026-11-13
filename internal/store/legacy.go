// 旧版から読み込んだ未紐付けの旧履歴（legacy_sessions）の読み書きを行う（仕様書 9 章）。
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/fuyu28/CloudLaunch_v3/internal/legacy"
)

// AddLegacySessions は、ID と日時を CreateSession と同じく検証し、1 件でも当たれば何も書かない。
func (s *Store) AddLegacySessions(ctx context.Context, sessions []legacy.Session) (int, error) {
	for _, sess := range sessions {
		if !legacy.ValidSessionID(sess.ID) {
			return 0, fmt.Errorf("invalid legacy session id: %q", sess.ID)
		}
		if err := validateTime(sess.PlayedAt); err != nil {
			return 0, err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	added := 0
	for _, sess := range sessions {
		// 先に SELECT で確かめてから INSERT すると、WAL では書き込みへの格上げが busy_timeout を待たずに失敗しうる
		// （ImportSessions と同じ）。play_sessions に同じ ID があるかは、INSERT の中で確かめる
		r, err := tx.ExecContext(ctx,
			`INSERT INTO legacy_sessions (id, legacy_game_id, legacy_game_title, played_at, duration_sec)
			 SELECT ?, ?, ?, ?, ? WHERE NOT EXISTS (SELECT 1 FROM play_sessions WHERE id = ?)
			 ON CONFLICT (id) DO NOTHING`,
			sess.ID, sess.GameID, sess.GameTitle, formatTime(sess.PlayedAt), sess.DurationSec, sess.ID)
		if err != nil {
			return 0, fmt.Errorf("insert legacy_sessions: %w", err)
		}
		n, err := r.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("insert legacy_sessions: %w", err)
		}
		added += int(n)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return added, nil
}

// LegacySessions は、played_at（同じなら id）の順に返す。
func (s *Store) LegacySessions(ctx context.Context) ([]legacy.Session, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, legacy_game_id, legacy_game_title, played_at, duration_sec FROM legacy_sessions ORDER BY played_at, id`)
	if err != nil {
		return nil, fmt.Errorf("select legacy_sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var sessions []legacy.Session
	for rows.Next() {
		var sess legacy.Session
		var playedAt string
		if err := rows.Scan(&sess.ID, &sess.GameID, &sess.GameTitle, &playedAt, &sess.DurationSec); err != nil {
			return nil, fmt.Errorf("scan legacy_sessions: %w", err)
		}
		if sess.PlayedAt, err = time.Parse(timeLayout, playedAt); err != nil {
			return nil, fmt.Errorf("parse played_at of legacy session %s: %w", sess.ID, err)
		}
		sessions = append(sessions, sess)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("select legacy_sessions: %w", err)
	}
	return sessions, nil
}
