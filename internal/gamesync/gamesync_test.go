package gamesync_test

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fuyu28/CloudLaunch_v3/internal/gamesync"
	"github.com/fuyu28/CloudLaunch_v3/internal/metasync"
	"github.com/fuyu28/CloudLaunch_v3/internal/objstore/memstore"
	"github.com/fuyu28/CloudLaunch_v3/internal/store"
	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

const (
	testGameID    = "0192d4e8-0000-7000-8000-000000000001"
	tombstoneJSON = `{"deletedAt":"2026-11-03T00:00:00.000Z","deviceName":"Laptop"}`
)

var (
	testCreatedAt = time.Date(2026, 11, 3, 1, 2, 3, 4_000_000, time.UTC)
	errInjected   = errors.New("injected")
	gameJSONKey   = gameKey("game.json")
	tombstoneKey  = gameKey("deleted.json")
	headKey       = gameKey("HEAD")
)

func gameKey(name string) string {
	return "games/" + testGameID + "/" + name
}

func isObjectKey(key string) bool {
	return strings.HasPrefix(key, gameKey("objects/"))
}

// env は、本物の syncore・metasync・store と、偽のリモートで組み立てた 1 台の PC。
type env struct {
	core    *gamesync.Core
	store   *store.Store
	remote  *memstore.Store
	saveDir string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	return newEnvOn(t, memstore.New())
}

