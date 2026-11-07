package syncore_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

// fakeSHA は、キャッシュに入れる、実際の中身と違う sha256。判定にこの値が現れれば、計算せずにキャッシュを使ったことが分かる。
var fakeSHA = strings.Repeat("f", 64)

// oldMTime は、テストのコアの時刻（testNow）より十分前の mtime。キャッシュの計算を始めた時刻より 2 秒以上古くする。
var oldMTime = testNow.Add(-time.Hour)

// writeOld は、files を置き、mtime を oldMTime にする。
func writeOld(t *testing.T, root string, files map[string]string) {
	t.Helper()
	writeFiles(t, root, files)
	for rel := range files {
		if err := os.Chtimes(filepath.Join(root, filepath.FromSlash(rel)), oldMTime, oldMTime); err != nil {
			t.Fatalf("chtimes %s: %v", rel, err)
		}
	}
}

// cacheRow は、root の rel を stat した size と mtime に、sha256 と計算を始めた時刻を添えたキャッシュの行を返す。
func cacheRow(t *testing.T, root, rel, sha string, hashedAt time.Time) syncore.HashCacheEntry {
	t.Helper()
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("stat %s: %v", rel, err)
	}
	return syncore.HashCacheEntry{Path: rel, Size: info.Size(), MTimeNs: info.ModTime().UnixNano(), SHA256: sha, HashedAt: hashedAt}
}

func (e env) replaceCache(t *testing.T, rows ...syncore.HashCacheEntry) {
	t.Helper()
	if err := e.store.ReplaceHashCache(context.Background(), testGameID, rows); err != nil {
		t.Fatalf("ReplaceHashCache: %v", err)
	}
}

func (e env) cache(t *testing.T) []syncore.HashCacheEntry {
	t.Helper()
	rows, err := e.store.HashCache(context.Background(), testGameID)
	if err != nil {
		t.Fatalf("HashCache: %v", err)
	}
	return rows
}

// statusSHA は、Status を呼び、local の entries のうち path の sha256 を返す。
func statusSHA(t *testing.T, e env, path string) string {
	t.Helper()
	plan, err := e.status(t)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	for _, entry := range plan.Local.Entries {
		if entry.Path == path {
			return entry.SHA256
		}
	}
	t.Fatalf("%s が local の entries にない: %v", path, plan.Local.Entries)
	return ""
}

