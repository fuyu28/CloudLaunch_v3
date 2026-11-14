// プレイ履歴（play_sessions）の読み書きと、メタ情報の同期が使うゲームのローカル情報の読み込みを行う。
package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/fuyu28/CloudLaunch_v3/internal/metasync"
)

// CreateSession は、PlayedAt を UTC のミリ秒に切り捨てて書く（仕様書 3 章の日時の形）。ID が仕様書 8-S6 に合わないときは
// 書かない。
func (s *Store) CreateSession(ctx context.Context, sess metasync.Session) error {
	if !metasync.ValidSessionID(sess.ID) {
		return fmt.Errorf("invalid session id: %q", sess.ID)
	}
	playedAt, err := formatTime(sess.PlayedAt)
	if err != nil {
		return fmt.Errorf("session %s: played_at: %w", sess.ID, err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO play_sessions (id, game_id, played_at, duration_sec) VALUES (?, ?, ?, ?)`,
		sess.ID, sess.GameID, playedAt, sess.DurationSec); err != nil {
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
	sessions, err := scanAll(rows, func(rows *sql.Rows) (metasync.Session, error) {
		sess := metasync.Session{GameID: gameID}
		var playedAt string
		if err := rows.Scan(&sess.ID, &playedAt, &sess.DurationSec); err != nil {
			return sess, fmt.Errorf("scan: %w", err)
		}
		var err error
		if sess.PlayedAt, err = parseTime(playedAt); err != nil {
			return sess, fmt.Errorf("parse played_at of session %s: %w", sess.ID, err)
		}
		return sess, nil
	})
	if err != nil {
		return nil, fmt.Errorf("select play_sessions: %w", err)
	}
	return sessions, nil
}

// ImportSessions は、sessions の GameID を見ずに gameID のセッションとして書く。ID と日時を CreateSession と同じく検証し、
// 1 件でも当たれば何も書かない。
func (s *Store) ImportSessions(ctx context.Context, gameID string, sessions []metasync.Session) (metasync.ImportResult, error) {
	playedAt := make([]string, len(sessions))
	for i, sess := range sessions {
		if !metasync.ValidSessionID(sess.ID) {
			return metasync.ImportResult{}, fmt.Errorf("invalid session id: %q", sess.ID)
		}
		var err error
		if playedAt[i], err = formatTime(sess.PlayedAt); err != nil {
			return metasync.ImportResult{}, fmt.Errorf("session %s: played_at: %w", sess.ID, err)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return metasync.ImportResult{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	insert, err := tx.PrepareContext(ctx,
		`INSERT INTO play_sessions (id, game_id, played_at, duration_sec) VALUES (?, ?, ?, ?) ON CONFLICT (id) DO NOTHING`)
	if err != nil {
		return metasync.ImportResult{}, fmt.Errorf("prepare insert play_sessions: %w", err)
	}
	defer func() { _ = insert.Close() }()

	var res metasync.ImportResult
	for i, sess := range sessions {
		// 先に SELECT で確かめてから INSERT すると、読んだ後に別の接続が書いたとき、WAL では書き込みへの格上げが
		// busy_timeout を待たずに失敗する。最初に書き込みを行い、入らなかったときだけ持ち主を読む
		r, err := insert.ExecContext(ctx, sess.ID, gameID, playedAt[i], sess.DurationSec)
		if err != nil {
			return metasync.ImportResult{}, fmt.Errorf("insert play_sessions: %w", err)
		}
		if n, err := r.RowsAffected(); err != nil {
			return metasync.ImportResult{}, fmt.Errorf("insert play_sessions: %w", err)
		} else if n == 1 {
			res.Inserted++
			continue
		}
		var owner string
		if err := tx.QueryRowContext(ctx, `SELECT game_id FROM play_sessions WHERE id = ?`, sess.ID).Scan(&owner); err != nil {
			return metasync.ImportResult{}, fmt.Errorf("select play_sessions: %w", err)
		}
		if owner != gameID {
			res.OtherGame = append(res.OtherGame, sess.ID)
		}
	}
	if err := tx.Commit(); err != nil {
		return metasync.ImportResult{}, fmt.Errorf("commit: %w", err)
	}
	return res, nil
}
