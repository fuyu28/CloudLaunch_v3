package metasync_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/fuyu28/CloudLaunch_v3/internal/metasync"
	"github.com/fuyu28/CloudLaunch_v3/internal/objstore/memstore"
	"github.com/fuyu28/CloudLaunch_v3/internal/store"
)

const (
	testGameID = "0192d4e8-0000-7000-8000-000000000001"
	sessionA   = "0192d4e8-0000-7000-8000-0000000000c1"
	sessionB   = "0192d4e8-0000-7000-8000-0000000000c2"
	// legacySession は、9 章で取り込む旧版の形の ID
	legacySession = "0123456789abcdef0123456789abcdef"
)

var testPlayedAt = time.Date(2026, 11, 3, 1, 2, 3, 4_000_000, time.UTC)

type env struct {
	core   *metasync.Core
	store  *store.Store
	remote *memstore.Store
	dbPath string
	// imageDir は、画像のキャッシュのフォルダ
	imageDir string
	// puts と gets は、リモートに PUT・GET したキーを、呼んだ順に記録する。並列なので mu で守る
	mu   sync.Mutex
	puts []string
	gets []string
}

// newEnv は、ローカルと同じ game.json をリモートに置く。ゲーム情報は同期しても変わらず（8-G4）、プレイ履歴のテストの
// PUT・GET の記録と結果に、game.json の PUT が混ざらない。
func newEnv(t *testing.T) *env {
	t.Helper()
	e := newBareEnv(t)
	e.putRemote(t, gameKey, localGameJSON)
	return e
}

// newBareEnv は、リモートを空にした PC を 1 台作り、PUT・GET したキーを記録する。
func newBareEnv(t *testing.T) *env {
	t.Helper()
	e := newEnvOn(t, memstore.New())
	e.remote.Before = func(op memstore.Op, key string) error {
		e.mu.Lock()
		defer e.mu.Unlock()
		switch op {
		case memstore.OpPut:
			e.puts = append(e.puts, key)
		case memstore.OpGet:
			e.gets = append(e.gets, key)
		}
		return nil
	}
	return e
}

// newEnvOn は、remote を使う PC を 1 台作る。複数の PC で同じ remote を共有するときに使い、PUT・GET は記録しない。
func newEnvOn(t *testing.T, remote *memstore.Store) *env {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "cloudlaunch.db")
	s, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.CreateGame(ctx, store.NewGame{
		ID:             testGameID,
		Title:          "Game",
		SaveFolderPath: "/save",
		ExePath:        "/game.exe",
		CreatedAt:      testPlayedAt,
	}); err != nil {
		t.Fatalf("CreateGame: %v", err)
	}

	e := &env{store: s, remote: remote, dbPath: dbPath, imageDir: filepath.Join(t.TempDir(), "images")}
	e.core = metasync.New(metasync.Config{
		Remote:   e.remote,
		Local:    s,
		Logger:   slog.New(slog.DiscardHandler),
		ImageDir: e.imageDir,
	})
	return e
}

func (e *env) addSession(t *testing.T, id string, durationSec int64) {
	t.Helper()
	if err := e.store.CreateSession(context.Background(), metasync.Session{
		ID:          id,
		GameID:      testGameID,
		PlayedAt:    testPlayedAt,
		DurationSec: durationSec,
	}); err != nil {
		t.Fatalf("CreateSession %s: %v", id, err)
	}
}

func (e *env) putRemote(t *testing.T, key, body string) {
	t.Helper()
	if err := e.remote.Put(context.Background(), key, strings.NewReader(body), int64(len(body))); err != nil {
		t.Fatalf("put %s: %v", key, err)
	}
	e.mu.Lock()
	e.puts = nil
	e.mu.Unlock()
}

// putRemoteSession は、#95 の PUT と同じ形のセッションをリモートに置く。
func (e *env) putRemoteSession(t *testing.T, id string, durationSec int64) {
	t.Helper()
	e.putRemote(t, sessionKey(id), sessionJSON(id, testGameID, durationSec))
}

