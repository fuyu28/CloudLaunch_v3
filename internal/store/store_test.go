package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/pressly/goose/v3"

	"github.com/fuyu28/CloudLaunch_v3/internal/legacy"
	"github.com/fuyu28/CloudLaunch_v3/internal/metasync"
	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

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

		want := []string{"game_locals", "games", "legacy_sessions", "play_sessions", "pull_operations", "settings"}
		if d := cmp.Diff(want, got); d != "" {
			t.Errorf("tables (-want +got):\n%s", d)
		}
	})

	t.Run("store/hash_cache に行がある version 1 の DB を開くと、表を消してゲームは残す", func(t *testing.T) {
		ctx := context.Background()
		path := filepath.Join(t.TempDir(), "cloudlaunch.db")
		db, err := sql.Open("sqlite", path+dsnParams)
		if err != nil {
			t.Fatalf("open db: %v", err)
		}
		dir, err := fs.Sub(migrations, "migrations")
		if err != nil {
			t.Fatalf("migrations dir: %v", err)
		}
		p, err := goose.NewProvider(goose.DialectSQLite3, db, dir)
		if err != nil {
			t.Fatalf("migration provider: %v", err)
		}
		if _, err := p.UpTo(ctx, 1); err != nil {
			t.Fatalf("migrate to 1: %v", err)
		}
		for _, q := range []string{
			`INSERT INTO games (id, title, created_at, updated_at) VALUES ('g1', 'Game', '2026-11-07T00:00:00.000Z', '2026-11-07T00:00:00.000Z')`,
			`INSERT INTO hash_cache (game_id, path, size, mtime_ns, sha256) VALUES ('g1', 'a.sav', 1, 1, '` + strings.Repeat("a", 64) + `')`,
		} {
			if _, err := db.ExecContext(ctx, q); err != nil {
				t.Fatalf("exec %q: %v", q, err)
			}
		}
		if err := db.Close(); err != nil {
			t.Fatalf("close db: %v", err)
		}

		s, err := Open(ctx, path)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		t.Cleanup(func() { _ = s.Close() })
		var tables, games int
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'hash_cache'`).Scan(&tables); err != nil {
			t.Fatalf("query tables: %v", err)
		}
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM games`).Scan(&games); err != nil {
			t.Fatalf("count games: %v", err)
		}
		if tables != 0 || games != 1 {
			t.Errorf("(hash_cache の表, games の行) = (%d, %d), want (0, 1)", tables, games)
		}
	})

	t.Run("3章/DB/synchronous は FULL", func(t *testing.T) {
		s, _ := openTemp(t)
		ctx := context.Background()

		// 新しい接続にも効くことを確かめるため、1 本目を握ったまま 2 本目を開く
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
			var got int
			if err := c.QueryRowContext(ctx, `PRAGMA synchronous`).Scan(&got); err != nil {
				t.Fatalf("PRAGMA synchronous: %v", err)
			}
			// 2 が FULL。WAL では NORMAL（1）だとコミットがディスクへ届く前に戻りうる
			if got != 2 {
				t.Errorf("synchronous = %d, want 2 (FULL)", got)
			}
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

// dumpTables は、goose の管理表を除くすべての表の行を、表の名前ごとに返す。
func dumpTables(t *testing.T, s *Store) map[string][][]any {
	t.Helper()
	ctx := context.Background()
	rows, err := s.db.QueryContext(ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name <> 'goose_db_version'`)
	if err != nil {
		t.Fatalf("select sqlite_master: %v", err)
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	tables := map[string][][]any{}
	for _, name := range names {
		r, err := s.db.QueryContext(ctx, fmt.Sprintf(`SELECT * FROM %q ORDER BY 1`, name))
		if err != nil {
			t.Fatalf("select %s: %v", name, err)
		}
		cols, err := r.Columns()
		if err != nil {
			t.Fatalf("columns %s: %v", name, err)
		}
		for r.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := r.Scan(ptrs...); err != nil {
				t.Fatalf("scan %s: %v", name, err)
			}
			tables[name] = append(tables[name], vals)
		}
		if err := r.Close(); err != nil {
			t.Fatalf("close %s: %v", name, err)
		}
	}
	return tables
}

func TestTimeOutOfRange(t *testing.T) {
	ctx := context.Background()
	const (
		otherGameID = "0192d4e8-0000-7000-8000-000000000002"
		sessionA    = "0192d4e8-0000-7000-8000-0000000000c1"
		sessionB    = "0192d4e8-0000-7000-8000-0000000000c2"
	)
	valid := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	legacySession := func(id string, at time.Time) legacy.Session {
		return legacy.Session{ID: id, GameID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1", GameTitle: "ゲーム", PlayedAt: at, DurationSec: 1}
	}
	title := "New"

	type testCase struct {
		op    string
		setup func(t *testing.T, s *Store)
		write func(s *Store, at time.Time) error
	}
	tests := []testCase{
		{
			op: "CreateGame",
			write: func(s *Store, at time.Time) error {
				return s.CreateGame(ctx, NewGame{ID: testGameID, Title: "Game", SaveFolderPath: "/save", ExePath: "/game.exe", CreatedAt: at})
			},
		},
	}
	// ゲーム情報は created_at と updated_at の両方を書くので、片方だけを範囲外にして、どちらも確かめていることを見る
	for _, field := range []string{"createdAt", "updatedAt"} {
		gameInfo := func(id string, at time.Time) metasync.GameInfo {
			g := metasync.GameInfo{ID: id, Title: "Remote", CreatedAt: valid, UpdatedAt: valid}
			if field == "createdAt" {
				g.CreatedAt = at
			} else {
				g.UpdatedAt = at
			}
			return g
		}
		tests = append(tests,
			testCase{
				op: "RegisterGame の " + field,
				write: func(s *Store, at time.Time) error {
					return s.RegisterGame(ctx, gameInfo(testGameID, at), "/save", "/game.exe")
				},
			},
			testCase{
				op: "ReuploadGame の " + field,
				setup: func(t *testing.T, s *Store) {
					createTestGame(t, s)
					if err := s.RecordRemoteDeleted(ctx, testGameID, syncore.Tombstone{DeletedAt: valid, DeviceName: "Laptop"}); err != nil {
						t.Fatalf("RecordRemoteDeleted: %v", err)
					}
				},
				write: func(s *Store, at time.Time) error {
					return s.ReuploadGame(ctx, testGameID, gameInfo(otherGameID, at))
				},
			},
			testCase{
				op:    "ReplaceGameInfo の " + field,
				setup: createTestGame,
				write: func(s *Store, at time.Time) error {
					_, err := s.ReplaceGameInfo(ctx, gameInfo(testGameID, at))
					return err
				},
			},
		)
	}
	tests = append(tests, []testCase{
		{
			op:    "InsertPullOperation",
			setup: createTestGame,
			write: func(s *Store, at time.Time) error {
				return s.InsertPullOperation(ctx, "0192d4e8-0000-7000-8000-0000000000f1", testGameID, "/save", "/stage", "/old", at)
			},
		},
		{
			op:    "RecordRemoteDeleted",
			setup: createTestGame,
			write: func(s *Store, at time.Time) error {
				return s.RecordRemoteDeleted(ctx, testGameID, syncore.Tombstone{DeletedAt: at, DeviceName: "Laptop"})
			},
		},
		{
			op:    "CreateSession",
			setup: createTestGame,
			write: func(s *Store, at time.Time) error {
				return s.CreateSession(ctx, metasync.Session{ID: sessionA, GameID: testGameID, PlayedAt: at, DurationSec: 1})
			},
		},
		{
			op:    "ImportSessions",
			setup: createTestGame,
			write: func(s *Store, at time.Time) error {
				_, err := s.ImportSessions(ctx, testGameID, []metasync.Session{
					{ID: sessionA, PlayedAt: valid, DurationSec: 1},
					{ID: sessionB, PlayedAt: at, DurationSec: 1},
				})
				return err
			},
		},
		{
			op:    "EditGame",
			setup: createTestGame,
			write: func(s *Store, at time.Time) error {
				return s.EditGame(ctx, testGameID, GameEdit{Title: &title}, at)
			},
		},
		{
			op: "AddLegacySessions",
			write: func(s *Store, at time.Time) error {
				_, err := s.AddLegacySessions(ctx, []legacy.Session{
					legacySession("0123456789abcdef0123456789abcd01", valid),
					legacySession("0123456789abcdef0123456789abcd02", at),
				})
				return err
			},
		},
	}...)
	for _, tt := range tests {
		t.Run("3章/日時の形/年が 4 桁に収まらない日時は書かない（"+tt.op+"）", func(t *testing.T) {
			for _, at := range []time.Time{
				time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC),
				time.Date(-1, 12, 31, 23, 59, 59, 0, time.UTC),
				// 手元の時刻では 4 桁に収まり、UTC に直すと外れる
				time.Date(9999, 12, 31, 23, 0, 0, 0, time.FixedZone("", -5*60*60)),
				time.Date(0, 1, 1, 5, 0, 0, 0, time.FixedZone("", 9*60*60)),
			} {
				t.Run(at.Format(time.RFC3339), func(t *testing.T) {
					s, _ := openTemp(t)
					if tt.setup != nil {
						tt.setup(t, s)
					}
					before := dumpTables(t, s)

					if err := tt.write(s, at); err == nil {
						t.Errorf("%s がエラーを返さなかった", tt.op)
					}
					if diff := cmp.Diff(before, dumpTables(t, s)); diff != "" {
						t.Errorf("DB の行が変わった (-before +after):\n%s", diff)
					}
				})
			}
		})
	}

	t.Run("3章/日時の形/年が 0 と 9999 の日時は書いて読み戻せる", func(t *testing.T) {
		s, _ := openTemp(t)
		createTestGame(t, s)
		want := []metasync.Session{
			{ID: sessionA, GameID: testGameID, PlayedAt: time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC), DurationSec: 1},
			{ID: sessionB, GameID: testGameID, PlayedAt: time.Date(9999, 12, 31, 23, 59, 59, 999_000_000, time.UTC), DurationSec: 1},
		}
		for _, sess := range want {
			if err := s.CreateSession(ctx, sess); err != nil {
				t.Fatalf("CreateSession: %v", err)
			}
		}
		got, err := s.Sessions(ctx, testGameID)
		if err != nil {
			t.Fatalf("Sessions: %v", err)
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("Sessions (-want +got):\n%s", diff)
		}
	})
}
