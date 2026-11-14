package legacy_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/fuyu28/CloudLaunch_v3/internal/legacy"
	"github.com/fuyu28/CloudLaunch_v3/internal/metasync"
	"github.com/fuyu28/CloudLaunch_v3/internal/store"
)

const (
	legacyGameC = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa3"
	sessionD    = "0123456789abcdef0123456789abcd04"
	otherGameID = "0192d4e8-0000-7000-8000-000000000002"
	// unknownGameID は、どのテストでも登録しないゲーム ID
	unknownGameID = "0192d4e8-0000-7000-8000-0000000000ff"
)

// gamesOf は、id と title の組から、旧版のエクスポートの games を作る。
func gamesOf(pairs ...[2]string) string {
	var elems []string
	for _, p := range pairs {
		elems = append(elems, `{"id": "`+p[0]+`", "title": "`+p[1]+`"}`)
	}
	return "[" + strings.Join(elems, ", ") + "]"
}

func mustImport(t *testing.T, s *store.Store, body string) {
	t.Helper()
	if _, err := importString(t, s, body); err != nil {
		t.Fatalf("Import: %v", err)
	}
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

func sessionsOf(t *testing.T, s *store.Store, gameID string) []metasync.Session {
	t.Helper()
	got, err := s.Sessions(context.Background(), gameID)
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	return got
}

func utc(day, sec int) time.Time {
	return time.Date(2026, 10, day, 0, 0, sec, 0, time.UTC)
}

func TestMatch(t *testing.T) {
	ctx := context.Background()

	t.Run("9-K4 前後の空白を除いた完全一致で照合する", func(t *testing.T) {
		tests := []struct {
			name        string
			legacyTitle string
			title       string
			want        bool
		}{
			{name: "同じタイトルは一致する", legacyTitle: "ゲーム", title: "ゲーム", want: true},
			{name: "v3 のゲーム名の前後の半角・全角の空白は除く", legacyTitle: "ゲーム", title: " \tゲーム　", want: true},
			{name: "旧ゲームのタイトルの前後の空白は除く", legacyTitle: "  ゲーム ", title: "ゲーム", want: true},
			{name: "大文字小文字が違えば一致しない", legacyTitle: "Game", title: "game", want: false},
			{name: "部分一致は一致しない", legacyTitle: "ゲーム", title: "ゲーム2", want: false},
			{name: "間の空白は除かない", legacyTitle: "ゲ ーム", title: "ゲーム", want: false},
			{name: "空白だけのタイトル同士は一致しない", legacyTitle: "   ", title: " ", want: false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				s := openStore(t)
				mustImport(t, s, exportJSON(gamesOf([2]string{legacyGameA, tt.legacyTitle}),
					sessionJSON(sessionA, legacyGameA, "2026-10-01T00:00:00Z", 60)))

				got, err := legacy.Match(ctx, s, tt.title)
				if err != nil {
					t.Fatalf("Match: %v", err)
				}
				if (got.Suggested != nil) != tt.want {
					t.Errorf("Suggested = %+v, want 一致 %v", got.Suggested, tt.want)
				}
			})
		}
	})

	t.Run("9-K1 1 件だけ一致すれば提案する", func(t *testing.T) {
		s := openStore(t)
		mustImport(t, s, exportJSON(gamesOf([2]string{legacyGameA, "ゲーム"}, [2]string{legacyGameB, "別のゲーム"}),
			sessionJSON(sessionA, legacyGameA, "2026-10-01T00:00:00Z", 60),
			sessionJSON(sessionB, legacyGameA, "2026-10-03T00:00:00Z", 30),
			sessionJSON(sessionC, legacyGameB, "2026-10-02T00:00:00Z", 10),
		))

		got, err := legacy.Match(ctx, s, "ゲーム")
		if err != nil {
			t.Fatalf("Match: %v", err)
		}
		matched := legacy.Game{ID: legacyGameA, Title: "ゲーム", Sessions: 2, TotalSec: 90, LastPlayedAt: utc(3, 0)}
		want := legacy.MatchResult{
			Suggested: &matched,
			// 提案を断ったときに選び直せるよう、提案したものを含めて全件を返す
			Candidates: []legacy.Game{
				matched,
				{ID: legacyGameB, Title: "別のゲーム", Sessions: 1, TotalSec: 10, LastPlayedAt: utc(2, 0)},
			},
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("Match (-want +got):\n%s", diff)
		}
	})

	t.Run("9-K2 一致がなければ候補一覧を返す", func(t *testing.T) {
		s := openStore(t)
		mustImport(t, s, exportJSON(gamesOf([2]string{legacyGameA, "いろは"}, [2]string{legacyGameB, "あいう"}),
			sessionJSON(sessionA, legacyGameA, "2026-10-01T00:00:00Z", 60),
			sessionJSON(sessionB, legacyGameB, "2026-10-02T00:00:00Z", 10),
		))

		got, err := legacy.Match(ctx, s, "ゲーム")
		if err != nil {
			t.Fatalf("Match: %v", err)
		}
		want := legacy.MatchResult{Candidates: []legacy.Game{
			{ID: legacyGameB, Title: "あいう", Sessions: 1, TotalSec: 10, LastPlayedAt: utc(2, 0)},
			{ID: legacyGameA, Title: "いろは", Sessions: 1, TotalSec: 60, LastPlayedAt: utc(1, 0)},
		}}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("Match (-want +got):\n%s", diff)
		}
	})

	t.Run("9-K2 同名の旧ゲームが複数あれば提案せずに候補一覧を返す", func(t *testing.T) {
		s := openStore(t)
		// gamesJSON は、同じタイトル「ゲーム」の旧ゲームを 2 つ持つ
		mustImport(t, s, exportJSON(gamesJSON,
			sessionJSON(sessionA, legacyGameB, "2026-10-01T00:00:00Z", 60),
			sessionJSON(sessionB, legacyGameA, "2026-10-02T00:00:00Z", 10),
		))

		got, err := legacy.Match(ctx, s, "ゲーム")
		if err != nil {
			t.Fatalf("Match: %v", err)
		}
		want := legacy.MatchResult{Candidates: []legacy.Game{
			{ID: legacyGameA, Title: "ゲーム", Sessions: 1, TotalSec: 10, LastPlayedAt: utc(2, 0)},
			{ID: legacyGameB, Title: "ゲーム", Sessions: 1, TotalSec: 60, LastPlayedAt: utc(1, 0)},
		}}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("Match (-want +got):\n%s", diff)
		}
	})

	t.Run("9章/照合/未紐付けの旧履歴がなければ何も返さない", func(t *testing.T) {
		s := openStore(t)

		got, err := legacy.Match(ctx, s, "ゲーム")
		if err != nil {
			t.Fatalf("Match: %v", err)
		}
		if diff := cmp.Diff(legacy.MatchResult{}, got); diff != "" {
			t.Errorf("Match (-want +got):\n%s", diff)
		}
	})

	t.Run("9章/照合/同じ旧版の gameId でタイトルが違えば、最も新しい行のタイトルを使う", func(t *testing.T) {
		s := openStore(t)
		// 旧版でタイトルを変えた後に、新しいプレイ履歴を持つエクスポートを読み込んだ状態
		mustImport(t, s, exportJSON(gamesOf([2]string{legacyGameA, "旧タイトル"}),
			sessionJSON(sessionA, legacyGameA, "2026-10-01T00:00:00Z", 60)))
		mustImport(t, s, exportJSON(gamesOf([2]string{legacyGameA, "新タイトル"}),
			sessionJSON(sessionA, legacyGameA, "2026-10-01T00:00:00Z", 60),
			sessionJSON(sessionB, legacyGameA, "2026-10-02T00:00:00Z", 10)))

		got, err := legacy.Match(ctx, s, "新タイトル")
		if err != nil {
			t.Fatalf("Match: %v", err)
		}
		matched := legacy.Game{ID: legacyGameA, Title: "新タイトル", Sessions: 2, TotalSec: 70, LastPlayedAt: utc(2, 0)}
		want := legacy.MatchResult{Suggested: &matched, Candidates: []legacy.Game{matched}}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("Match (-want +got):\n%s", diff)
		}
	})
}

