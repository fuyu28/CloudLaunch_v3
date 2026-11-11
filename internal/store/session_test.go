package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/fuyu28/CloudLaunch_v3/internal/metasync"
)

func TestSessions(t *testing.T) {
	t.Run("store/CreateSession で書いたセッションを Sessions が UTC のミリ秒で返す", func(t *testing.T) {
		s, _ := openTemp(t)
		createTestGame(t, s)
		ctx := context.Background()

		jst := time.FixedZone("JST", 9*60*60)
		sessions := []metasync.Session{
			{ID: "0192d4e8-0000-7000-8000-0000000000c2", GameID: testGameID, PlayedAt: time.Date(2026, 11, 3, 10, 0, 0, 1_234_567, jst), DurationSec: 0},
			{ID: "0192d4e8-0000-7000-8000-0000000000c1", GameID: testGameID, PlayedAt: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), DurationSec: 3600},
		}
		for _, sess := range sessions {
			if err := s.CreateSession(ctx, sess); err != nil {
				t.Fatalf("CreateSession: %v", err)
			}
		}

		got, err := s.Sessions(ctx, testGameID)
		if err != nil {
			t.Fatalf("Sessions: %v", err)
		}
		want := []metasync.Session{
			{ID: "0192d4e8-0000-7000-8000-0000000000c1", GameID: testGameID, PlayedAt: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), DurationSec: 3600},
			{ID: "0192d4e8-0000-7000-8000-0000000000c2", GameID: testGameID, PlayedAt: time.Date(2026, 11, 3, 1, 0, 0, 1_000_000, time.UTC), DurationSec: 0},
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("Sessions (-want +got):\n%s", diff)
		}
	})

	t.Run("8-S6 store/CreateSession は形に合わない ID のセッションを書かない", func(t *testing.T) {
		s, _ := openTemp(t)
		createTestGame(t, s)
		ctx := context.Background()

		for _, id := range []string{"", "0192D4E8-0000-7000-8000-0000000000C1", "0192d4e8-0000-4000-8000-0000000000c1", "../x"} {
			if err := s.CreateSession(ctx, metasync.Session{ID: id, GameID: testGameID, PlayedAt: time.Now()}); err == nil {
				t.Errorf("CreateSession(%q) がエラーを返さなかった", id)
			}
		}
		got, err := s.Sessions(ctx, testGameID)
		if err != nil {
			t.Fatalf("Sessions: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("Sessions = %v, want 空", got)
		}
	})

	t.Run("store/CreateSession は UTC の年が 4 桁に収まらない日時を書かない", func(t *testing.T) {
		s, _ := openTemp(t)
		createTestGame(t, s)

		played := time.Date(9999, 12, 31, 23, 0, 0, 0, time.FixedZone("", -5*60*60))
		err := s.CreateSession(context.Background(), metasync.Session{
			ID: "0192d4e8-0000-7000-8000-0000000000c1", GameID: testGameID, PlayedAt: played,
		})
		if err == nil {
			t.Error("CreateSession がエラーを返さなかった")
		}
	})

	t.Run("store/CreateSession は登録していないゲームのセッションを書かない", func(t *testing.T) {
		s, _ := openTemp(t)

		err := s.CreateSession(context.Background(), metasync.Session{
			ID: "0192d4e8-0000-7000-8000-0000000000c1", GameID: testGameID, PlayedAt: time.Now(),
		})
		if err == nil {
			t.Error("CreateSession がエラーを返さなかった")
		}
	})
}

func TestRemoteDeleted(t *testing.T) {
	ctx := context.Background()

	t.Run("store/RemoteDeleted はゲームの tombstone の記録がなければ false を返す", func(t *testing.T) {
		s, _ := openTemp(t)
		createTestGame(t, s)

		got, err := s.RemoteDeleted(ctx, testGameID)
		if err != nil {
			t.Fatalf("RemoteDeleted: %v", err)
		}
		if got {
			t.Error("RemoteDeleted = true, want false")
		}
	})

	t.Run("store/RemoteDeleted はゲームの tombstone の記録があれば true を返す", func(t *testing.T) {
		s, _ := openTemp(t)
		createTestGame(t, s)
		if _, err := s.db.ExecContext(ctx,
			`UPDATE game_locals SET remote_deleted_at = ?, remote_deleted_by = ? WHERE game_id = ?`,
			"2026-11-03T00:00:00.000Z", "Laptop", testGameID); err != nil {
			t.Fatalf("update game_locals: %v", err)
		}

		got, err := s.RemoteDeleted(ctx, testGameID)
		if err != nil {
			t.Fatalf("RemoteDeleted: %v", err)
		}
		if !got {
			t.Error("RemoteDeleted = false, want true")
		}
	})

	t.Run("store/RemoteDeleted は登録していないゲームでエラーを返す", func(t *testing.T) {
		s, _ := openTemp(t)

		if _, err := s.RemoteDeleted(ctx, testGameID); err == nil {
			t.Error("RemoteDeleted がエラーを返さなかった")
		}
	})
}
