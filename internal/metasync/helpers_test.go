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
	"sync"
	"testing"
	"time"

	"github.com/fuyu28/CloudLaunch_v3/internal/metasync"
	"github.com/fuyu28/CloudLaunch_v3/internal/objstore/memstore"
	"github.com/fuyu28/CloudLaunch_v3/internal/store"
	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

const (
	testGameID  = "0192d4e8-0000-7000-8000-000000000001"
	localStamp  = "2026-11-03T01:02:03.004Z"
	deletedJSON = `{"deletedAt":"2026-11-03T01:02:03.004Z","deviceName":"Laptop"}`
)

// localGameJSON は、newEnvOn が登録するゲームを PUT したときの本文。
const localGameJSON = `{"id":"` + testGameID + `","title":"Game","createdAt":"` + localStamp + `","updatedAt":"` + localStamp + `"}`

var (
	testPlayedAt = time.Date(2026, 11, 3, 1, 2, 3, 4_000_000, time.UTC)
	errInjected  = errors.New("injected")
	gameKey      = remoteGameKey(testGameID, "game.json")
)

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
	e.putRemote(gameKey, localGameJSON)
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
		Remote:     e.remote,
		Local:      s,
		Tombstones: fakeTombstones{remote: e.remote},
		Logger:     slog.New(slog.DiscardHandler),
		ImageDir:   e.imageDir,
	})
	return e
}

// fakeTombstones は、syncore の代わりにゲームの tombstone を確かめる。リモートの deleted.json の本文が deletedJSON なら
// 削除済み、それ以外の本文なら不正と返す。本文の検証（8-T4〜8-T6）は syncore のテストで確かめる。
type fakeTombstones struct {
	remote interface {
		Get(ctx context.Context, key string) (io.ReadCloser, error)
	}
}

func (f fakeTombstones) RemoteTombstoneExists(ctx context.Context, gameID string) (bool, error) {
	r, err := f.remote.Get(ctx, remoteGameKey(gameID, "deleted.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { _ = r.Close() }()
	body, err := io.ReadAll(r)
	if err != nil {
		return false, err
	}
	if string(body) != deletedJSON {
		return false, fmt.Errorf("%w: %s", metasync.ErrInvalidTombstone, body)
	}
	return true, nil
}

func remoteGameKey(gameID, name string) string {
	return "games/" + gameID + "/" + name
}

func imageKey(sha string) string {
	return remoteGameKey(testGameID, "images/"+sha)
}

// putRemote は、Before を通さずに置くので、puts・gets の記録に数えない。
func (e *env) putRemote(key, body string) {
	e.remote.Set(key, []byte(body))
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

// markRemoteDeleted は、ゲームの tombstone の記録（8-T2）を、リモートの deleted.json（deletedJSON）と違う値で DB にだけ書く。
func (e *env) markRemoteDeleted(t *testing.T) {
	t.Helper()
	ts := syncore.Tombstone{DeletedAt: time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC), DeviceName: "Laptop"}
	if err := e.store.RecordRemoteDeleted(context.Background(), testGameID, ts); err != nil {
		t.Fatalf("RecordRemoteDeleted: %v", err)
	}
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

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("parse %s: %v", s, err)
	}
	return v
}
