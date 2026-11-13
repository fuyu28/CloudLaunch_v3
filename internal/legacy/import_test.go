package legacy_test

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/fuyu28/CloudLaunch_v3/internal/legacy"
	"github.com/fuyu28/CloudLaunch_v3/internal/metasync"
	"github.com/fuyu28/CloudLaunch_v3/internal/store"
)

const (
	legacyGameA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1"
	legacyGameB = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa2"
	sessionA    = "0123456789abcdef0123456789abcd01"
	sessionB    = "0123456789abcdef0123456789abcd02"
	sessionC    = "0123456789abcdef0123456789abcd03"
	v3GameID    = "0192d4e8-0000-7000-8000-000000000001"
)

// gamesJSON は、旧版のエクスポートと同じく、使わない項目も持つゲーム 2 つ。タイトルは同じで、旧版の gameId で区別する。
const gamesJSON = `[
  {"id": "` + legacyGameA + `", "title": "ゲーム", "publisher": "P", "exePath": "C:\\a.exe", "createdAt": "2025-01-01T00:00:00+09:00", "updatedAt": "2025-01-01T00:00:00+09:00", "playStatus": "playing"},
  {"id": "` + legacyGameB + `", "title": "ゲーム", "publisher": "P", "exePath": "C:\\b.exe", "createdAt": "2025-01-01T00:00:00+09:00", "updatedAt": "2025-01-01T00:00:00+09:00", "playStatus": "played"}
]`

func sessionJSON(id, gameID, playedAt string, duration int64) string {
	return `{"id": "` + id + `", "gameId": "` + gameID + `", "playedAt": "` + playedAt + `", "duration": ` +
		strconv.FormatInt(duration, 10) + `, "updatedAt": "2025-01-01T00:00:00+09:00"}`
}

func exportJSON(games string, sessions ...string) string {
	return `{"exportedAt": "2026-11-01T00:00:00+09:00", "games": ` + games + `, "statistics": [], "sessions": [` +
		strings.Join(sessions, ", ") + `]}`
}

// validExport は、時差付き・秒未満 7 桁の playedAt（旧版の DB にある形）と、Z・秒未満なしの playedAt を持つ。
var validExport = exportJSON(gamesJSON,
	sessionJSON(sessionA, legacyGameA, "2026-10-14T02:13:23.1852465+09:00", 61),
	sessionJSON(sessionB, legacyGameB, "2026-10-05T00:00:00Z", 0),
)

var validSessions = []legacy.Session{
	{ID: sessionB, GameID: legacyGameB, GameTitle: "ゲーム", PlayedAt: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC), DurationSec: 0},
	{ID: sessionA, GameID: legacyGameA, GameTitle: "ゲーム", PlayedAt: time.Date(2026, 10, 13, 17, 13, 23, 185_000_000, time.UTC), DurationSec: 61},
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "cloudlaunch.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func importString(t *testing.T, s *store.Store, body string) (legacy.Result, error) {
	t.Helper()
	return legacy.Import(context.Background(), strings.NewReader(body), s)
}

func legacySessions(t *testing.T, s *store.Store) []legacy.Session {
	t.Helper()
	got, err := s.LegacySessions(context.Background())
	if err != nil {
		t.Fatalf("LegacySessions: %v", err)
	}
	return got
}

