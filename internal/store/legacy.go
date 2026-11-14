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

func (s *Store) LegacyGames(ctx context.Context) ([]legacy.Game, error) {
	// 集約に MAX を 1 つだけ使うと、SQLite は素の列（legacy_game_title）を MAX を取った行から返す。played_at は固定幅の
	// 形で持つので、文字列の MAX が最も新しい行になる
	rows, err := s.db.QueryContext(ctx,
		`SELECT legacy_game_id, legacy_game_title, COUNT(*), SUM(duration_sec), MAX(played_at)
		 FROM legacy_sessions GROUP BY legacy_game_id`)
	if err != nil {
		return nil, fmt.Errorf("select legacy_sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var games []legacy.Game
	for rows.Next() {
		var g legacy.Game
		var lastPlayedAt string
		if err := rows.Scan(&g.ID, &g.Title, &g.Sessions, &g.TotalSec, &lastPlayedAt); err != nil {
			return nil, fmt.Errorf("scan legacy_sessions: %w", err)
		}
		if g.LastPlayedAt, err = time.Parse(timeLayout, lastPlayedAt); err != nil {
			return nil, fmt.Errorf("parse played_at of legacy game %s: %w", g.ID, err)
		}
		games = append(games, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("select legacy_sessions: %w", err)
	}
	return games, nil
}

func (s *Store) LinkLegacySessions(ctx context.Context, legacyGameID, gameID string) (legacy.LinkResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return legacy.LinkResult{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// 最初に書き込みを行う（AddLegacySessions と同じ理由）。登録の確かめを最後にするのはこのためで、旧履歴がなければ
	// INSERT は外部キーに当たらないので、確かめないと登録していない gameId が 0 件として通る
	inserted, err := tx.ExecContext(ctx,
		`INSERT INTO play_sessions (id, game_id, played_at, duration_sec)
		 SELECT id, ?, played_at, duration_sec FROM legacy_sessions WHERE legacy_game_id = ?
		 ON CONFLICT (id) DO NOTHING`,
		gameID, legacyGameID)
	if err != nil {
		return legacy.LinkResult{}, fmt.Errorf("insert play_sessions: %w", err)
	}
	linked, err := inserted.RowsAffected()
	if err != nil {
		return legacy.LinkResult{}, fmt.Errorf("insert play_sessions: %w", err)
	}
	deleted, err := tx.ExecContext(ctx, `DELETE FROM legacy_sessions WHERE legacy_game_id = ?`, legacyGameID)
	if err != nil {
		return legacy.LinkResult{}, fmt.Errorf("delete legacy_sessions: %w", err)
	}
	removed, err := deleted.RowsAffected()
	if err != nil {
		return legacy.LinkResult{}, fmt.Errorf("delete legacy_sessions: %w", err)
	}
	var registered bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM games WHERE id = ?)`, gameID).Scan(&registered); err != nil {
		return legacy.LinkResult{}, fmt.Errorf("select games: %w", err)
	}
	if !registered {
		return legacy.LinkResult{}, fmt.Errorf("game %s is not registered", gameID)
	}
	if err := tx.Commit(); err != nil {
		return legacy.LinkResult{}, fmt.Errorf("commit: %w", err)
	}
	return legacy.LinkResult{Linked: int(linked), Dropped: int(removed - linked)}, nil
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
