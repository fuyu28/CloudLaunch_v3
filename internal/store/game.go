// ゲームの登録（games と game_locals への書き込み）と、同期コアが使うゲームのローカル情報の読み込みを行う。
package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// NewGame の ID と日時は呼び出し側が決める。
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

// GameLocal は、base が未設定なら baseManifestHash に空文字を返す。
func (s *Store) GameLocal(ctx context.Context, gameID string) (saveFolderPath, baseManifestHash string, err error) {
	var base sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT save_folder_path, base_manifest_hash FROM game_locals WHERE game_id = ?`, gameID,
	).Scan(&saveFolderPath, &base); err != nil {
		return "", "", fmt.Errorf("select game_locals: %w", err)
	}
	return saveFolderPath, base.String, nil
}

func (s *Store) SetBase(ctx context.Context, gameID, manifestHash string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE game_locals SET base_manifest_hash = ? WHERE game_id = ?`, manifestHash, gameID)
	if err != nil {
		return fmt.Errorf("update base: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update base: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("update base: game %s: %w", gameID, sql.ErrNoRows)
	}
	return nil
}
