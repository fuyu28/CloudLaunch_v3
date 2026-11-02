package syncore_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/fuyu28/CloudLaunch_v3/internal/objstore/memstore"
	"github.com/fuyu28/CloudLaunch_v3/internal/store"
	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

const (
	testGameID     = "0192d4e8-0000-7000-8000-000000000001"
	testGeneration = "0192d4e8-0000-7000-8000-0000000000aa"
	// pushGeneration は、テストのコアが Push で振る世代
	pushGeneration = "0192d4e8-0000-7000-8000-0000000000bb"
	testDeviceName = "Desktop"
)

var testNow = time.Date(2026, 11, 3, 1, 2, 3, 4_000_000, time.UTC)

type env struct {
	core    *syncore.Core
	store   *store.Store
	remote  *memstore.Store
	saveDir string
}

// newEnv は、セーブフォルダを持つゲームを 1 つ登録した DB と、空のリモートを用意する。
func newEnv(t *testing.T) env {
	t.Helper()
	return newEnvWithRemote(t, memstore.New())
}

// newEnvWithRemote は、別の PC のように、DB とセーブフォルダを分けたまま remote を共有させる。
func newEnvWithRemote(t *testing.T, remote *memstore.Store) env {
	t.Helper()
	dir := t.TempDir()
	saveDir := filepath.Join(dir, "save")
	if err := os.Mkdir(saveDir, 0o700); err != nil {
		t.Fatalf("mkdir save: %v", err)
	}

	st, err := store.Open(context.Background(), filepath.Join(dir, "cloudlaunch.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.CreateGame(context.Background(), store.NewGame{
		ID:             testGameID,
		Title:          "Game",
		SaveFolderPath: saveDir,
		ExePath:        filepath.Join(dir, "game.exe"),
		CreatedAt:      time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("CreateGame: %v", err)
	}

	core := syncore.New(syncore.Config{
		Remote:        remote,
		Local:         st,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		DeviceName:    testDeviceName,
		Now:           func() time.Time { return testNow },
		NewGeneration: func() (string, error) { return pushGeneration, nil },
	})
	return env{core: core, store: st, remote: remote, saveDir: saveDir}
}

func (e env) status(t *testing.T) (syncore.Plan, error) {
	t.Helper()
	return e.core.Status(t.Context(), testGameID)
}

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
}

func sha256Hex(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// entriesOf は、files をセーブフォルダに置いたときに期待する entries を、path の昇順で返す。
func entriesOf(files map[string]string) []syncore.Entry {
	entries := []syncore.Entry{}
	for rel, content := range files {
		entries = append(entries, syncore.Entry{Path: rel, SHA256: sha256Hex(content), Size: int64(len(content))})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries
}

func headKey() string                      { return "games/" + testGameID + "/HEAD" }
func manifestKey(generation string) string { return "games/" + testGameID + "/manifests/" + generation + ".json" }
func objectKey(content string) string     { return "games/" + testGameID + "/objects/" + sha256Hex(content) }

func (e env) putObject(t *testing.T, key, body string) {
	t.Helper()
	if err := e.remote.Put(context.Background(), key, strings.NewReader(body), int64(len(body))); err != nil {
		t.Fatalf("put %s: %v", key, err)
	}
}

// getObject は key がなければ ok に false を返す。
func (e env) getObject(t *testing.T, key string) (body string, ok bool) {
	t.Helper()
	r, err := e.remote.Get(context.Background(), key)
	if err != nil {
		return "", false
	}
	defer func() { _ = r.Close() }()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read %s: %v", key, err)
	}
	return string(b), true
}

func (e env) keys(t *testing.T, prefix string) []string {
	t.Helper()
	keys, err := e.remote.List(context.Background(), prefix)
	if err != nil {
		t.Fatalf("list %s: %v", prefix, err)
	}
	return keys
}

// putManifest は、entries を持つ世代の manifest だけを置き、HEAD は変えない。
func (e env) putManifest(t *testing.T, generation string, entries []syncore.Entry) {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"version":    1,
		"createdAt":  "2026-10-27T00:00:00.000Z",
		"deviceName": "Other",
		"entries":    entries,
	})
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	e.putObject(t, manifestKey(generation), string(b))
}

// putGeneration は、entries を持つ世代を置き、HEAD をその世代に向ける。
func (e env) putGeneration(t *testing.T, entries []syncore.Entry) {
	t.Helper()
	e.putManifest(t, testGeneration, entries)
	e.putObject(t, headKey(), testGeneration)
}

func (e env) setBase(t *testing.T, hash string) {
	t.Helper()
	if err := e.store.SetBase(context.Background(), testGameID, hash); err != nil {
		t.Fatalf("SetBase: %v", err)
	}
}

func (e env) base(t *testing.T) string {
	t.Helper()
	_, base, err := e.store.GameLocal(context.Background(), testGameID)
	if err != nil {
		t.Fatalf("GameLocal: %v", err)
	}
	return base
}
