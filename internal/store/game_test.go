package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/fuyu28/CloudLaunch_v3/internal/metasync"
	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

const testGameID = "0192d4e8-0000-7000-8000-000000000001"

func createTestGame(t *testing.T, s *Store) {
	t.Helper()
	if err := s.CreateGame(context.Background(), NewGame{
		ID:             testGameID,
		Title:          "Game",
		SaveFolderPath: "/save",
		ExePath:        "/game.exe",
		CreatedAt:      time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("CreateGame: %v", err)
	}
}

func TestGameLocal(t *testing.T) {
	t.Run("store/GameLocal がセーブフォルダと実行ファイルのパスと base を返す", func(t *testing.T) {
		s, _ := openTemp(t)
		createTestGame(t, s)

		got, err := s.GameLocal(context.Background(), testGameID)
		if err != nil {
			t.Fatalf("GameLocal: %v", err)
		}
		want := syncore.GameLocal{SaveFolderPath: "/save", ExePath: "/game.exe"}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("GameLocal (-want +got):\n%s", diff)
		}
	})

	t.Run("store/SetBase が書いた base を GameLocal が返す", func(t *testing.T) {
		s, _ := openTemp(t)
		createTestGame(t, s)
		ctx := context.Background()

		const hash = "4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945"
		if err := s.SetBase(ctx, testGameID, hash); err != nil {
			t.Fatalf("SetBase: %v", err)
		}
		got, err := s.GameLocal(ctx, testGameID)
		if err != nil {
			t.Fatalf("GameLocal: %v", err)
		}
		if got.BaseManifestHash != hash {
			t.Errorf("base = %q, want %q", got.BaseManifestHash, hash)
		}
	})
}

func TestDeleteGame(t *testing.T) {
	const otherGameID = "0192d4e8-0000-7000-8000-000000000002"
	playedAt := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)

	// setup は、セーブフォルダを saveDir にした testGameID と、ほかのゲームを登録し、それぞれにセッションを 1 件ずつ入れる。
	setup := func(t *testing.T, saveDir string) *Store {
		t.Helper()
		s, _ := openTemp(t)
		ctx := context.Background()
		for _, g := range []NewGame{
			{ID: testGameID, Title: "Game", SaveFolderPath: saveDir, ExePath: "/game.exe", CreatedAt: playedAt},
			{ID: otherGameID, Title: "Other", SaveFolderPath: "/other", ExePath: "/other.exe", CreatedAt: playedAt},
		} {
			if err := s.CreateGame(ctx, g); err != nil {
				t.Fatalf("CreateGame: %v", err)
			}
		}
		for i, gameID := range []string{testGameID, otherGameID} {
			if err := s.CreateSession(ctx, metasync.Session{
				ID: fmt.Sprintf("0192d4e8-0000-7000-8000-0000000000c%d", i), GameID: gameID, PlayedAt: playedAt, DurationSec: 60,
			}); err != nil {
				t.Fatalf("CreateSession: %v", err)
			}
		}
		return s
	}
	count := func(t *testing.T, s *Store, query string) int {
		t.Helper()
		var n int
		if err := s.db.QueryRow(query, testGameID).Scan(&n); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return n
	}
	unregisterCases := []struct {
		name    string
		prepare func(t *testing.T, s *Store)
	}{
		{"8-X1 ゲーム情報・ローカル情報・プレイ履歴を消し、ほかのゲームとセーブフォルダは残す", func(*testing.T, *Store) {}},
		{"8-U1 ゲームの tombstone の記録があるゲームも同じく解除し、セーブフォルダは残す", func(t *testing.T, s *Store) {
			if err := s.RecordRemoteDeleted(context.Background(), testGameID, syncore.Tombstone{DeletedAt: playedAt, DeviceName: "Laptop"}); err != nil {
				t.Fatalf("RecordRemoteDeleted: %v", err)
			}
		}},
	}
	for _, tc := range unregisterCases {
		t.Run(tc.name, func(t *testing.T) {
			saveDir := t.TempDir()
			savePath := filepath.Join(saveDir, "slot1.sav")
			if err := os.WriteFile(savePath, []byte("save"), 0o600); err != nil {
				t.Fatalf("write save: %v", err)
			}
			s := setup(t, saveDir)
			ctx := context.Background()
			tc.prepare(t, s)

			if err := s.DeleteGame(ctx, testGameID); err != nil {
				t.Fatalf("DeleteGame: %v", err)
			}
			for _, q := range []string{
				`SELECT count(*) FROM games WHERE id = ?`,
				`SELECT count(*) FROM game_locals WHERE game_id = ?`,
				`SELECT count(*) FROM play_sessions WHERE game_id = ?`,
			} {
				if n := count(t, s, q); n != 0 {
					t.Errorf("%s = %d, want 0", q, n)
				}
			}
			ids, err := s.GameIDs(ctx)
			if err != nil {
				t.Fatalf("GameIDs: %v", err)
			}
			if diff := cmp.Diff([]string{otherGameID}, ids); diff != "" {
				t.Errorf("GameIDs (-want +got):\n%s", diff)
			}
			sessions, err := s.Sessions(ctx, otherGameID)
			if err != nil {
				t.Fatalf("Sessions: %v", err)
			}
			if len(sessions) != 1 {
				t.Errorf("ほかのゲームのセッション = %v, want 1 件", sessions)
			}
			if b, err := os.ReadFile(savePath); err != nil || string(b) != "save" {
				t.Errorf("セーブ = %q, %v, want 変わらずに残る", b, err)
			}
		})
	}

	t.Run("3章/Pull ジャーナル/ジャーナルが残るゲームの登録の解除は ErrPullInProgress で拒否する", func(t *testing.T) {
		s := setup(t, "/save")
		ctx := context.Background()
		if err := s.InsertPullOperation(ctx, "0192d4e8-0000-7000-8000-0000000000d1", testGameID,
			"/save", "/.cloudlaunch-stage-x", "/.cloudlaunch-old-x", playedAt); err != nil {
			t.Fatalf("InsertPullOperation: %v", err)
		}

		if err := s.DeleteGame(ctx, testGameID); !errors.Is(err, ErrPullInProgress) {
			t.Fatalf("DeleteGame error = %v, want ErrPullInProgress", err)
		}
		if n := count(t, s, `SELECT count(*) FROM games WHERE id = ?`); n != 1 {
			t.Errorf("games の行 = %d, want 1", n)
		}
	})

	t.Run("store/登録していないゲームの登録の解除は sql.ErrNoRows を返す", func(t *testing.T) {
		s, _ := openTemp(t)

		if err := s.DeleteGame(context.Background(), testGameID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("DeleteGame error = %v, want sql.ErrNoRows", err)
		}
	})
}

