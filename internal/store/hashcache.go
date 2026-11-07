// ハッシュキャッシュ（仕様書 3 章）の読み書きを行う。
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

// HashCache は、ゲームの行を path の順に返す。
func (s *Store) HashCache(ctx context.Context, gameID string) ([]syncore.HashCacheEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT path, size, mtime_ns, sha256, hashed_at FROM hash_cache WHERE game_id = ? ORDER BY path`, gameID)
	if err != nil {
		return nil, fmt.Errorf("select hash_cache: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var entries []syncore.HashCacheEntry
	for rows.Next() {
		var e syncore.HashCacheEntry
		var hashedAt string
		if err := rows.Scan(&e.Path, &e.Size, &e.MTimeNs, &e.SHA256, &hashedAt); err != nil {
			return nil, fmt.Errorf("scan hash_cache: %w", err)
		}
		if e.HashedAt, err = time.Parse(timeLayout, hashedAt); err != nil {
			return nil, fmt.Errorf("parse hashed_at of %s: %w", e.Path, err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("select hash_cache: %w", err)
	}
	return entries, nil
}

// ReplaceHashCache は、ゲームの行を entries だけにする。GUI と CLI が同じ DB を開いても途中の状態を読ませないよう、
// 消すことと書くことを 1 つのトランザクションで行う。
func (s *Store) ReplaceHashCache(ctx context.Context, gameID string, entries []syncore.HashCacheEntry) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, `DELETE FROM hash_cache WHERE game_id = ?`, gameID); err != nil {
		return fmt.Errorf("delete hash_cache: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO hash_cache (game_id, path, size, mtime_ns, sha256, hashed_at) VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare insert hash_cache: %w", err)
	}
	defer func() { _ = stmt.Close() }()
	for _, e := range entries {
		if _, err := stmt.ExecContext(ctx, gameID, e.Path, e.Size, e.MTimeNs, e.SHA256, formatTime(e.HashedAt)); err != nil {
			return fmt.Errorf("insert hash_cache %s: %w", e.Path, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit hash_cache: %w", err)
	}
	return nil
}

func (s *Store) DeleteHashCache(ctx context.Context, gameID string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM hash_cache WHERE game_id = ?`, gameID); err != nil {
		return fmt.Errorf("delete hash_cache: %w", err)
	}
	return nil
}
