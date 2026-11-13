// プレイ履歴（play_sessions）の読み書きと、メタ情報の同期が使うゲームのローカル情報の読み込みを行う。
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/fuyu28/CloudLaunch_v3/internal/metasync"
)

// CreateSession は、PlayedAt を UTC のミリ秒に切り捨てて書く（仕様書 3 章の日時の形）。ID が仕様書 8-S6 に合わないときと、
// UTC の年が 4 桁に収まらないときは書かない。4 桁を超える年は固定長の形で読み戻せず、そのゲームの Sessions が毎回失敗するため。
func (s *Store) CreateSession(ctx context.Context, sess metasync.Session) error {
	if err := validateSession(sess); err != nil {
		return err
	}
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

func validateSession(sess metasync.Session) error {
	if !metasync.ValidSessionID(sess.ID) {
		return fmt.Errorf("invalid session id: %q", sess.ID)
	}
	if y := sess.PlayedAt.UTC().Year(); y < 0 || y > 9999 {
		return fmt.Errorf("played_at out of range: %s", sess.PlayedAt)
	}
	return nil
}

// ImportSessions は、sessions の GameID を見ずに gameID のセッションとして書く。ID と日時を CreateSession と同じく検証し、
// 1 件でも当たれば何も書かない。
func (s *Store) ImportSessions(ctx context.Context, gameID string, sessions []metasync.Session) (metasync.ImportResult, error) {
	for _, sess := range sessions {
		if err := validateSession(sess); err != nil {
			return metasync.ImportResult{}, err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return metasync.ImportResult{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var res metasync.ImportResult
	for _, sess := range sessions {
		// 先に SELECT で確かめてから INSERT すると、読んだ後に別の接続が書いたとき、WAL では書き込みへの格上げが
		// busy_timeout を待たずに失敗する。最初に書き込みを行い、入らなかったときだけ持ち主を読む
		r, err := tx.ExecContext(ctx,
			`INSERT INTO play_sessions (id, game_id, played_at, duration_sec) VALUES (?, ?, ?, ?) ON CONFLICT (id) DO NOTHING`,
			sess.ID, gameID, formatTime(sess.PlayedAt), sess.DurationSec)
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

func (s *Store) RemoteDeleted(ctx context.Context, gameID string) (bool, error) {
	var deleted bool
	if err := s.db.QueryRowContext(ctx,
		`SELECT remote_deleted_at IS NOT NULL FROM game_locals WHERE game_id = ?`, gameID,
	).Scan(&deleted); err != nil {
		return false, fmt.Errorf("select game_locals: %w", err)
	}
	return deleted, nil
}