func TestReuploadGame(t *testing.T) {
	const newGameID = "0192d4e8-0000-7000-8000-0000000000b1"
	now := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)

	// snapshot は、上げ直しが書き換える表の全行を、表ごとに文字列にして返す。
	snapshot := func(t *testing.T, s *Store) map[string][]string {
		t.Helper()
		got := map[string][]string{}
		for table, query := range map[string]string{
			"games":         `SELECT id || '|' || title || '|' || coalesce(image_sha256, '') || '|' || updated_at FROM games ORDER BY id`,
			"game_locals":   `SELECT game_id || '|' || coalesce(base_manifest_hash, '') || '|' || coalesce(remote_deleted_at, '') FROM game_locals ORDER BY game_id`,
			"play_sessions": `SELECT id || '|' || game_id FROM play_sessions ORDER BY id`,
		} {
			rows, err := s.db.Query(query)
			if err != nil {
				t.Fatalf("%s: %v", query, err)
			}
			for rows.Next() {
				var row string
				if err := rows.Scan(&row); err != nil {
					t.Fatalf("scan %s: %v", table, err)
				}
				got[table] = append(got[table], row)
			}
			if err := errors.Join(rows.Err(), rows.Close()); err != nil {
				t.Fatalf("%s: %v", query, err)
			}
		}
		return got
	}

	// setup は、プレイ履歴と base を持つゲームを登録する。recordDeleted なら、ゲームの tombstone の記録も書く。
	setup := func(t *testing.T, recordDeleted bool) *Store {
		t.Helper()
		s, _ := openTemp(t)
		ctx := context.Background()
		createTestGame(t, s)
		if err := s.CreateSession(ctx, metasync.Session{
			ID: "0192d4e8-0000-7000-8000-0000000000c1", GameID: testGameID, PlayedAt: now, DurationSec: 60,
		}); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		if err := s.SetBase(ctx, testGameID, "4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945"); err != nil {
			t.Fatalf("SetBase: %v", err)
		}
		if recordDeleted {
			if err := s.RecordRemoteDeleted(ctx, testGameID, syncore.Tombstone{DeletedAt: now, DeviceName: "Laptop"}); err != nil {
				t.Fatalf("RecordRemoteDeleted: %v", err)
			}
		}
		return s
	}
	newGame := metasync.GameInfo{ID: newGameID, Title: "Game", CreatedAt: now, UpdatedAt: now}

	t.Run("8-U4 途中で失敗したら、DB は上げ直す前のままになる", func(t *testing.T) {
		s := setup(t, true)
		// 最後の手順の、古いゲーム情報の削除で失敗させる
		if _, err := s.db.Exec(`CREATE TRIGGER fail_delete_games BEFORE DELETE ON games BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
			t.Fatalf("create trigger: %v", err)
		}
		before := snapshot(t, s)

		if err := s.ReuploadGame(context.Background(), testGameID, newGame); err == nil {
			t.Fatal("ReuploadGame error = nil, want error")
		}
		if diff := cmp.Diff(before, snapshot(t, s)); diff != "" {
			t.Errorf("DB (-before +after):\n%s", diff)
		}
	})

	t.Run("8-U3 Pull ジャーナルが残っていれば、何も変えずに ErrPullInProgress を返す", func(t *testing.T) {
		s := setup(t, true)
		if err := s.InsertPullOperation(context.Background(), "0192d4e8-0000-7000-8000-0000000000d1", testGameID,
			"/save", "/.cloudlaunch-stage-x", "/.cloudlaunch-old-x", now); err != nil {
			t.Fatalf("InsertPullOperation: %v", err)
		}
		before := snapshot(t, s)

		if err := s.ReuploadGame(context.Background(), testGameID, newGame); !errors.Is(err, ErrPullInProgress) {
			t.Fatalf("ReuploadGame error = %v, want ErrPullInProgress", err)
		}
		if diff := cmp.Diff(before, snapshot(t, s)); diff != "" {
			t.Errorf("DB (-before +after):\n%s", diff)
		}
	})

	t.Run("8章/上げ直し/ゲームの tombstone の記録がなければ、何も変えずに ErrNotRemoteDeleted を返す", func(t *testing.T) {
		s := setup(t, false)
		before := snapshot(t, s)

		if err := s.ReuploadGame(context.Background(), testGameID, newGame); !errors.Is(err, syncore.ErrNotRemoteDeleted) {
			t.Fatalf("ReuploadGame error = %v, want ErrNotRemoteDeleted", err)
		}
		if diff := cmp.Diff(before, snapshot(t, s)); diff != "" {
			t.Errorf("DB (-before +after):\n%s", diff)
		}
	})
}
