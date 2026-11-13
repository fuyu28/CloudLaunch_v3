// ゲーム情報（games）の読み込み、メタ情報の同期による置き換え（仕様書 8 章「ゲーム情報」）、CLI からの編集を行う。
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/fuyu28/CloudLaunch_v3/internal/metasync"
)

// GameEdit は、nil の項目を変えない。Brand の空文字は brand を消す。
type GameEdit struct {
	Title       *string
	Brand       *string
	ImageSha256 *string
}

func (s *Store) GameInfo(ctx context.Context, gameID string) (metasync.GameInfo, error) {
	g := metasync.GameInfo{ID: gameID}
	var brand, image sql.NullString
	var createdAt, updatedAt string
	if err := s.db.QueryRowContext(ctx,
		`SELECT title, brand, image_sha256, created_at, updated_at FROM games WHERE id = ?`, gameID,
	).Scan(&g.Title, &brand, &image, &createdAt, &updatedAt); err != nil {
		return metasync.GameInfo{}, fmt.Errorf("select games: %w", err)
	}
	g.Brand, g.ImageSha256 = brand.String, image.String
	var err error
	if g.CreatedAt, err = time.Parse(timeLayout, createdAt); err != nil {
		return metasync.GameInfo{}, fmt.Errorf("parse created_at of game %s: %w", gameID, err)
	}
	if g.UpdatedAt, err = time.Parse(timeLayout, updatedAt); err != nil {
		return metasync.GameInfo{}, fmt.Errorf("parse updated_at of game %s: %w", gameID, err)
	}
	return g, nil
}

// ReplaceGameInfo は、updated_at の比較と書き換えを 1 つの UPDATE で行う。読んでから書くと、間に入った編集を消すため。
// 日時は固定長の形で持つので、文字列の大小が日時の前後と一致する（仕様書 3 章）。title・imageSha256・日時の形が
// 合わなければ、何も書かずにエラーを返す。
func (s *Store) ReplaceGameInfo(ctx context.Context, g metasync.GameInfo) (bool, error) {
	if g.Title == "" {
		return false, errors.New("empty title")
	}
	if g.ImageSha256 != "" && !metasync.ValidSHA256(g.ImageSha256) {
		return false, fmt.Errorf("invalid image sha256: %q", g.ImageSha256)
	}
	for _, t := range []time.Time{g.CreatedAt, g.UpdatedAt} {
		if err := validateTime(t); err != nil {
			return false, err
		}
	}
	updatedAt := formatTime(g.UpdatedAt)
	res, err := s.db.ExecContext(ctx,
		`UPDATE games SET title = ?, brand = ?, image_sha256 = ?, created_at = ?, updated_at = ? WHERE id = ? AND updated_at < ?`,
		g.Title, nullIfEmpty(g.Brand), nullIfEmpty(g.ImageSha256), formatTime(g.CreatedAt), updatedAt, g.ID, updatedAt)
	if err != nil {
		return false, fmt.Errorf("update games: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("update games: %w", err)
	}
	return n == 1, nil
}

// EditGame は、updated_at を now（ミリ秒に切り捨てる）と、今の updated_at に 1 ms を足した時刻の、大きい方にする。
// PC の時計が遅れていても、編集が直前の内容より新しくなり、後勝ち（仕様書 8-G2）で負けないようにするため。
// 今の値の読み出しと書き込みを 1 つの UPDATE で行い、間に入った同期の書き換えを基準にできるようにする。
// 今の updated_at が日時の上限（9999 年の最後）なら、strftime が NULL を返すので、updated_at を変えない。
func (s *Store) EditGame(ctx context.Context, gameID string, edit GameEdit, now time.Time) error {
	if edit.Title != nil && *edit.Title == "" {
		return errors.New("empty title")
	}
	if err := validateTime(now); err != nil {
		return err
	}
	var title sql.NullString
	if edit.Title != nil {
		title = sql.NullString{String: *edit.Title, Valid: true}
	}
	var brand string
	if edit.Brand != nil {
		brand = *edit.Brand
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE games SET
		   title = COALESCE(?, title),
		   brand = CASE WHEN ? THEN NULLIF(?, '') ELSE brand END,
		   updated_at = max(?, COALESCE(strftime('%Y-%m-%dT%H:%M:%fZ', updated_at, '+0.001 seconds'), updated_at))
		 WHERE id = ?`,
		title, edit.Brand != nil, brand, formatTime(now), gameID)
	if err != nil {
		return fmt.Errorf("update games: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update games: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("update games: game %s: %w", gameID, sql.ErrNoRows)
	}
	return nil
}

// validateTime は、UTC の年が 4 桁に収まらない日時を拒む。4 桁を超える年は固定長の形で読み戻せないため。
func validateTime(t time.Time) error {
	if y := t.UTC().Year(); y < 0 || y > 9999 {
		return fmt.Errorf("time out of range: %s", t)
	}
	return nil
}

func nullIfEmpty(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
