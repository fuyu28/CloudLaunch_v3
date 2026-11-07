package syncore_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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
	core *syncore.Core
	// cfg は core を作った設定。coreWithout が 1 項目だけ変えたコアを作るために持つ
	cfg        syncore.Config
	store      *store.Store
	remote     *memstore.Store
	saveDir    string
	dbPath     string
	backupsDir string
	// rename は、テストがリネームに失敗を仕込むためのもの。fn が nil でなければ、os.Rename の前に呼ぶ
	rename *renameHook
	// ops は、ファイルの書き出しとリネームを呼んだ順に記録する
	ops   *opLog
	lock  *fakeLocker
	procs *fakeProcesses
	// exePath は、登録したゲームの実行ファイルのパス
	exePath string
}

type renameHook struct {
	fn func(oldpath, newpath string) error
}

// fileOp は、コアが呼んだファイルの書き出し（kind が "sync"）かリネーム（kind が "rename"）の 1 回。
type fileOp struct {
	kind string
	path string
	to   string
}

// opLog は、書き出しとリネームを呼んだ順に記録する。stage への取得は並列なので、mu で守る。
// failSync が nil でなければ、書き出しの前に呼び、エラーを返せばその書き出しを失敗させる。
type opLog struct {
	mu       sync.Mutex
	ops      []fileOp
	failSync func(path string) error
}

func (l *opLog) sync(f *os.File) error {
	l.mu.Lock()
	fail := l.failSync
	l.ops = append(l.ops, fileOp{kind: "sync", path: f.Name()})
	l.mu.Unlock()
	if fail != nil {
		if err := fail(f.Name()); err != nil {
			return err
		}
	}
	return f.Sync()
}

func (l *opLog) renamed(oldpath, newpath string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ops = append(l.ops, fileOp{kind: "rename", path: oldpath, to: newpath})
}

// syncedBeforeRename は、isTarget に当たる最初のリネームより前に書き出したファイルのうち、そのリネームで移す
// ディレクトリの中のものを、区切りを / にした相対パスの昇順で返す。当たるリネームがなければテストを止める。
func (l *opLog) syncedBeforeRename(t *testing.T, isTarget func(op fileOp) bool) []string {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	for i, op := range l.ops {
		if op.kind != "rename" || !isTarget(op) {
			continue
		}
		synced := []string{}
		for _, s := range l.ops[:i] {
			if s.kind != "sync" {
				continue
			}
			rel, err := filepath.Rel(op.path, s.path)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				continue
			}
			synced = append(synced, filepath.ToSlash(rel))
		}
		sort.Strings(synced)
		return synced
	}
	t.Fatalf("当たるリネームがない: %+v", l.ops)
	return nil
}

// fakeLocker は、PC 全体の同期ロックの偽物。deny が真のあいだは、ほかの同期が握っているものとして取れない。
type fakeLocker struct {
	mu       sync.Mutex
	deny     bool
	held     bool
	acquired int
}

func (l *fakeLocker) TryLock() (func() error, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.deny || l.held {
		return nil, false, nil
	}
	l.held = true
	l.acquired++
	return func() error {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.held = false
		return nil
	}, true, nil
}

func (l *fakeLocker) state() (acquired int, held bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.acquired, l.held
}

// fakeProcesses は、ゲームのプロセスの検出の偽物。runningFrom が 0 でなければ、その回目以降の呼び出しで
// 実行中を返す（2 なら、1 回目の確認は通り、転送の後の 2 回目で実行中になる）。
type fakeProcesses struct {
	mu          sync.Mutex
	runningFrom int
	paths       []string
}

func (p *fakeProcesses) Running(exePath string) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.paths = append(p.paths, exePath)
	return p.runningFrom > 0 && len(p.paths) >= p.runningFrom, nil
}

