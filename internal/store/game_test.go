package store

import (
	"context"
	"testing"
	"time"
)

func TestGameLocal(t *testing.T) {
	t.Run("store/GameLocal がセーブフォルダと base を返す", func(t *testing.T) {
		s, _ := openTemp(t)
		ctx := context.Background()
		const gameID = "0192d4e8-0000-7000-8000-000000000001"
		if err := s.CreateGame(ctx, NewGame{
			ID:             gameID,
			Title:          "Game",
			SaveFolderPath: "/save",
			ExePath:        "/game.exe",
			CreatedAt:      time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC),
		}); err != nil {
			t.Fatalf("CreateGame: %v", err)
		}

		saveFolder, base, err := s.GameLocal(ctx, gameID)
		if err != nil {
			t.Fatalf("GameLocal: %v", err)
		}
		if saveFolder != "/save" || base != "" {
			t.Errorf("GameLocal = (%q, %q), want (%q, %q)", saveFolder, base, "/save", "")
		}

		const hash = "4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945"
		if _, err := s.db.ExecContext(ctx,
			`UPDATE game_locals SET base_manifest_hash = ? WHERE game_id = ?`, hash, gameID); err != nil {
			t.Fatalf("set base: %v", err)
		}
		_, base, err = s.GameLocal(ctx, gameID)
		if err != nil {
			t.Fatalf("GameLocal: %v", err)
		}
		if base != hash {
			t.Errorf("base = %q, want %q", base, hash)
		}
	})
}
