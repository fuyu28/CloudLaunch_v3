package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

func TestHashCache(t *testing.T) {
	hashedAt := time.Date(2026, 11, 3, 1, 2, 3, 4_000_000, time.UTC)
	rows := []syncore.HashCacheEntry{
		{Path: "a.sav", Size: 1, MTimeNs: 1_000_000_001, SHA256: strings.Repeat("a", 64), HashedAt: hashedAt},
		{Path: "dir/b.sav", Size: 2, MTimeNs: 2_000_000_002, SHA256: strings.Repeat("b", 64), HashedAt: hashedAt.Add(time.Second)},
	}

	t.Run("3章/ハッシュキャッシュ/置き換えた行を読める", func(t *testing.T) {
		s, _ := openTemp(t)
		createTestGame(t, s)
		ctx := context.Background()

		if err := s.ReplaceHashCache(ctx, testGameID, rows); err != nil {
			t.Fatalf("ReplaceHashCache: %v", err)
		}
		got, err := s.HashCache(ctx, testGameID)
		if err != nil {
			t.Fatalf("HashCache: %v", err)
		}
		if d := cmp.Diff(rows, got); d != "" {
			t.Errorf("rows (-want +got):\n%s", d)
		}
	})

	t.Run("3章/ハッシュキャッシュ/置き換えると、渡さなかった行は消える", func(t *testing.T) {
		s, _ := openTemp(t)
		createTestGame(t, s)
		ctx := context.Background()

		if err := s.ReplaceHashCache(ctx, testGameID, rows); err != nil {
			t.Fatalf("ReplaceHashCache: %v", err)
		}
		if err := s.ReplaceHashCache(ctx, testGameID, rows[1:]); err != nil {
			t.Fatalf("ReplaceHashCache: %v", err)
		}
		got, err := s.HashCache(ctx, testGameID)
		if err != nil {
			t.Fatalf("HashCache: %v", err)
		}
		if d := cmp.Diff(rows[1:], got); d != "" {
			t.Errorf("rows (-want +got):\n%s", d)
		}
	})

	t.Run("3章/ハッシュキャッシュ/行を消せる", func(t *testing.T) {
		s, _ := openTemp(t)
		createTestGame(t, s)
		ctx := context.Background()

		if err := s.ReplaceHashCache(ctx, testGameID, rows); err != nil {
			t.Fatalf("ReplaceHashCache: %v", err)
		}
		if err := s.DeleteHashCache(ctx, testGameID); err != nil {
			t.Fatalf("DeleteHashCache: %v", err)
		}
		got, err := s.HashCache(ctx, testGameID)
		if err != nil {
			t.Fatalf("HashCache: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("rows = %v, want none", got)
		}
	})

	t.Run("3章/ハッシュキャッシュ/ゲームを削除すると行も消える", func(t *testing.T) {
		s, _ := openTemp(t)
		createTestGame(t, s)
		ctx := context.Background()

		if err := s.ReplaceHashCache(ctx, testGameID, rows); err != nil {
			t.Fatalf("ReplaceHashCache: %v", err)
		}
		if _, err := s.db.ExecContext(ctx, `DELETE FROM games WHERE id = ?`, testGameID); err != nil {
			t.Fatalf("delete game: %v", err)
		}
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM hash_cache`).Scan(&n); err != nil {
			t.Fatalf("count hash_cache: %v", err)
		}
		if n != 0 {
			t.Errorf("行の数 = %d, want 0", n)
		}
	})
}
