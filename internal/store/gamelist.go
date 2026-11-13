// GUI のホーム画面に出すゲームの一覧を読み込む。
package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type GameSummary struct {
	ID           string
	Title        string
	Brand        string
	LastPlayedAt time.Time // 一度も遊んでいなければゼロ値
	TotalSec     int64
	CreatedAt    time.Time
}

type GameSort string

const (
	SortTitle      GameSort = "title"
	SortLastPlayed GameSort = "lastPlayed"
	SortCreated    GameSort = "created"
	SortPlayTime   GameSort = "playTime"
	SortBrand      GameSort = "brand"
)

// orderBy の値は SQL に埋め込むので、利用者の入力をそのまま使わず、この表で引く。
var orderBy = map[GameSort]string{
	SortTitle:      "g.title",
	SortLastPlayed: "last_played_at",
	SortCreated:    "g.created_at",
	SortPlayTime:   "total_sec",
	SortBrand:      "COALESCE(g.brand, '')",
}

// ListGames は、query をタイトルかブランドに含むゲームを返す。sort が未知なら title 順にする。
func (s *Store) ListGames(ctx context.Context, query string, sort GameSort, desc bool) ([]GameSummary, error) {
	col, ok := orderBy[sort]
	if !ok {
		col = orderBy[SortTitle]
	}
	dir := "ASC"
	if desc {
		dir = "DESC"
	}
	like := "%" + escapeLike(strings.TrimSpace(query)) + "%"
	rows, err := s.db.QueryContext(ctx, `
SELECT g.id, g.title, COALESCE(g.brand, ''), g.created_at,
       MAX(p.played_at) AS last_played_at,
       COALESCE(SUM(p.duration_sec), 0) AS total_sec
FROM games g
LEFT JOIN play_sessions p ON p.game_id = g.id
WHERE g.title LIKE ? ESCAPE '\' OR COALESCE(g.brand, '') LIKE ? ESCAPE '\'
GROUP BY g.id
ORDER BY `+col+` `+dir+`, g.title ASC`, like, like)
	if err != nil {
		return nil, fmt.Errorf("select games: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var games []GameSummary
	for rows.Next() {
		var g GameSummary
		var createdAt string
		var lastPlayed sql.NullString
		if err := rows.Scan(&g.ID, &g.Title, &g.Brand, &createdAt, &lastPlayed, &g.TotalSec); err != nil {
			return nil, fmt.Errorf("scan games: %w", err)
		}
		if g.CreatedAt, err = time.Parse(timeLayout, createdAt); err != nil {
			return nil, fmt.Errorf("parse created_at: %w", err)
		}
		if lastPlayed.Valid {
			if g.LastPlayedAt, err = time.Parse(timeLayout, lastPlayed.String); err != nil {
				return nil, fmt.Errorf("parse played_at: %w", err)
			}
		}
		games = append(games, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate games: %w", err)
	}
	return games, nil
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