func sessionJSON(id, gameID string, durationSec int64) string {
	return fmt.Sprintf(`{"id":%q,"gameId":%q,"playedAt":"2026-11-03T01:02:03.004Z","durationSec":%d}`, id, gameID, durationSec)
}

func session(id string, durationSec int64) metasync.Session {
	return metasync.Session{ID: id, GameID: testGameID, PlayedAt: testPlayedAt, DurationSec: durationSec}
}

func (e *env) putKeys() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	keys := append([]string{}, e.puts...)
	sort.Strings(keys)
	return keys
}

func (e *env) getKeys() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	keys := append([]string{}, e.gets...)
	sort.Strings(keys)
	return keys
}

func (e *env) localSessions(t *testing.T) []metasync.Session {
	t.Helper()
	got, err := e.store.Sessions(context.Background(), testGameID)
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	return got
}

func (e *env) remoteBody(t *testing.T, key string) string {
	t.Helper()
	r, err := e.remote.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}
	defer func() { _ = r.Close() }()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read %s: %v", key, err)
	}
	return string(b)
}

func sessionKey(id string) string {
	return "games/" + testGameID + "/sessions/" + id + ".json"
}

func TestSyncSessions(t *testing.T) {
	ctx := context.Background()

	prefix := "games/" + testGameID + "/sessions/"
	tests := []struct {
		name  string
		local []string
		// rawLocal は、CreateSession が書かない 8-S6 に合わない ID。DB を直接書き換えられた場合として入れる
		rawLocal []string
		// remote は、リモートに置くキー。どれもローカルにもある ID（8-S4）か、セッションとして数えないキーなので、中身は読まれない
		remote   []string
		wantPuts []string
	}{
		{
			name:     "8-S1 ローカルにだけあるセッションを PUT する",
			local:    []string{sessionA, legacySession},
			wantPuts: []string{sessionKey(legacySession), sessionKey(sessionA)},
		},
		{
			name:     "8-S1 リモートにもあるセッションは PUT しない",
			local:    []string{sessionA, sessionB},
			remote:   []string{sessionKey(sessionA)},
			wantPuts: []string{sessionKey(sessionB)},
		},
		{
			name:     "8-S3 sessions/ の一覧で .deleted.json を数えない",
			local:    []string{sessionA},
			remote:   []string{prefix + sessionA + ".deleted.json"},
			wantPuts: []string{sessionKey(sessionA)},
		},
		{
			name:  "8-S6 形に合わない ID のローカルのセッションは PUT しない",
			local: []string{sessionA},
			rawLocal: []string{
				"0192D4E8-0000-7000-8000-0000000000C3", // 大文字
				"0192d4e8-0000-4000-8000-0000000000c4", // UUIDv4
				"0123456789ABCDEF0123456789ABCDEF",     // 大文字の 16 進
				"../" + sessionB,
			},
			wantPuts: []string{sessionKey(sessionA)},
		},
		{
			name:     "8-S6 形に合わない ID のキーはリモートにあるセッションとして数えない",
			local:    []string{sessionA},
			remote:   []string{prefix + "bad.json", prefix + "sub/" + sessionA + ".json", prefix + sessionA},
			wantPuts: []string{sessionKey(sessionA)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			for _, id := range tt.local {
				e.addSession(t, id, 60)
			}
			for _, id := range tt.rawLocal {
				execDB(t, e.dbPath, `INSERT INTO play_sessions (id, game_id, played_at, duration_sec) VALUES (?, ?, ?, ?)`,
					id, testGameID, "2026-11-03T01:02:03.004Z", 60)
			}
			for _, key := range tt.remote {
				e.putRemote(t, key, `{"remote":true}`)
			}

			got, err := e.core.Sync(ctx, testGameID)
			if err != nil {
				t.Fatalf("Sync: %v", err)
			}
			if diff := cmp.Diff(metasync.Result{UploadedSessions: len(tt.wantPuts)}, got); diff != "" {
				t.Errorf("Result (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantPuts, e.putKeys()); diff != "" {
				t.Errorf("PUT したキー (-want +got):\n%s", diff)
			}
			for _, key := range tt.remote {
				if body := e.remoteBody(t, key); body != `{"remote":true}` {
					t.Errorf("リモートの %s が書き換わった: %s", key, body)
				}
			}
		})
	}

	t.Run("8章/プレイ履歴/一覧の後にキャンセルされたら、成功とは返さない", func(t *testing.T) {
		e := newEnv(t)
		e.addSession(t, sessionA, 60)
		cctx, cancel := context.WithCancel(ctx)
		defer cancel()
		e.remote.Before = func(op memstore.Op, key string) error {
			if op == memstore.OpList {
				cancel()
			}
			return nil
		}

		if _, err := e.core.Sync(cctx, testGameID); !errors.Is(err, context.Canceled) {
			t.Errorf("Sync = %v, want %v", err, context.Canceled)
		}
	})

	t.Run("8-M4 ゲームの tombstone の記録があれば、プレイ履歴もゲーム情報も同期しない", func(t *testing.T) {
		e := newBareEnv(t)
		e.addSession(t, sessionA, 60)
		// 同期すれば 8-G3 で DB が書き換わる game.json を置いておく
		e.putRemote(t, gameKey, remoteGameJSON(newerStamp))
		markRemoteDeleted(t, e.dbPath)
		e.remote.Before = func(op memstore.Op, key string) error {
			t.Errorf("リモートを操作した: op %d, key %s", op, key)
			return nil
		}

		got, err := e.core.Sync(ctx, testGameID)
		if err != nil {
			t.Fatalf("Sync: %v", err)
		}
		if diff := cmp.Diff(metasync.Result{}, got); diff != "" {
			t.Errorf("Result (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(localGameInfo, e.localGameInfo(t)); diff != "" {
			t.Errorf("DB のゲーム情報 (-want +got):\n%s", diff)
		}
	})

	t.Run("8章/プレイ履歴/PUT に失敗したら、残りを止めてエラーを返す", func(t *testing.T) {
		e := newEnv(t)
		const sessions = 20
		for i := range sessions {
			e.addSession(t, fmt.Sprintf("0192d4e8-0000-7000-8000-%012x", 0x100+i), 60)
		}
		errInjected := errors.New("injected")
		var mu sync.Mutex
		puts := 0
		e.remote.Before = func(op memstore.Op, key string) error {
			if op != memstore.OpPut {
				return nil
			}
			mu.Lock()
			puts++
			mu.Unlock()
			return errInjected
		}

		_, err := e.core.Sync(ctx, testGameID)
		if !errors.Is(err, errInjected) {
			t.Errorf("Sync = %v, want %v", err, errInjected)
		}
		// 並列数（8）の分は失敗より前に始まりうるが、失敗の後に空いた枠では始めない
		if puts > 8 {
			t.Errorf("PUT を %d 回呼んだ。失敗の後も続けている（全 %d 件）", puts, sessions)
		}
	})

	t.Run("8章/プレイ履歴/一覧に失敗したら、PUT せずにエラーを返す", func(t *testing.T) {
		e := newEnv(t)
		e.addSession(t, sessionA, 60)
		errInjected := errors.New("injected")
		e.remote.Before = func(op memstore.Op, key string) error {
			if op == memstore.OpList {
				return errInjected
			}
			if op == memstore.OpPut {
				t.Errorf("PUT した: %s", key)
			}
			return nil
		}

		_, err := e.core.Sync(ctx, testGameID)
		if !errors.Is(err, errInjected) {
			t.Errorf("Sync = %v, want %v", err, errInjected)
		}
	})

	t.Run("8章/プレイ履歴/PUT する JSON の形", func(t *testing.T) {
		e := newEnv(t)
		e.addSession(t, sessionA, 3600)

		if _, err := e.core.Sync(ctx, testGameID); err != nil {
			t.Fatalf("Sync: %v", err)
		}
		want := `{"id":"` + sessionA + `","gameId":"` + testGameID + `","playedAt":"2026-11-03T01:02:03.004Z","durationSec":3600}`
		if diff := cmp.Diff(want, e.remoteBody(t, sessionKey(sessionA))); diff != "" {
			t.Errorf("本文 (-want +got):\n%s", diff)
		}
	})

	t.Run("8章/プレイ履歴/登録していないゲームはエラーにする", func(t *testing.T) {
		e := newEnv(t)

		if _, err := e.core.Sync(ctx, "0192d4e8-0000-7000-8000-0000000000ff"); err == nil {
			t.Error("Sync がエラーを返さなかった")
		}
	})
}

func TestDownloadSessions(t *testing.T) {
	ctx := context.Background()
	const sessionC = "0192d4e8-0000-7000-8000-0000000000c3"

	t.Run("8-S2 リモートにだけあるセッションを DB に入れる", func(t *testing.T) {
		e := newEnv(t)
		e.putRemoteSession(t, sessionA, 3600)
		e.putRemoteSession(t, legacySession, 0)

		got, err := e.core.Sync(ctx, testGameID)
		if err != nil {
			t.Fatalf("Sync: %v", err)
		}
		if diff := cmp.Diff(metasync.Result{DownloadedSessions: 2}, got); diff != "" {
			t.Errorf("Result (-want +got):\n%s", diff)
		}
		want := []metasync.Session{session(legacySession, 0), session(sessionA, 3600)}
		if diff := cmp.Diff(want, e.localSessions(t)); diff != "" {
			t.Errorf("DB のセッション (-want +got):\n%s", diff)
		}
		if keys := e.putKeys(); len(keys) != 0 {
			t.Errorf("取り込んだセッションを PUT した: %v", keys)
		}
	})

	t.Run("8-S4 両方にある ID のセッションは、中身が違っても GET せず、どちらも書き換えない", func(t *testing.T) {
		e := newEnv(t)
		e.addSession(t, sessionA, 60)
		remote := sessionJSON(sessionA, testGameID, 999)
		e.putRemote(t, sessionKey(sessionA), remote)

		got, err := e.core.Sync(ctx, testGameID)
		if err != nil {
			t.Fatalf("Sync: %v", err)
		}
		if diff := cmp.Diff(metasync.Result{}, got); diff != "" {
			t.Errorf("Result (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]string{gameKey}, e.getKeys()); diff != "" {
			t.Errorf("GET したキー (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]metasync.Session{session(sessionA, 60)}, e.localSessions(t)); diff != "" {
			t.Errorf("DB のセッション (-want +got):\n%s", diff)
		}
		if body := e.remoteBody(t, sessionKey(sessionA)); body != remote {
			t.Errorf("リモートが書き換わった: %s", body)
		}
	})

	t.Run("8-S5 別のゲームに同じ ID があるセッションは取り込まない", func(t *testing.T) {
		e := newEnv(t)
		const otherGameID = "0192d4e8-0000-7000-8000-000000000002"
		if err := e.store.CreateGame(ctx, store.NewGame{
			ID: otherGameID, Title: "Other", SaveFolderPath: "/other", ExePath: "/other.exe", CreatedAt: testPlayedAt,
		}); err != nil {
			t.Fatalf("CreateGame: %v", err)
		}
		other := metasync.Session{ID: sessionA, GameID: otherGameID, PlayedAt: testPlayedAt, DurationSec: 60}
		if err := e.store.CreateSession(ctx, other); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		e.putRemoteSession(t, sessionA, 999)
		e.putRemoteSession(t, sessionB, 0)

		got, err := e.core.Sync(ctx, testGameID)
		if err != nil {
			t.Fatalf("Sync: %v", err)
		}
		if diff := cmp.Diff(metasync.Result{DownloadedSessions: 1}, got); diff != "" {
			t.Errorf("Result (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]metasync.Session{session(sessionB, 0)}, e.localSessions(t)); diff != "" {
			t.Errorf("DB のセッション (-want +got):\n%s", diff)
		}
		otherSessions, err := e.store.Sessions(ctx, otherGameID)
		if err != nil {
			t.Fatalf("Sessions: %v", err)
		}
		if diff := cmp.Diff([]metasync.Session{other}, otherSessions); diff != "" {
			t.Errorf("別のゲームのセッション (-want +got):\n%s", diff)
		}
	})

	// 不正なセッションを sessionA のキーに置き、正しいセッション sessionB と並べる。不正な 1 件だけを飛ばし、残りは取り込む
	invalid := []struct {
		name string
		body string
	}{
		{"8-V1 JSON として読めないセッションは取り込まない", `{"id":`},
		{"8-V1 64 KiB を超えるセッションは取り込まない", sessionJSON(sessionA, testGameID, 60) + strings.Repeat(" ", 64<<10)},
		{"8-V2 項目名の大文字小文字が違うセッションは取り込まない", fmt.Sprintf(`{"ID":%q,"GameID":%q,"PlayedAt":"2026-11-03T01:02:03.004Z","DurationSec":60}`, sessionA, testGameID)},
		{"8-V2 項目が欠けたセッションは取り込まない", fmt.Sprintf(`{"id":%q,"gameId":%q,"playedAt":"2026-11-03T01:02:03.004Z"}`, sessionA, testGameID)},
		{"8-V2 項目が null のセッションは取り込まない", fmt.Sprintf(`{"id":%q,"gameId":%q,"playedAt":null,"durationSec":60}`, sessionA, testGameID)},
		{"8-V3 id がキーの ID と違うセッションは取り込まない", sessionJSON(sessionC, testGameID, 60)},
		{"8-V4 gameId がキーのゲームと違うセッションは取り込まない", sessionJSON(sessionA, "0192d4e8-0000-7000-8000-000000000002", 60)},
		{"8-V5 playedAt が 3 章の日時の形でないセッションは取り込まない（時差つき）", fmt.Sprintf(`{"id":%q,"gameId":%q,"playedAt":"2026-11-03T10:02:03.004+09:00","durationSec":60}`, sessionA, testGameID)},
		{"8-V5 playedAt が 3 章の日時の形でないセッションは取り込まない（ミリ秒なし）", fmt.Sprintf(`{"id":%q,"gameId":%q,"playedAt":"2026-11-03T01:02:03Z","durationSec":60}`, sessionA, testGameID)},
		{"8-V6 durationSec が負のセッションは取り込まない", sessionJSON(sessionA, testGameID, -1)},
		{"8-V6 durationSec が小数のセッションは取り込まない", fmt.Sprintf(`{"id":%q,"gameId":%q,"playedAt":"2026-11-03T01:02:03.004Z","durationSec":1.5}`, sessionA, testGameID)},
		{"8-V6 durationSec が指数表記のセッションは取り込まない", fmt.Sprintf(`{"id":%q,"gameId":%q,"playedAt":"2026-11-03T01:02:03.004Z","durationSec":1e3}`, sessionA, testGameID)},
		{"8-V6 durationSec が文字列のセッションは取り込まない", fmt.Sprintf(`{"id":%q,"gameId":%q,"playedAt":"2026-11-03T01:02:03.004Z","durationSec":"60"}`, sessionA, testGameID)},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			e.putRemote(t, sessionKey(sessionA), tt.body)
			e.putRemoteSession(t, sessionB, 0)

			got, err := e.core.Sync(ctx, testGameID)
			if err != nil {
				t.Fatalf("Sync: %v", err)
			}
			if diff := cmp.Diff(metasync.Result{DownloadedSessions: 1}, got); diff != "" {
				t.Errorf("Result (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff([]metasync.Session{session(sessionB, 0)}, e.localSessions(t)); diff != "" {
				t.Errorf("DB のセッション (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("8章/プレイ履歴/知らない項目があっても取り込む", func(t *testing.T) {
		e := newEnv(t)
		e.putRemote(t, sessionKey(sessionA),
			fmt.Sprintf(`{"id":%q,"gameId":%q,"playedAt":"2026-11-03T01:02:03.004Z","durationSec":60,"note":"x"}`, sessionA, testGameID))

		if _, err := e.core.Sync(ctx, testGameID); err != nil {
			t.Fatalf("Sync: %v", err)
		}
		if diff := cmp.Diff([]metasync.Session{session(sessionA, 60)}, e.localSessions(t)); diff != "" {
			t.Errorf("DB のセッション (-want +got):\n%s", diff)
		}
	})

	t.Run("8章/プレイ履歴/2 台の PC で同期すると、両方の DB が和集合になる", func(t *testing.T) {
		remote := memstore.New()
		pcA, pcB := newEnvOn(t, remote), newEnvOn(t, remote)
		pcA.addSession(t, sessionA, 60)
		pcB.addSession(t, sessionB, 120)

		for _, pc := range []*env{pcA, pcB, pcA} {
			if _, err := pc.core.Sync(ctx, testGameID); err != nil {
				t.Fatalf("Sync: %v", err)
			}
		}

		want := []metasync.Session{session(sessionA, 60), session(sessionB, 120)}
		for name, pc := range map[string]*env{"A": pcA, "B": pcB} {
			if diff := cmp.Diff(want, pc.localSessions(t)); diff != "" {
				t.Errorf("PC %s の DB のセッション (-want +got):\n%s", name, diff)
			}
		}
	})

	t.Run("8章/プレイ履歴/一覧の後に消えたセッションは飛ばす", func(t *testing.T) {
		e := newEnv(t)
		e.putRemoteSession(t, sessionA, 60)
		e.putRemoteSession(t, sessionB, 0)
		// 一覧の後に、別の PC がゲームを削除して sessionA が消えたものとする
		e.remote.Before = func(op memstore.Op, key string) error {
			if op == memstore.OpGet && key == sessionKey(sessionA) {
				return fmt.Errorf("get %q: %w", key, fs.ErrNotExist)
			}
			return nil
		}

		got, err := e.core.Sync(ctx, testGameID)
		if err != nil {
			t.Fatalf("Sync: %v", err)
		}
		if diff := cmp.Diff(metasync.Result{DownloadedSessions: 1}, got); diff != "" {
			t.Errorf("Result (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]metasync.Session{session(sessionB, 0)}, e.localSessions(t)); diff != "" {
			t.Errorf("DB のセッション (-want +got):\n%s", diff)
		}
	})

	t.Run("8章/プレイ履歴/GET に失敗したら、何も DB に入れずにエラーを返す", func(t *testing.T) {
		e := newEnv(t)
		e.putRemoteSession(t, sessionA, 60)
		e.putRemoteSession(t, sessionB, 0)
		e.putRemoteSession(t, sessionC, 0)
		errInjected := errors.New("injected")
		e.remote.Before = func(op memstore.Op, key string) error {
			if op == memstore.OpGet && key == sessionKey(sessionB) {
				return errInjected
			}
			return nil
		}

		if _, err := e.core.Sync(ctx, testGameID); !errors.Is(err, errInjected) {
			t.Errorf("Sync = %v, want %v", err, errInjected)
		}
		if got := e.localSessions(t); len(got) != 0 {
			t.Errorf("DB に入れた: %v", got)
		}
	})
}

// markRemoteDeleted は、ゲームの tombstone の記録（8-T2）を DB に直接書く。記録を書く操作は #100 で作る。
func markRemoteDeleted(t *testing.T, dbPath string) {
	t.Helper()
	execDB(t, dbPath, `UPDATE game_locals SET remote_deleted_at = ?, remote_deleted_by = ? WHERE game_id = ?`,
		"2026-11-03T00:00:00.000Z", "Laptop", testGameID)
}

func execDB(t *testing.T, dbPath, query string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}
