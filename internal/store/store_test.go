package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func openTemp(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cloudlaunch.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func TestOpen(t *testing.T) {
	t.Run("store/空の DB に migration を最後まで適用できる", func(t *testing.T) {
		s, _ := openTemp(t)

		rows, err := s.db.Query(`SELECT name FROM sqlite_master
			WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name <> 'goose_db_version'
			ORDER BY name`)
		if err != nil {
			t.Fatalf("query tables: %v", err)
		}
		defer func() { _ = rows.Close() }()
		var got []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				t.Fatalf("scan: %v", err)
			}
			got = append(got, name)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("rows: %v", err)
		}

		want := []string{"game_locals", "games", "hash_cache", "legacy_sessions", "play_sessions", "pull_operations", "settings"}
		if d := cmp.Diff(want, got); d != "" {
			t.Errorf("tables (-want +got):\n%s", d)
		}
	})

	t.Run("store/同じ DB をもう一度開いても失敗しない", func(t *testing.T) {
		s, path := openTemp(t)
		if err := s.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}

		again, err := Open(context.Background(), path)
		if err != nil {
			t.Fatalf("Open again: %v", err)
		}
		if err := again.Close(); err != nil {
			t.Fatalf("Close again: %v", err)
		}
	})

	t.Run("store/存在しないゲームの game_locals は外部キー制約で拒否される", func(t *testing.T) {
		s, _ := openTemp(t)
		ctx := context.Background()

		// PRAGMA は接続ごとの設定なので、1 本目を握ったまま 2 本目の新しい接続でも効くことを確かめる
		first, err := s.db.Conn(ctx)
		if err != nil {
			t.Fatalf("Conn: %v", err)
		}
		defer func() { _ = first.Close() }()
		second, err := s.db.Conn(ctx)
		if err != nil {
			t.Fatalf("Conn: %v", err)
		}
		defer func() { _ = second.Close() }()

		for _, c := range []*sql.Conn{first, second} {
			_, err := c.ExecContext(ctx, `INSERT INTO game_locals (game_id, save_folder_path, exe_path)
				VALUES ('no-such-game', '/save', '/game.exe')`)
			if err == nil {
				t.Fatal("insert succeeded, want foreign key error")
			}
		}
	})
}