func TestLink(t *testing.T) {
	ctx := context.Background()

	t.Run("9-K3 選んだ旧ゲームの旧履歴を通常のプレイ履歴にし、旧履歴から消す", func(t *testing.T) {
		s := openStore(t)
		createGame(t, s, v3GameID, "ゲーム")
		mustImport(t, s, exportJSON(gamesOf([2]string{legacyGameA, "ゲーム"}, [2]string{legacyGameB, "別のゲーム"}),
			sessionJSON(sessionA, legacyGameA, "2026-10-01T00:00:00Z", 60),
			sessionJSON(sessionB, legacyGameA, "2026-10-02T00:00:00Z", 30),
			sessionJSON(sessionC, legacyGameB, "2026-10-03T00:00:00Z", 10),
		))

		res, err := legacy.Link(ctx, s, legacyGameA, v3GameID)
		if err != nil {
			t.Fatalf("Link: %v", err)
		}
		if diff := cmp.Diff(legacy.LinkResult{Linked: 2}, res); diff != "" {
			t.Errorf("LinkResult (-want +got):\n%s", diff)
		}
		wantSessions := []metasync.Session{
			{ID: sessionA, GameID: v3GameID, PlayedAt: utc(1, 0), DurationSec: 60},
			{ID: sessionB, GameID: v3GameID, PlayedAt: utc(2, 0), DurationSec: 30},
		}
		if diff := cmp.Diff(wantSessions, sessionsOf(t, s, v3GameID)); diff != "" {
			t.Errorf("Sessions (-want +got):\n%s", diff)
		}
		// ほかの旧ゲームの旧履歴は残る
		wantLegacy := []legacy.Session{
			{ID: sessionC, GameID: legacyGameB, GameTitle: "別のゲーム", PlayedAt: utc(3, 0), DurationSec: 10},
		}
		if diff := cmp.Diff(wantLegacy, legacySessions(t, s)); diff != "" {
			t.Errorf("LegacySessions (-want +got):\n%s", diff)
		}
	})

	t.Run("9-K3 通常のプレイ履歴に同じ ID があるもの（別のゲームのものを含む）は取り込まずに旧履歴から消す", func(t *testing.T) {
		s := openStore(t)
		createGame(t, s, v3GameID, "ゲーム")
		createGame(t, s, otherGameID, "別のゲーム")
		mustImport(t, s, exportJSON(gamesOf([2]string{legacyGameA, "ゲーム"}),
			sessionJSON(sessionA, legacyGameA, "2026-10-01T00:00:00Z", 60),
			sessionJSON(sessionB, legacyGameA, "2026-10-02T00:00:00Z", 30),
			sessionJSON(sessionD, legacyGameA, "2026-10-04T00:00:00Z", 5),
		))
		// 読み込んだ後に、別の PC が同じエクスポートを取り込んで同期した状態。中身はそのまま残ることを確かめるため、
		// 旧履歴と違う値にする
		inOther := metasync.Session{ID: sessionA, GameID: otherGameID, PlayedAt: utc(1, 1), DurationSec: 61}
		inTarget := metasync.Session{ID: sessionB, GameID: v3GameID, PlayedAt: utc(2, 1), DurationSec: 31}
		createSession(t, s, inOther)
		createSession(t, s, inTarget)

		res, err := legacy.Link(ctx, s, legacyGameA, v3GameID)
		if err != nil {
			t.Fatalf("Link: %v", err)
		}
		if diff := cmp.Diff(legacy.LinkResult{Linked: 1, Dropped: 2}, res); diff != "" {
			t.Errorf("LinkResult (-want +got):\n%s", diff)
		}
		wantTarget := []metasync.Session{inTarget, {ID: sessionD, GameID: v3GameID, PlayedAt: utc(4, 0), DurationSec: 5}}
		if diff := cmp.Diff(wantTarget, sessionsOf(t, s, v3GameID)); diff != "" {
			t.Errorf("Sessions(%s) (-want +got):\n%s", v3GameID, diff)
		}
		if diff := cmp.Diff([]metasync.Session{inOther}, sessionsOf(t, s, otherGameID)); diff != "" {
			t.Errorf("Sessions(%s) (-want +got):\n%s", otherGameID, diff)
		}
		if got := legacySessions(t, s); len(got) != 0 {
			t.Errorf("LegacySessions = %+v, want 空", got)
		}
	})

	t.Run("9-K3 途中で失敗したら DB は取り込む前のまま", func(t *testing.T) {
		s := openStore(t)
		mustImport(t, s, validExport)

		// 登録していない gameId に取り込もうとすると、通常のプレイ履歴への追加が外部キーに当たって失敗する
		if _, err := legacy.Link(ctx, s, legacyGameA, unknownGameID); err == nil {
			t.Fatal("Link がエラーを返さなかった")
		}
		if diff := cmp.Diff(validSessions, legacySessions(t, s)); diff != "" {
			t.Errorf("LegacySessions (-want +got):\n%s", diff)
		}
	})

	t.Run("9章/取り込み/登録していないゲームはエラーにする", func(t *testing.T) {
		s := openStore(t)

		// 旧履歴がない旧ゲームでも、取り込み先が登録されていなければエラーにする
		if _, err := legacy.Link(ctx, s, legacyGameC, unknownGameID); err == nil {
			t.Fatal("Link がエラーを返さなかった")
		}
	})

	t.Run("9章/取り込み/旧履歴がない旧ゲームは 0 件を返す", func(t *testing.T) {
		s := openStore(t)
		createGame(t, s, v3GameID, "ゲーム")
		mustImport(t, s, validExport)

		res, err := legacy.Link(ctx, s, legacyGameC, v3GameID)
		if err != nil {
			t.Fatalf("Link: %v", err)
		}
		if diff := cmp.Diff(legacy.LinkResult{}, res); diff != "" {
			t.Errorf("LinkResult (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(validSessions, legacySessions(t, s)); diff != "" {
			t.Errorf("LegacySessions (-want +got):\n%s", diff)
		}
	})
}
