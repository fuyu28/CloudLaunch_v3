package store

import (
	"context"
	"testing"
	"time"
)

func countPullOperations(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM pull_operations`).Scan(&n); err != nil {
		t.Fatalf("count pull_operations: %v", err)
	}
	return n
}

func TestPullOperation(t *testing.T) {
	createdAt := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)

	t.Run("store/InsertPullOperation が書いた行を DeletePullOperation が消す", func(t *testing.T) {
		s, _ := openTemp(t)
		createTestGame(t, s)
		ctx := context.Background()

		if err := s.InsertPullOperation(ctx, "op1", testGameID, "/save", "/.cloudlaunch-stage-op1", "/.cloudlaunch-old-op1", createdAt); err != nil {
			t.Fatalf("InsertPullOperation: %v", err)
		}
		if n := countPullOperations(t, s); n != 1 {
			t.Fatalf("行の数 = %d, want 1", n)
		}
		if err := s.DeletePullOperation(ctx, "op1"); err != nil {
			t.Fatalf("DeletePullOperation: %v", err)
		}
		if n := countPullOperations(t, s); n != 0 {
			t.Errorf("行の数 = %d, want 0", n)
		}
	})

	t.Run("store/同じゲームのジャーナルが残っていれば InsertPullOperation は失敗する", func(t *testing.T) {
		s, _ := openTemp(t)
		createTestGame(t, s)
		ctx := context.Background()

		if err := s.InsertPullOperation(ctx, "op1", testGameID, "/save", "/stage1", "/old1", createdAt); err != nil {
			t.Fatalf("InsertPullOperation: %v", err)
		}
		if err := s.InsertPullOperation(ctx, "op2", testGameID, "/save", "/stage2", "/old2", createdAt); err == nil {
			t.Error("2 つ目の InsertPullOperation が成功した")
		}
	})
}