func TestImport(t *testing.T) {
	t.Run("9-L1 エクスポートのプレイ履歴を未紐付けの旧履歴として持つ", func(t *testing.T) {
		s := openStore(t)

		res, err := importString(t, s, validExport)
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
		if diff := cmp.Diff(legacy.Result{Added: 2}, res); diff != "" {
			t.Errorf("Result (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(validSessions, legacySessions(t, s)); diff != "" {
			t.Errorf("LegacySessions (-want +got):\n%s", diff)
		}
	})

	t.Run("9-L2 games にない gameId のプレイ履歴は持たず、件数を返す", func(t *testing.T) {
		s := openStore(t)
		body := exportJSON(gamesJSON,
			sessionJSON(sessionA, legacyGameA, "2026-10-14T02:13:23.1852465+09:00", 61),
			sessionJSON(sessionB, legacyGameB, "2026-10-05T00:00:00Z", 0),
			sessionJSON(sessionC, "ffffffffffffffffffffffffffffffff", "2026-10-06T00:00:00Z", 10),
		)

		res, err := importString(t, s, body)
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
		if diff := cmp.Diff(legacy.Result{Added: 2, Orphaned: 1}, res); diff != "" {
			t.Errorf("Result (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(validSessions, legacySessions(t, s)); diff != "" {
			t.Errorf("LegacySessions (-want +got):\n%s", diff)
		}
	})

	t.Run("9-L3 同じファイルを 2 回読み込んでも結果は変わらない", func(t *testing.T) {
		s := openStore(t)
		if _, err := importString(t, s, validExport); err != nil {
			t.Fatalf("Import 1 回目: %v", err)
		}

		res, err := importString(t, s, validExport)
		if err != nil {
			t.Fatalf("Import 2 回目: %v", err)
		}
		if diff := cmp.Diff(legacy.Result{Existing: 2}, res); diff != "" {
			t.Errorf("Result (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(validSessions, legacySessions(t, s)); diff != "" {
			t.Errorf("LegacySessions (-want +got):\n%s", diff)
		}
	})

	t.Run("9-L3 通常のプレイ履歴に同じ ID があるものは持たない", func(t *testing.T) {
		s := openStore(t)
		ctx := context.Background()
		if err := s.CreateGame(ctx, store.NewGame{
			ID: v3GameID, Title: "ゲーム", SaveFolderPath: "/save", ExePath: "/game.exe", CreatedAt: time.Now(),
		}); err != nil {
			t.Fatalf("CreateGame: %v", err)
		}
		if err := s.CreateSession(ctx, metasync.Session{ID: sessionA, GameID: v3GameID, PlayedAt: time.Now(), DurationSec: 61}); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}

		res, err := importString(t, s, validExport)
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
		if diff := cmp.Diff(legacy.Result{Added: 1, Existing: 1}, res); diff != "" {
			t.Errorf("Result (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(validSessions[:1], legacySessions(t, s)); diff != "" {
			t.Errorf("LegacySessions (-want +got):\n%s", diff)
		}
	})

	t.Run("9章/読み込み/games が null でもエラーにしない", func(t *testing.T) {
		// sessions の null も、games と同じく空の配列として扱う
		for _, body := range []string{`{"games": null, "sessions": []}`, `{"games": [], "sessions": null}`} {
			s := openStore(t)

			res, err := importString(t, s, body)
			if err != nil {
				t.Fatalf("Import(%s): %v", body, err)
			}
			if diff := cmp.Diff(legacy.Result{}, res); diff != "" {
				t.Errorf("Import(%s) Result (-want +got):\n%s", body, diff)
			}
		}
	})

	t.Run("9章/読み込み/知らない項目は無視する", func(t *testing.T) {
		s := openStore(t)
		body := `{"games": [{"id": "` + legacyGameA + `", "title": "ゲーム", "extra": {"a": [1]}}], "sessions": [` +
			`{"id": "` + sessionA + `", "gameId": "` + legacyGameA + `", "playedAt": "2026-10-14T02:13:23.1852465+09:00", "duration": 61, "routeId": null}` +
			`], "unknown": 1}`

		res, err := importString(t, s, body)
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
		if diff := cmp.Diff(legacy.Result{Added: 1}, res); diff != "" {
			t.Errorf("Result (-want +got):\n%s", diff)
		}
	})
}

func TestImportInvalid(t *testing.T) {
	// valid は、不正な行より前に置く正しいプレイ履歴。これも書かれないことを確かめる
	valid := sessionJSON(sessionA, legacyGameA, "2026-10-14T02:13:23.1852465+09:00", 61)
	gameA := `{"id": "` + legacyGameA + `", "title": "ゲーム"}`
	games := func(extra string) string { return `[` + gameA + `, ` + extra + `]` }
	sess := func(fields string) string { return exportJSON(`[`+gameA+`]`, valid, `{`+fields+`}`) }
	const idB = `"id": "` + sessionB + `"`
	const gameIDA = `"gameId": "` + legacyGameA + `"`
	const playedAt = `"playedAt": "2026-10-05T00:00:00Z"`

	tests := []struct {
		name string
		body string
	}{
		{"JSON として読めない", `{"games": [`},
		{"最上位がオブジェクトでない", `[]`},
		{"games がない", `{"sessions": [` + valid + `]}`},
		{"sessions がない", `{"games": [` + gameA + `]}`},
		{"games が配列でない", exportJSON(`{}`, valid)},
		{"sessions の要素がオブジェクトでない", exportJSON(`[`+gameA+`]`, valid, `1`)},
		{"games の要素がオブジェクトでない", exportJSON(games(`"x"`), valid)},
		{"ゲームの id がない", exportJSON(games(`{"title": "x"}`), valid)},
		{"ゲームの id が数", exportJSON(games(`{"id": 1, "title": "x"}`), valid)},
		{"ゲームの id が空文字", exportJSON(games(`{"id": "", "title": "x"}`), valid)},
		{"ゲームの title がない", exportJSON(games(`{"id": "`+legacyGameB+`"}`), valid)},
		{"ゲームの title が null", exportJSON(games(`{"id": "`+legacyGameB+`", "title": null}`), valid)},
		{"ゲームの title が数", exportJSON(games(`{"id": "`+legacyGameB+`", "title": 1}`), valid)},
		{"ゲームの title が空文字", exportJSON(games(`{"id": "`+legacyGameB+`", "title": ""}`), valid)},
		{"ゲームの id が重複", exportJSON(games(`{"id": "`+legacyGameA+`", "title": "x"}`), valid)},
		{"プレイ履歴の id がない", sess(gameIDA + `, ` + playedAt + `, "duration": 1`)},
		{"プレイ履歴の id が null", sess(`"id": null, ` + gameIDA + `, ` + playedAt + `, "duration": 1`)},
		{"プレイ履歴の id が数", sess(`"id": 1, ` + gameIDA + `, ` + playedAt + `, "duration": 1`)},
		{"プレイ履歴の id が大文字の 16 進", sess(`"id": "0123456789ABCDEF0123456789ABCD02", ` + gameIDA + `, ` + playedAt + `, "duration": 1`)},
		{"プレイ履歴の id が UUIDv7", sess(`"id": "` + v3GameID + `", ` + gameIDA + `, ` + playedAt + `, "duration": 1`)},
		{"プレイ履歴の id が重複", exportJSON(`[`+gameA+`]`, valid, valid)},
		{"gameId がない", sess(idB + `, ` + playedAt + `, "duration": 1`)},
		{"gameId が null", sess(idB + `, "gameId": null, ` + playedAt + `, "duration": 1`)},
		{"gameId が数", sess(idB + `, "gameId": 1, ` + playedAt + `, "duration": 1`)},
		{"playedAt がない", sess(idB + `, ` + gameIDA + `, "duration": 1`)},
		{"playedAt が null", sess(idB + `, ` + gameIDA + `, "playedAt": null, "duration": 1`)},
		{"playedAt が数", sess(idB + `, ` + gameIDA + `, "playedAt": 1, "duration": 1`)},
		{"playedAt が RFC3339 でない", sess(idB + `, ` + gameIDA + `, "playedAt": "2026-10-05 00:00:00 +0900 JST", "duration": 1`)},
		{"playedAt を UTC に直すと年が 4 桁に収まらない", sess(idB + `, ` + gameIDA + `, "playedAt": "0000-01-01T00:00:00+09:00", "duration": 1`)},
		{"duration がない", sess(idB + `, ` + gameIDA + `, ` + playedAt)},
		{"duration が null", sess(idB + `, ` + gameIDA + `, ` + playedAt + `, "duration": null`)},
		{"duration が負", sess(idB + `, ` + gameIDA + `, ` + playedAt + `, "duration": -1`)},
		{"duration が小数", sess(idB + `, ` + gameIDA + `, ` + playedAt + `, "duration": 1.5`)},
		{"duration が文字列", sess(idB + `, ` + gameIDA + `, ` + playedAt + `, "duration": "1"`)},
		{"項目名の大文字小文字が違う", sess(`"ID": "` + sessionB + `", ` + gameIDA + `, ` + playedAt + `, "duration": 1`)},
	}
	for _, tt := range tests {
		t.Run("9章/読み込み/形の違う JSON はエラーにし、何も書かない/"+tt.name, func(t *testing.T) {
			s := openStore(t)

			if _, err := importString(t, s, tt.body); err == nil {
				t.Errorf("Import がエラーを返さなかった")
			}
			if got := legacySessions(t, s); len(got) != 0 {
				t.Errorf("LegacySessions = %v, want 空", got)
			}
		})
	}
}
