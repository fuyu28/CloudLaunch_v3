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

func TestImportSessions(t *testing.T) {
	ctx := context.Background()
	const (
		otherGameID = "0192d4e8-0000-7000-8000-000000000002"
		idA         = "0192d4e8-0000-7000-8000-0000000000c1"
		idB         = "0192d4e8-0000-7000-8000-0000000000c2"
	)
	playedAt := time.Date(2026, 11, 3, 1, 2, 3, 4_000_000, time.UTC)
	session := func(id, gameID string, durationSec int64) metasync.Session {
		return metasync.Session{ID: id, GameID: gameID, PlayedAt: playedAt, DurationSec: durationSec}
	}
	createOtherGame := func(t *testing.T, s *Store) {
		t.Helper()
		if err := s.CreateGame(ctx, NewGame{
			ID: otherGameID, Title: "Other", SaveFolderPath: "/other", ExePath: "/other.exe", CreatedAt: playedAt,
		}); err != nil {
			t.Fatalf("CreateGame: %v", err)
		}
	}
	sessionsOf := func(t *testing.T, s *Store, gameID string) []metasync.Session {
		t.Helper()
		got, err := s.Sessions(ctx, gameID)
		if err != nil {
			t.Fatalf("Sessions: %v", err)
		}
		return got
	}

	t.Run("8-S2 store/ImportSessions はセッションを入れ、入れた件数を返す", func(t *testing.T) {
		s, _ := openTemp(t)
		createTestGame(t, s)

		got, err := s.ImportSessions(ctx, testGameID, []metasync.Session{session(idA, testGameID, 60), session(idB, testGameID, 0)})
		if err != nil {
			t.Fatalf("ImportSessions: %v", err)
		}
		if diff := cmp.Diff(metasync.ImportResult{Inserted: 2}, got); diff != "" {
			t.Errorf("ImportResult (-want +got):\n%s", diff)
		}
		want := []metasync.Session{session(idA, testGameID, 60), session(idB, testGameID, 0)}
		if diff := cmp.Diff(want, sessionsOf(t, s, testGameID)); diff != "" {
			t.Errorf("Sessions (-want +got):\n%s", diff)
		}
	})

	t.Run("store/ImportSessions は同じゲームに同じ ID があれば、書き換えずに飛ばす", func(t *testing.T) {
		s, _ := openTemp(t)
		createTestGame(t, s)
		if err := s.CreateSession(ctx, session(idA, testGameID, 60)); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}

		got, err := s.ImportSessions(ctx, testGameID, []metasync.Session{session(idA, testGameID, 999), session(idB, testGameID, 0)})
		if err != nil {
			t.Fatalf("ImportSessions: %v", err)
		}
		if diff := cmp.Diff(metasync.ImportResult{Inserted: 1}, got); diff != "" {
			t.Errorf("ImportResult (-want +got):\n%s", diff)
		}
		want := []metasync.Session{session(idA, testGameID, 60), session(idB, testGameID, 0)}
		if diff := cmp.Diff(want, sessionsOf(t, s, testGameID)); diff != "" {
			t.Errorf("Sessions (-want +got):\n%s", diff)
		}
	})

	t.Run("8-S5 store/ImportSessions は別のゲームに同じ ID があれば、入れずに返す", func(t *testing.T) {
		s, _ := openTemp(t)
		createTestGame(t, s)
		createOtherGame(t, s)
		if err := s.CreateSession(ctx, session(idA, otherGameID, 60)); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}

		got, err := s.ImportSessions(ctx, testGameID, []metasync.Session{session(idA, testGameID, 999), session(idB, testGameID, 0)})
		if err != nil {
			t.Fatalf("ImportSessions: %v", err)
		}
		if diff := cmp.Diff(metasync.ImportResult{Inserted: 1, OtherGame: []string{idA}}, got); diff != "" {
			t.Errorf("ImportResult (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]metasync.Session{session(idB, testGameID, 0)}, sessionsOf(t, s, testGameID)); diff != "" {
			t.Errorf("Sessions of game (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]metasync.Session{session(idA, otherGameID, 60)}, sessionsOf(t, s, otherGameID)); diff != "" {
			t.Errorf("Sessions of other game (-want +got):\n%s", diff)
		}
	})

	t.Run("8-S6 store/ImportSessions は形に合わない ID があれば、何も入れずにエラーを返す", func(t *testing.T) {
		s, _ := openTemp(t)
		createTestGame(t, s)

		if _, err := s.ImportSessions(ctx, testGameID, []metasync.Session{session(idA, testGameID, 60), session("../x", testGameID, 0)}); err == nil {
			t.Error("ImportSessions がエラーを返さなかった")
		}
		if got := sessionsOf(t, s, testGameID); len(got) != 0 {
			t.Errorf("Sessions = %v, want 空", got)
		}
	})
}
