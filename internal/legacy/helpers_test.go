package legacy_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/fuyu28/CloudLaunch_v3/internal/metasync"
	"github.com/fuyu28/CloudLaunch_v3/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "cloudlaunch.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func createGame(t *testing.T, s *store.Store, id, title string) {
	t.Helper()
	if err := s.CreateGame(context.Background(), store.NewGame{
		ID: id, Title: title, SaveFolderPath: "/save", ExePath: "/game.exe", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("CreateGame: %v", err)
	}
}

func createSession(t *testing.T, s *store.Store, sess metasync.Session) {
	t.Helper()
	if err := s.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
}
