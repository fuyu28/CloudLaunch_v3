package metasync_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
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
	// puts は、リモートに PUT したキーを記録する。PUT は並列なので mu で守る
	mu   sync.Mutex
	puts []string
}

func newEnv(t *testing.T) *env {
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

	e := &env{store: s, remote: memstore.New(), dbPath: dbPath}
	e.remote.Before = func(op memstore.Op, key string) error {
		if op == memstore.OpPut {
			e.mu.Lock()
			e.puts = append(e.puts, key)
			e.mu.Unlock()
		}
		return nil
	}
	e.core = metasync.New(metasync.Config{
		Remote: e.remote,
		Local:  s,
		Logger: slog.New(slog.DiscardHandler),
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

func (e *env) putKeys() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	keys := append([]string{}, e.puts...)
	sort.Strings(keys)
	return keys
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
		// remote は、リモートに置くキー。中身はローカルと違うが、この Issue では中身を読まない（8-S4 は #96）
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

	t.Run("8-M4 ゲームの tombstone の記録があれば PUT しない", func(t *testing.T) {
		e := newEnv(t)
		e.addSession(t, sessionA, 60)
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
