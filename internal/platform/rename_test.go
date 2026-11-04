package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRename(t *testing.T) {
	// src は、中にファイルを 1 つ持つディレクトリを作って返す
	src := func(t *testing.T, dir string) string {
		t.Helper()
		p := filepath.Join(dir, "src")
		if err := os.Mkdir(p, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(p, "slot1.sav"), []byte("one"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		return p
	}
	assertContent := func(t *testing.T, dir string) {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dir, "slot1.sav"))
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if string(b) != "one" {
			t.Errorf("slot1.sav = %q, want %q", b, "one")
		}
	}

	t.Run("3章/ディスクへの書き出し/ディレクトリをリネームできる", func(t *testing.T) {
		dir := t.TempDir()
		from := src(t, dir)
		to := filepath.Join(dir, "sub", "dst")
		if err := os.Mkdir(filepath.Dir(to), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}

		if err := Rename(from, to); err != nil {
			t.Fatalf("Rename: %v", err)
		}
		assertContent(t, to)
		if _, err := os.Lstat(from); !os.IsNotExist(err) {
			t.Errorf("移す元が残っている (err = %v)", err)
		}
	})

	t.Run("3章/ディスクへの書き出し/移す先があればリネームは失敗する", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			create func(p string) error
		}{
			{"空のディレクトリ", func(p string) error { return os.Mkdir(p, 0o700) }},
			{"ファイル", func(p string) error { return os.WriteFile(p, nil, 0o600) }},
		} {
			t.Run(tc.name, func(t *testing.T) {
				dir := t.TempDir()
				from := src(t, dir)
				to := filepath.Join(dir, "dst")
				if err := tc.create(to); err != nil {
					t.Fatalf("create dst: %v", err)
				}

				if err := Rename(from, to); err == nil {
					t.Fatal("Rename error = nil, want an error")
				}
				assertContent(t, from)
			})
		}
	})
}