func (p *fakeProcesses) asked() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.paths...)
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

	// 実行ファイルがなければ、Push・Pull は実行中かを確かめられずに止まる（仕様書 7 章）
	exePath := filepath.Join(dir, "game.exe")
	if err := os.WriteFile(exePath, nil, 0o700); err != nil {
		t.Fatalf("write exe: %v", err)
	}
	dbPath := filepath.Join(dir, "cloudlaunch.db")
	st, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.CreateGame(context.Background(), store.NewGame{
		ID:             testGameID,
		Title:          "Game",
		SaveFolderPath: saveDir,
		ExePath:        exePath,
		CreatedAt:      time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("CreateGame: %v", err)
	}

	backupsDir := filepath.Join(dir, "backups")
	hook := &renameHook{}
	ops := &opLog{}
	lock := &fakeLocker{}
	procs := &fakeProcesses{}
	cfg := syncore.Config{
		Remote:        remote,
		Local:         st,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		DeviceName:    testDeviceName,
		BackupsDir:    backupsDir,
		Locker:        lock,
		Processes:     procs,
		Now:           func() time.Time { return testNow },
		NewGeneration: func() (string, error) { return pushGeneration, nil },
		Rename: func(oldpath, newpath string) error {
			if hook.fn != nil {
				if err := hook.fn(oldpath, newpath); err != nil {
					return err
				}
			}
			if err := os.Rename(oldpath, newpath); err != nil {
				return err
			}
			ops.renamed(oldpath, newpath)
			return nil
		},
		SyncFile: ops.sync,
	}
	return env{core: syncore.New(cfg), cfg: cfg, store: st, remote: remote, saveDir: saveDir, dbPath: dbPath, backupsDir: backupsDir, rename: hook, ops: ops, lock: lock, procs: procs, exePath: exePath}
}

// coreWithout は、e のコアと同じ設定から edit で 1 項目だけ外したコアを作る。必須の項目がないときの確認で、
// 外した項目のほかは揃っているようにするため（別の必須の項目がないことで先に止まると、確認したいことを確かめない）。
func (e env) coreWithout(edit func(cfg *syncore.Config)) *syncore.Core {
	cfg := e.cfg
	edit(&cfg)
	return syncore.New(cfg)
}

// gameBackups は、ゲームのバックアップのディレクトリにある名前を、名前の順で返す。ディレクトリがなければ空を返す。
func (e env) gameBackups(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(e.backupsDir, testGameID))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("read backups: %v", err)
	}
	names := make([]string, len(entries))
	for i, d := range entries {
		names[i] = d.Name()
	}
	return names
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

func headKey() string { return "games/" + testGameID + "/HEAD" }
func manifestKey(generation string) string {
	return "games/" + testGameID + "/manifests/" + generation + ".json"
}
func objectKey(content string) string {
	return "games/" + testGameID + "/objects/" + sha256Hex(content)
}

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

// openDB は、store が公開していない読み書き（ジャーナルの行の数、セーブフォルダのパスの変更）のために、同じ DB を別に開く。
func (e env) openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", e.dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func (e env) execDB(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := e.openDB(t).Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func (e env) journalCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.openDB(t).QueryRow(`SELECT count(*) FROM pull_operations`).Scan(&n); err != nil {
		t.Fatalf("count pull_operations: %v", err)
	}
	return n
}

// workDirs は、セーブフォルダの親ディレクトリにある stage と old の名前を返す。
func (e env) workDirs(t *testing.T, kind string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(e.saveDir), ".cloudlaunch-"+kind+"-*"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	return matches
}

// readFiles はセーブフォルダの全ファイルを、区切りを / にした相対パスをキーにして返す。
func readFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatalf("read files: %v", err)
	}
	return files
}

func (e env) base(t *testing.T) string {
	t.Helper()
	game, err := e.store.GameLocal(context.Background(), testGameID)
	if err != nil {
		t.Fatalf("GameLocal: %v", err)
	}
	return game.BaseManifestHash
}
