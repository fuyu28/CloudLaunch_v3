package store

import (
	"context"
	"testing"
	"time"
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
	t.Run("store/GameLocal がセーブフォルダと base を返す", func(t *testing.T) {
		s, _ := openTemp(t)
		createTestGame(t, s)

		saveFolder, base, err := s.GameLocal(context.Background(), testGameID)
		if err != nil {
			t.Fatalf("GameLocal: %v", err)
		}
		if saveFolder != "/save" || base != "" {
			t.Errorf("GameLocal = (%q, %q), want (%q, %q)", saveFolder, base, "/save", "")
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
		_, base, err := s.GameLocal(ctx, testGameID)
		if err != nil {
			t.Fatalf("GameLocal: %v", err)
		}
		if base != hash {
			t.Errorf("base = %q, want %q", base, hash)
		}
	})
}