func TestHashCache(t *testing.T) {
	hashedAt := testNow.Add(-30 * time.Minute)

	t.Run("3章/ハッシュキャッシュ/size と mtime が一致するファイルは計算し直さない", func(t *testing.T) {
		e := newEnv(t)
		writeOld(t, e.saveDir, map[string]string{"slot.sav": "one"})
		e.replaceCache(t, cacheRow(t, e.saveDir, "slot.sav", fakeSHA, hashedAt))

		if got := statusSHA(t, e, "slot.sav"); got != fakeSHA {
			t.Errorf("sha256 = %q, want キャッシュの値 %q", got, fakeSHA)
		}
	})

	t.Run("3章/ハッシュキャッシュ/size が違えば計算し直す", func(t *testing.T) {
		e := newEnv(t)
		writeOld(t, e.saveDir, map[string]string{"slot.sav": "one"})
		row := cacheRow(t, e.saveDir, "slot.sav", fakeSHA, hashedAt)
		row.Size++
		e.replaceCache(t, row)

		if got := statusSHA(t, e, "slot.sav"); got != sha256Hex("one") {
			t.Errorf("sha256 = %q, want %q", got, sha256Hex("one"))
		}
	})

	t.Run("3章/ハッシュキャッシュ/mtime が違えば計算し直す", func(t *testing.T) {
		e := newEnv(t)
		writeOld(t, e.saveDir, map[string]string{"slot.sav": "one"})
		row := cacheRow(t, e.saveDir, "slot.sav", fakeSHA, hashedAt)
		row.MTimeNs -= int64(time.Second)
		e.replaceCache(t, row)

		if got := statusSHA(t, e, "slot.sav"); got != sha256Hex("one") {
			t.Errorf("sha256 = %q, want %q", got, sha256Hex("one"))
		}
	})

	t.Run("3章/ハッシュキャッシュ/mtime が計算を始めた時刻の 2 秒前より新しければ計算し直す", func(t *testing.T) {
		e := newEnv(t)
		writeOld(t, e.saveDir, map[string]string{"slot.sav": "one"})
		e.replaceCache(t, cacheRow(t, e.saveDir, "slot.sav", fakeSHA, oldMTime.Add(time.Second)))

		if got := statusSHA(t, e, "slot.sav"); got != sha256Hex("one") {
			t.Errorf("sha256 = %q, want %q", got, sha256Hex("one"))
		}
	})

	t.Run("3章/ハッシュキャッシュ/キャッシュの sha256 を使った行は、計算を始めた時刻を引き継ぐ", func(t *testing.T) {
		e := newEnv(t)
		writeOld(t, e.saveDir, map[string]string{"kept.sav": "kept", "new.sav": "new"})
		kept := cacheRow(t, e.saveDir, "kept.sav", fakeSHA, hashedAt)
		e.replaceCache(t, kept)

		if _, err := e.status(t); err != nil {
			t.Fatalf("Status: %v", err)
		}
		want := []syncore.HashCacheEntry{kept, cacheRow(t, e.saveDir, "new.sav", sha256Hex("new"), testNow)}
		if d := cmp.Diff(want, e.cache(t)); d != "" {
			t.Errorf("キャッシュ (-want +got):\n%s", d)
		}
	})

	t.Run("3章/ハッシュキャッシュ/消えたファイルの行を消す", func(t *testing.T) {
		e := newEnv(t)
		writeOld(t, e.saveDir, map[string]string{"slot.sav": "one"})
		e.replaceCache(t,
			cacheRow(t, e.saveDir, "slot.sav", sha256Hex("one"), hashedAt),
			syncore.HashCacheEntry{Path: "gone.sav", Size: 1, MTimeNs: 1, SHA256: fakeSHA, HashedAt: hashedAt},
		)

		if _, err := e.status(t); err != nil {
			t.Fatalf("Status: %v", err)
		}
		want := []syncore.HashCacheEntry{cacheRow(t, e.saveDir, "slot.sav", sha256Hex("one"), hashedAt)}
		if d := cmp.Diff(want, e.cache(t)); d != "" {
			t.Errorf("キャッシュ (-want +got):\n%s", d)
		}
	})

	t.Run("3章/ハッシュキャッシュ/Status がキャッシュを書く", func(t *testing.T) {
		e := newEnv(t)
		writeOld(t, e.saveDir, map[string]string{"slot.sav": "one", "dir/b.sav": "two"})

		if _, err := e.status(t); err != nil {
			t.Fatalf("Status: %v", err)
		}
		want := []syncore.HashCacheEntry{
			cacheRow(t, e.saveDir, "dir/b.sav", sha256Hex("two"), testNow),
			cacheRow(t, e.saveDir, "slot.sav", sha256Hex("one"), testNow),
		}
		if d := cmp.Diff(want, e.cache(t)); d != "" {
			t.Errorf("キャッシュ (-want +got):\n%s", d)
		}
	})

	t.Run("3章/ハッシュキャッシュ/Push の承認後の確かめでもキャッシュを使う", func(t *testing.T) {
		e := newEnv(t)
		files := map[string]string{"slot.sav": "one"}
		writeOld(t, e.saveDir, files)
		e.putObject(t, objectKey("one"), "one")
		plan := approvedPush(t, e, files)
		// 承認した後に、キャッシュだけが違う sha256 を持つ。確かめがキャッシュを使えば、local が変わったと見える
		writeOld(t, e.saveDir, files)
		e.replaceCache(t, cacheRow(t, e.saveDir, "slot.sav", fakeSHA, hashedAt))

		if err := e.core.Push(t.Context(), plan, syncore.PushOptions{}); !errors.Is(err, syncore.ErrPlanStale) {
			t.Fatalf("Push error = %v, want ErrPlanStale", err)
		}
	})

	t.Run("3章/ハッシュキャッシュ/Pull の承認後の確かめでもキャッシュを使う", func(t *testing.T) {
		e := newEnv(t)
		e.setBase(t, syncore.ManifestHash(entriesOf(map[string]string{"slot.sav": "one"})))
		writeOld(t, e.saveDir, map[string]string{"slot.sav": "one"})
		plan := approvedPull(t, e, map[string]string{"slot.sav": "remote"})
		e.replaceCache(t, cacheRow(t, e.saveDir, "slot.sav", fakeSHA, hashedAt))

		if err := e.core.Pull(t.Context(), plan, syncore.PullOptions{}); !errors.Is(err, syncore.ErrPlanStale) {
			t.Fatalf("Pull error = %v, want ErrPlanStale", err)
		}
	})

	t.Run("3章/ハッシュキャッシュ/ForgetHashes の後はすべて計算し直す", func(t *testing.T) {
		e := newEnv(t)
		writeOld(t, e.saveDir, map[string]string{"slot.sav": "one"})
		e.replaceCache(t, cacheRow(t, e.saveDir, "slot.sav", fakeSHA, hashedAt))

		if err := e.core.ForgetHashes(t.Context(), testGameID); err != nil {
			t.Fatalf("ForgetHashes: %v", err)
		}
		if got := statusSHA(t, e, "slot.sav"); got != sha256Hex("one") {
			t.Errorf("sha256 = %q, want %q", got, sha256Hex("one"))
		}
	})
}
