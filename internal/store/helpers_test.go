package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

const testGameID = "0192d4e8-0000-7000-8000-000000000001"

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
