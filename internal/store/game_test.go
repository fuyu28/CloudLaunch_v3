package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

	// setup は、testGameID とほかのゲームを登録し、それぞれにセッションを 1 件ずつ入れる。
	setup := func(t *testing.T) *Store {
		t.Helper()
		s, _ := openTemp(t)
		ctx := context.Background()
		createTestGame(t, s)
		if err := s.CreateGame(ctx, NewGame{
			ID: otherGameID, Title: "Other", SaveFolderPath: "/other", ExePath: "/other.exe", CreatedAt: playedAt,
		}); err != nil {
			t.Fatalf("CreateGame: %v", err)
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

	t.Run("8-X1 ゲーム情報・ローカル情報・プレイ履歴を消し、ほかのゲームは残す", func(t *testing.T) {
		s := setup(t)
		ctx := context.Background()

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
	})

	t.Run("3章/Pull ジャーナル/ジャーナルが残るゲームの登録の解除は ErrPullInProgress で拒否する", func(t *testing.T) {
		s := setup(t)
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
