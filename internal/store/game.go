// ゲームの登録（games と game_locals への書き込み）を行う。
package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// NewGame は登録するゲームの値。ID と日時は呼び出し側が決める。
type NewGame struct {
	ID             string
	Title          string
	Brand          string // 空文字なら未設定として NULL を書く
	SaveFolderPath string
	ExePath        string
	CreatedAt      time.Time
}

// CreateGame は games と game_locals に 1 行ずつ書き込む。
func (s *Store) CreateGame(ctx context.Context, g NewGame) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	brand := sql.NullString{String: g.Brand, Valid: g.Brand != ""}
	createdAt := formatTime(g.CreatedAt)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO games (id, title, brand, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		g.ID, g.Title, brand, createdAt, createdAt); err != nil {
		return fmt.Errorf("insert games: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO game_locals (game_id, save_folder_path, exe_path) VALUES (?, ?, ?)`,
		g.ID, g.SaveFolderPath, g.ExePath); err != nil {
		return fmt.Errorf("insert game_locals: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