// newEnvOn は、remote を使う PC を 1 台作る。複数の PC で同じ remote を共有するときに使う。
func newEnvOn(t *testing.T, remote *memstore.Store) *env {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	s, err := store.Open(ctx, filepath.Join(dir, "cloudlaunch.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	saveDir := filepath.Join(dir, "save")
	if err := os.Mkdir(saveDir, 0o700); err != nil {
		t.Fatalf("mkdir save: %v", err)
	}
	// Push・Pull は、実行ファイルがなければ止まる
	exePath := filepath.Join(dir, "game.exe")
	if err := os.WriteFile(exePath, nil, 0o600); err != nil {
		t.Fatalf("write exe: %v", err)
	}
	if err := s.CreateGame(ctx, store.NewGame{
		ID:             testGameID,
		Title:          "Game",
		SaveFolderPath: saveDir,
		ExePath:        exePath,
		CreatedAt:      testCreatedAt,
	}); err != nil {
		t.Fatalf("CreateGame: %v", err)
	}

	logger := slog.New(slog.DiscardHandler)
	save := syncore.New(syncore.Config{
		Remote:     remote,
		Local:      s,
		Logger:     logger,
		DeviceName: "Desktop",
		BackupsDir: filepath.Join(dir, "backups"),
		Locker:     &fakeLocker{},
		Processes:  notRunning{},
		Rename:     renameNoReplace,
	})
	meta := metasync.New(metasync.Config{Remote: remote, Local: s, Logger: logger, ImageDir: filepath.Join(dir, "images")})
	return &env{
		core:    gamesync.New(gamesync.Config{Save: save, Meta: meta, Logger: logger}),
		store:   s,
		remote:  remote,
		saveDir: saveDir,
	}
}

type fakeLocker struct {
	mu   sync.Mutex
	held bool
}

func (l *fakeLocker) TryLock() (func() error, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held {
		return nil, false, nil
	}
	l.held = true
	return func() error {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.held = false
		return nil
	}, true, nil
}

type notRunning struct{}

func (notRunning) Running(string) (bool, error) { return false, nil }

// renameNoReplace は、移す先があれば失敗する（仕様書 3 章「ディスクへの書き出し」）。テストではディスクへの書き出しは確かめない。
func renameNoReplace(oldpath, newpath string) error {
	if _, err := os.Lstat(newpath); err == nil {
		return fs.ErrExist
	}
	return os.Rename(oldpath, newpath)
}

func (e *env) writeSave(t *testing.T, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(e.saveDir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("write save: %v", err)
	}
}

// check は、Check を呼び、判定が want であることを確かめる。
func (e *env) check(t *testing.T, want syncore.Decision) syncore.Plan {
	t.Helper()
	plan, err := e.core.Check(t.Context(), testGameID)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if plan.RemoteDeleted != nil || plan.Decision != want {
		t.Fatalf("plan = %+v, want 判定 %v", plan, want)
	}
	return plan
}

// failOn は、op で key に当たる操作を errInjected で失敗させる。
func (e *env) failOn(op memstore.Op, match func(key string) bool) {
	prev := e.remote.Before
	e.remote.Before = func(o memstore.Op, key string) error {
		if o == op && match(key) {
			return errInjected
		}
		if prev != nil {
			return prev(o, key)
		}
		return nil
	}
}

func is(want string) func(string) bool {
	return func(key string) bool { return key == want }
}

func (e *env) hasRemote(t *testing.T, key string) bool {
	t.Helper()
	keys, err := e.remote.List(context.Background(), key)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, k := range keys {
		if k == key {
			return true
		}
	}
	return false
}

// assertMetaSynced は、メタ情報の同期が成功し、ローカルにしかない game.json を PUT したことを確かめる。
func (e *env) assertMetaSynced(t *testing.T, res gamesync.Result) {
	t.Helper()
	if res.MetaErr != nil {
		t.Errorf("MetaErr = %v", res.MetaErr)
	}
	if res.Meta == nil || res.Meta.GameInfo != metasync.GameInfoUploaded {
		t.Errorf("Meta = %+v, want game.json をアップロードした", res.Meta)
	}
	if !e.hasRemote(t, gameJSONKey) {
		t.Error("game.json を PUT していない")
	}
}

// assertMetaNotSynced は、メタ情報を同期しなかったことを確かめる。
func (e *env) assertMetaNotSynced(t *testing.T, res gamesync.Result) {
	t.Helper()
	if res.Meta != nil || res.MetaErr != nil {
		t.Errorf("Meta = %+v, MetaErr = %v, want 同期しない", res.Meta, res.MetaErr)
	}
	if e.hasRemote(t, gameJSONKey) {
		t.Error("game.json を PUT した")
	}
}

// assertRecordedDeleted は、ゲームの tombstone を DB に記録し、配下の deleted.json 以外を削除したこと（8-T2）を確かめる。
func (e *env) assertRecordedDeleted(t *testing.T) {
	t.Helper()
	if _, ok, err := e.store.RemoteDeleted(context.Background(), testGameID); err != nil || !ok {
		t.Errorf("RemoteDeleted = %v, %v, want 記録あり", ok, err)
	}
	keys, err := e.remote.List(context.Background(), gameKey(""))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, k := range keys {
		if k != tombstoneKey {
			t.Errorf("配下が残っている: %s", k)
		}
	}
}

func TestGameSync(t *testing.T) {
	t.Run("8-M1 ゲームの tombstone があれば、セーブもメタ情報も同期しない", func(t *testing.T) {
		e := newEnv(t)
		e.writeSave(t, "a.sav", "one")
		e.remote.Set(tombstoneKey, []byte(tombstoneJSON))
		e.remote.Before = func(op memstore.Op, key string) error {
			if op == memstore.OpPut {
				t.Errorf("PUT した: %s", key)
			}
			return nil
		}

		plan, err := e.core.Check(t.Context(), testGameID)
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if plan.RemoteDeleted == nil || plan.RemoteDeleted.DeviceName != "Laptop" {
			t.Errorf("RemoteDeleted = %+v, want Laptop の tombstone", plan.RemoteDeleted)
		}
		e.assertRecordedDeleted(t)
	})

	t.Run("8-M1 確認の後、判定の前にゲームの tombstone が置かれても、記録して判定しない", func(t *testing.T) {
		e := newEnv(t)
		e.writeSave(t, "a.sav", "one")
		// 1 回目の GET（8-M1 の確認）の後、2 回目（Status の確認）の前に置く
		var mu sync.Mutex
		gets := 0
		e.remote.Before = func(op memstore.Op, key string) error {
			if op == memstore.OpGet && key == tombstoneKey {
				mu.Lock()
				gets++
				if gets == 2 {
					e.remote.Set(tombstoneKey, []byte(tombstoneJSON))
				}
				mu.Unlock()
			}
			return nil
		}

		plan, err := e.core.Check(t.Context(), testGameID)
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if plan.RemoteDeleted == nil {
			t.Errorf("plan = %+v, want RemoteDeleted", plan)
		}
		e.assertRecordedDeleted(t)
	})

	t.Run("8-M2 Push を見送っても、メタ情報を同期する", func(t *testing.T) {
		e := newEnv(t)
		e.writeSave(t, "a.sav", "one")
		plan := e.check(t, syncore.DecisionPush)

		res, err := e.core.Skip(t.Context(), plan)
		if err != nil {
			t.Fatalf("Skip: %v", err)
		}
		e.assertMetaSynced(t, res)
		if e.hasRemote(t, headKey) {
			t.Error("HEAD を PUT した")
		}
	})

	t.Run("8-M2 Push が失敗しても、メタ情報を同期する", func(t *testing.T) {
		e := newEnv(t)
		e.writeSave(t, "a.sav", "one")
		plan := e.check(t, syncore.DecisionPush)
		e.failOn(memstore.OpPut, isObjectKey)

		res, err := e.core.Push(t.Context(), plan, syncore.PushOptions{})
		if !errors.Is(err, errInjected) {
			t.Fatalf("Push error = %v, want injected", err)
		}
		if res.RemoteDeleted != nil {
			t.Errorf("RemoteDeleted = %+v, want nil", res.RemoteDeleted)
		}
		e.assertMetaSynced(t, res)
	})

	t.Run("8-M2 判定に失敗しても、メタ情報を同期する", func(t *testing.T) {
		e := newEnv(t)
		e.writeSave(t, "a.sav", "one")
		e.failOn(memstore.OpGet, is(headKey))

		_, checkErr := e.core.Check(t.Context(), testGameID)
		if !errors.Is(checkErr, errInjected) {
			t.Fatalf("Check error = %v, want injected", checkErr)
		}
		res, err := e.core.SyncMeta(t.Context(), testGameID, checkErr)
		if !errors.Is(err, errInjected) {
			t.Errorf("SyncMeta error = %v, want injected", err)
		}
		e.assertMetaSynced(t, res)
	})

	t.Run("8-M3 メタ情報の同期が失敗しても、セーブの同期の結果を返す", func(t *testing.T) {
		e := newEnv(t)
		e.writeSave(t, "a.sav", "one")
		plan := e.check(t, syncore.DecisionPush)
		e.failOn(memstore.OpPut, is(gameJSONKey))

		res, err := e.core.Push(t.Context(), plan, syncore.PushOptions{})
		if err != nil {
			t.Fatalf("Push: %v", err)
		}
		if !e.hasRemote(t, headKey) {
			t.Error("HEAD を PUT していない")
		}
		if res.Meta != nil || !errors.Is(res.MetaErr, errInjected) {
			t.Errorf("Meta = %+v, MetaErr = %v, want injected", res.Meta, res.MetaErr)
		}
	})

	t.Run("8-T7 承認の後にゲームが削除されたら、メタ情報を同期しない", func(t *testing.T) {
		e := newEnv(t)
		e.writeSave(t, "a.sav", "one")
		plan := e.check(t, syncore.DecisionPush)
		e.remote.Set(tombstoneKey, []byte(tombstoneJSON))

		res, err := e.core.Push(t.Context(), plan, syncore.PushOptions{})
		if !errors.Is(err, syncore.ErrRemoteDeleted) {
			t.Fatalf("Push error = %v, want ErrRemoteDeleted", err)
		}
		if res.RemoteDeleted == nil || res.RemoteDeleted.DeviceName != "Laptop" {
			t.Errorf("RemoteDeleted = %+v, want Laptop の tombstone", res.RemoteDeleted)
		}
		e.assertMetaNotSynced(t, res)
		e.assertRecordedDeleted(t)
	})

	t.Run("8章/セーブとメタ情報の同期/セーブの同期の失敗の後に tombstone が見つかったら、メタ情報を同期しない", func(t *testing.T) {
		remote := memstore.New()
		pcA, pcB := newEnvOn(t, remote), newEnvOn(t, remote)
		pcA.writeSave(t, "a.sav", "one")
		if _, err := pcA.core.Push(t.Context(), pcA.check(t, syncore.DecisionPush), syncore.PushOptions{}); err != nil {
			t.Fatalf("PC A の Push: %v", err)
		}
		// PC A の game.json を消し、PC B が PUT したかを見分けられるようにする
		if err := remote.DeleteMany(context.Background(), []string{gameJSONKey}); err != nil {
			t.Fatalf("DeleteMany: %v", err)
		}
		plan := pcB.check(t, syncore.DecisionPull)
		// 承認の後に、ほかの PC がゲームを削除する。object が先に消えるので、Pull は tombstone を読み直す（8-T7）前に、
		// object を取得できずに失敗する
		objects, err := remote.List(context.Background(), gameKey("objects/"))
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if err := remote.DeleteMany(context.Background(), objects); err != nil {
			t.Fatalf("DeleteMany: %v", err)
		}
		remote.Set(tombstoneKey, []byte(tombstoneJSON))

		res, err := pcB.core.Pull(t.Context(), plan, syncore.PullOptions{})
		if err == nil || errors.Is(err, syncore.ErrRemoteDeleted) {
			t.Fatalf("Pull error = %v, want ErrRemoteDeleted でない失敗", err)
		}
		if res.RemoteDeleted == nil {
			t.Error("RemoteDeleted = nil, want tombstone")
		}
		pcB.assertMetaNotSynced(t, res)
		pcB.assertRecordedDeleted(t)
	})

	t.Run("8章/セーブとメタ情報の同期/tombstone を確かめ直せなかったら、メタ情報を同期しない", func(t *testing.T) {
		e := newEnv(t)
		e.writeSave(t, "a.sav", "one")
		plan := e.check(t, syncore.DecisionPush)
		e.failOn(memstore.OpPut, isObjectKey)
		e.failOn(memstore.OpGet, is(tombstoneKey))

		res, err := e.core.Push(t.Context(), plan, syncore.PushOptions{})
		if !errors.Is(err, errInjected) {
			t.Fatalf("Push error = %v, want injected", err)
		}
		if res.RemoteDeleted != nil {
			t.Errorf("RemoteDeleted = %+v, want nil", res.RemoteDeleted)
		}
		e.assertMetaNotSynced(t, res)
	})
}
