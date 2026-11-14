// ゲームの登録（games と game_locals への書き込み）と登録の解除、同期コアが使うゲームのローカル情報の読み込みを行う。
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/fuyu28/CloudLaunch_v3/internal/metasync"
	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
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

// RegisterGame は、リモートから取り込むゲームを登録する（仕様書 8-N3・8-N4）。CreateGame と違い、createdAt・updatedAt と
// imageSha256 も g のものをそのまま書く。base は書かない。
func (s *Store) RegisterGame(ctx context.Context, g metasync.GameInfo, saveFolderPath, exePath string) error {
	if err := validateGameInfo(g); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO games (id, title, brand, image_sha256, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		g.ID, g.Title, nullIfEmpty(g.Brand), nullIfEmpty(g.ImageSha256), formatTime(g.CreatedAt), formatTime(g.UpdatedAt),
	); err != nil {
		return fmt.Errorf("insert games: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO game_locals (game_id, save_folder_path, exe_path) VALUES (?, ?, ?)`,
		g.ID, saveFolderPath, exePath); err != nil {
		return fmt.Errorf("insert game_locals: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// ErrPullInProgress は、Pull ジャーナルが残っていて、ゲームの登録を解除できないことを表す。復旧で片付けてから解除する。
var ErrPullInProgress = errors.New("pull journal remains for the game")

// DeleteGame は、ゲームの登録を解除する（仕様書 8-X1）。ゲーム情報の行を消し、ON DELETE CASCADE でローカル情報とプレイ履歴も
// 消す。Pull ジャーナルが残っていれば ErrPullInProgress を返す。pull_operations の ON DELETE RESTRICT も止めるが、
// 制約違反のエラーでは呼び出し側が復旧を案内できないので、同じトランザクションで先に確かめる。
func (s *Store) DeleteGame(ctx context.Context, gameID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var pulls int
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM pull_operations WHERE game_id = ?`, gameID).Scan(&pulls); err != nil {
		return fmt.Errorf("select pull_operations: %w", err)
	}
	if pulls > 0 {
		return fmt.Errorf("delete game %s: %w", gameID, ErrPullInProgress)
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM games WHERE id = ?`, gameID)
	if err != nil {
		return fmt.Errorf("delete games: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete games: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("delete games: game %s: %w", gameID, sql.ErrNoRows)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func (s *Store) GameIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM games ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("select games: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan games: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("select games: %w", err)
	}
	return ids, nil
}

// GameLocal は、base が未設定なら BaseManifestHash に空文字を返す。
func (s *Store) GameLocal(ctx context.Context, gameID string) (syncore.GameLocal, error) {
	var g syncore.GameLocal
	var base sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT save_folder_path, exe_path, base_manifest_hash FROM game_locals WHERE game_id = ?`, gameID,
	).Scan(&g.SaveFolderPath, &g.ExePath, &base); err != nil {
		return syncore.GameLocal{}, fmt.Errorf("select game_locals: %w", err)
	}
	g.BaseManifestHash = base.String
	return g, nil
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
