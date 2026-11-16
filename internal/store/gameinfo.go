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

// GameEdit は、nil の項目を変えない。Brand と ImageSha256 の空文字は、その項目を消す。
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
	if g.CreatedAt, err = parseTime(createdAt); err != nil {
		return metasync.GameInfo{}, fmt.Errorf("parse created_at of game %s: %w", gameID, err)
	}
	if g.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return metasync.GameInfo{}, fmt.Errorf("parse updated_at of game %s: %w", gameID, err)
	}
	return g, nil
}

type GameListItem struct {
	ID    string
	Title string
	// Brand は、未設定なら空
	Brand string
}

// ListGames は ID の順に返す。ID を読んでからゲームごとに読むと、間に消えたゲームで一覧全体が読めなくなるので、1 つの
// SELECT で読む。
func (s *Store) ListGames(ctx context.Context) ([]GameListItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, title, brand FROM games ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("select games: %w", err)
	}
	games, err := scanAll(rows, func(rows *sql.Rows) (GameListItem, error) {
		var g GameListItem
		var brand sql.NullString
		if err := rows.Scan(&g.ID, &g.Title, &brand); err != nil {
			return GameListItem{}, fmt.Errorf("scan: %w", err)
		}
		g.Brand = brand.String
		return g, nil
	})
	if err != nil {
		return nil, fmt.Errorf("select games: %w", err)
	}
	return games, nil
}

// ReplaceGameInfo は、updated_at の比較と書き換えを 1 つの UPDATE で行う。読んでから書くと、間に入った編集を消すため。
// 日時は固定長の形で持つので、文字列の大小が日時の前後と一致する（仕様書 3 章）。title・imageSha256・日時の形が
// 合わなければ、何も書かずにエラーを返す。
func (s *Store) ReplaceGameInfo(ctx context.Context, g metasync.GameInfo) (bool, error) {
	r, err := newGameRow(g)
	if err != nil {
		return false, err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE games SET title = ?, brand = ?, image_sha256 = ?, created_at = ?, updated_at = ? WHERE id = ? AND updated_at < ?`,
		r.title, r.brand, r.imageSha256, r.createdAt, r.updatedAt, r.id, r.updatedAt)
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
	nowStr, err := formatTime(now)
	if err != nil {
		return fmt.Errorf("game %s: updated_at: %w", gameID, err)
	}
	var title sql.NullString
	if edit.Title != nil {
		title = sql.NullString{String: *edit.Title, Valid: true}
	}
	var brand, image string
	if edit.Brand != nil {
		brand = *edit.Brand
	}
	if edit.ImageSha256 != nil {
		image = *edit.ImageSha256
		if image != "" && !metasync.ValidSHA256(image) {
			return fmt.Errorf("invalid image sha256: %q", image)
		}
	}
	return execAffected(ctx, s.db, "update games", "game "+gameID,
		`UPDATE games SET
		   title = COALESCE(?, title),
		   brand = CASE WHEN ? THEN NULLIF(?, '') ELSE brand END,
		   image_sha256 = CASE WHEN ? THEN NULLIF(?, '') ELSE image_sha256 END,
		   updated_at = max(?, COALESCE(strftime('%Y-%m-%dT%H:%M:%fZ', updated_at, '+0.001 seconds'), updated_at))
		 WHERE id = ?`,
		title, edit.Brand != nil, brand, edit.ImageSha256 != nil, image, nowStr, gameID)
}

// SetImageSha256 は、sha が空なら画像を外す。updated_at は EditGame と同じく進める。
func (s *Store) SetImageSha256(ctx context.Context, gameID, sha string, now time.Time) error {
	return s.EditGame(ctx, gameID, GameEdit{ImageSha256: &sha}, now)
}

// gameRow は、games の 1 行に書く値。newGameRow で作り、トランザクションを始める前に入力を確かめ終える。
type gameRow struct {
	id, title            string
	brand, imageSha256   sql.NullString
	createdAt, updatedAt string
}

// newGameRow は、title・imageSha256 を DB の CHECK 制約より分かりやすいエラーで弾く。
func newGameRow(g metasync.GameInfo) (gameRow, error) {
	if g.Title == "" {
		return gameRow{}, errors.New("empty title")
	}
	if g.ImageSha256 != "" && !metasync.ValidSHA256(g.ImageSha256) {
		return gameRow{}, fmt.Errorf("invalid image sha256: %q", g.ImageSha256)
	}
	createdAt, err := formatTime(g.CreatedAt)
	if err != nil {
		return gameRow{}, fmt.Errorf("game %s: created_at: %w", g.ID, err)
	}
	updatedAt, err := formatTime(g.UpdatedAt)
	if err != nil {
		return gameRow{}, fmt.Errorf("game %s: updated_at: %w", g.ID, err)
	}
	return gameRow{
		id: g.ID, title: g.Title, brand: nullIfEmpty(g.Brand), imageSha256: nullIfEmpty(g.ImageSha256),
		createdAt: createdAt, updatedAt: updatedAt,
	}, nil
}

func (r gameRow) insert(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO games (id, title, brand, image_sha256, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		r.id, r.title, r.brand, r.imageSha256, r.createdAt, r.updatedAt,
	); err != nil {
		return fmt.Errorf("insert games: %w", err)
	}
	return nil
}

func nullIfEmpty(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
