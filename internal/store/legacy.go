// 旧版から読み込んだ未紐付けの旧履歴（legacy_sessions）の読み書きを行う（仕様書 9 章）。
package store

import (
	"context"
	"database/sql"
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
	// タイトルは played_at が最も新しい行から取る。MAX の素の列に任せると、played_at が同じ行が複数あるときにどの行から
	// 来るかが決まらないので、id でも順を付けて 1 行に決める。played_at は固定幅の形で持つので、文字列の順が時刻の順になる
	rows, err := s.db.QueryContext(ctx,
		`SELECT legacy_game_id, legacy_game_title, sessions, total_sec, played_at FROM (
		   SELECT legacy_game_id, legacy_game_title, played_at,
		     COUNT(*) OVER w_all AS sessions, SUM(duration_sec) OVER w_all AS total_sec,
		     ROW_NUMBER() OVER (PARTITION BY legacy_game_id ORDER BY played_at DESC, id DESC) AS rn
		   FROM legacy_sessions
		   WINDOW w_all AS (PARTITION BY legacy_game_id)
		 ) WHERE rn = 1`)
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
		// 外部キーの生のエラーでは gameId が原因だと分からないので、旧履歴がないときと同じ文言にそろえる
		if regErr := checkRegistered(ctx, tx, gameID); regErr != nil {
			return legacy.LinkResult{}, regErr
		}
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
	if err := checkRegistered(ctx, tx, gameID); err != nil {
		return legacy.LinkResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return legacy.LinkResult{}, fmt.Errorf("commit: %w", err)
	}
	return legacy.LinkResult{Linked: int(linked), Dropped: int(removed - linked)}, nil
}

func checkRegistered(ctx context.Context, tx *sql.Tx, gameID string) error {
	var registered bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM games WHERE id = ?)`, gameID).Scan(&registered); err != nil {
		return fmt.Errorf("select games: %w", err)
	}
	if !registered {
		return fmt.Errorf("game %s is not registered", gameID)
	}
	return nil
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
