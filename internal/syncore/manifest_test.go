package syncore_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

// emptyManifestHash は "[]" の sha256。
const emptyManifestHash = "4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945"

func TestManifest(t *testing.T) {
	t.Run("2章/manifest/サブディレクトリのファイルは / 区切りの path で昇順に並ぶ", func(t *testing.T) {
		e := newEnv(t)
		files := map[string]string{"z.sav": "z", "a/b.sav": "ab", "A.sav": "A", "a/c/d.sav": "acd"}
		writeFiles(t, e.saveDir, files)

		plan, err := e.status(t)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if d := cmp.Diff(entriesOf(files), plan.Local.Entries); d != "" {
			t.Errorf("Local.Entries (-want +got):\n%s", d)
		}
		var paths []string
		for _, en := range plan.Local.Entries {
			paths = append(paths, en.Path)
		}
		if d := cmp.Diff([]string{"A.sav", "a/b.sav", "a/c/d.sav", "z.sav"}, paths); d != "" {
			t.Errorf("paths (-want +got):\n%s", d)
		}
	})

	t.Run("2章/manifest/空ディレクトリは含めない", func(t *testing.T) {
		e := newEnv(t)
		files := map[string]string{"slot1.sav": "local"}
		writeFiles(t, e.saveDir, files)
		if err := os.MkdirAll(filepath.Join(e.saveDir, "empty", "nested"), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}

		plan, err := e.status(t)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if d := cmp.Diff(entriesOf(files), plan.Local.Entries); d != "" {
			t.Errorf("Local.Entries (-want +got):\n%s", d)
		}
	})

	t.Run("2章/manifest/通常のファイル以外があればエラー", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, map[string]string{"slot1.sav": "local"})
		targetDir := t.TempDir()
		writeFiles(t, targetDir, map[string]string{"target.sav": "linked"})
		if err := os.Symlink(filepath.Join(targetDir, "target.sav"), filepath.Join(e.saveDir, "link.sav")); err != nil {
			// Windows では、開発者モードか管理者の権限がないとシンボリックリンクを作れない
			t.Skipf("symlink: %v", err)
		}

		if _, err := e.status(t); err == nil {
			t.Fatal("Status succeeded, want error for non-regular file")
		}
	})

	invalidNames := []struct {
		name  string
		files []string
	}{
		{"正しい UTF-8 でない path があればエラー", []string{"a\xffb.sav"}},
		{"大文字小文字の違いだけで重複するファイルがあればエラー", []string{"Slot1.sav", "slot1.sav"}},
		{"予約されたデバイス名のファイルがあればエラー", []string{"dir/aux.sav"}},
		{"末尾が . のファイルがあればエラー", []string{"a.sav."}},
		{"使えない文字を含むファイルがあればエラー", []string{"a:b.sav"}},
		{"ディレクトリの部分が大文字小文字の違いだけで重複すればエラー", []string{"dir/a.sav", "DIR/b.sav"}},
	}
	for _, tt := range invalidNames {
		t.Run("2章/manifest の計算/"+tt.name, func(t *testing.T) {
			if runtime.GOOS == "windows" {
				t.Skip("Windows のファイルシステムには、この名前のファイルを作れない")
			}
			e := newEnv(t)
			files := map[string]string{"ok.sav": "ok"}
			for _, f := range tt.files {
				files[f] = f
			}
			writeFiles(t, e.saveDir, files)

			if _, err := e.status(t); err == nil {
				t.Fatal("Status succeeded, want error for invalid path")
			}
		})
	}

	t.Run("2章/manifest/version が 1 でなければエラー", func(t *testing.T) {
		e := newEnv(t)
		e.putObject(t, manifestKey(testGeneration),
			`{"version":2,"createdAt":"2026-10-27T00:00:00.000Z","deviceName":"Other","entries":[]}`)
		e.putObject(t, headKey(), testGeneration)

		if _, err := e.status(t); err == nil {
			t.Fatal("Status succeeded, want error for unknown version")
		}
	})
}

func TestManifestHash(t *testing.T) {
	t.Run("2章/manifest ハッシュ/既知の entries から固定値になる", func(t *testing.T) {
		// エスケープ（タブ・"）、エスケープしない文字（U+2028・非 ASCII）、2^53 を超える size を含める
		entries := []syncore.Entry{
			{Path: "save\t2.sav", SHA256: strings.Repeat("a", 64), Size: 1234},
			{Path: "save\"1.sav", SHA256: strings.Repeat("0", 64), Size: 0},
			{Path: "save 3.sav", SHA256: strings.Repeat("f", 64), Size: 9007199254740993},
			{Path: "セーブ/スロット1.sav", SHA256: strings.Repeat("0123456789abcdef", 4), Size: 42},
		}
		const want = "9a0bda2e03f743649d7009a7cda5d79cd0e514c3ce413b03427ea5743282b535"
		if got := syncore.ManifestHash(entries); got != want {
			t.Errorf("ManifestHash = %s, want %s", got, want)
		}
	})

	t.Run("2章/manifest ハッシュ/空 manifest は [] から計算する", func(t *testing.T) {
		for _, entries := range [][]syncore.Entry{nil, {}} {
			if got := syncore.ManifestHash(entries); got != emptyManifestHash {
				t.Errorf("ManifestHash(%#v) = %s, want %s", entries, got, emptyManifestHash)
			}
		}
	})

	t.Run("2章/manifest ハッシュ/createdAt と deviceName を含めない", func(t *testing.T) {
		e := newEnv(t)
		files := map[string]string{"slot1.sav": "local"}
		writeFiles(t, e.saveDir, files)
		e.putObject(t, manifestKey(testGeneration),
			`{"version":1,"createdAt":"2000-01-01T00:00:00.000Z","deviceName":"Another",`+
				`"entries":[{"path":"slot1.sav","sha256":"`+entriesOf(files)[0].SHA256+`","size":5}]}`)
		e.putObject(t, headKey(), testGeneration)

		plan, err := e.status(t)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if plan.Remote.Hash == "" || plan.Remote.Hash != plan.Local.Hash {
			t.Errorf("Remote.Hash = %q, want %q", plan.Remote.Hash, plan.Local.Hash)
		}
	})
}

func TestHEAD(t *testing.T) {
	tests := []struct {
		name string
		head string
	}{
		{name: "末尾に改行がある", head: testGeneration + "\n"},
		{name: "大文字を含む", head: strings.ToUpper(testGeneration)},
		{name: "UUIDv4 である", head: "0192d4e8-0000-4000-8000-0000000000aa"},
		{name: "空である", head: ""},
	}
	for _, tt := range tests {
		t.Run("2章/HEAD/形式が違えばエラー/"+tt.name, func(t *testing.T) {
			e := newEnv(t)
			e.putGeneration(t, []syncore.Entry{})
			e.putObject(t, headKey(), tt.head)

			if _, err := e.status(t); err == nil {
				t.Fatalf("Status succeeded with HEAD %q, want error", tt.head)
			}
		})
	}

	t.Run("2章/HEAD/指す manifest がなければエラー", func(t *testing.T) {
		e := newEnv(t)
		e.putObject(t, headKey(), testGeneration)

		if _, err := e.status(t); err == nil {
			t.Fatal("Status succeeded, want error for missing manifest")
		}
	})
}
