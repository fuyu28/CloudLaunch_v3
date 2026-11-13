package metasync_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/fuyu28/CloudLaunch_v3/internal/metasync"
	"github.com/fuyu28/CloudLaunch_v3/internal/objstore/memstore"
	"github.com/fuyu28/CloudLaunch_v3/internal/store"
)

// maxImageSize は、仕様書 8-I2・8-V13 の 10 MiB。
const maxImageSize = 10 << 20

var (
	pngMagic  = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	jpegMagic = []byte{0xff, 0xd8, 0xff}
	webpMagic = []byte("RIFF\x00\x00\x00\x00WEBP")

	testPNG   = imageBody(pngMagic, "test")
	testImage = sha256Hex(testPNG)
)

// imageBody は、magic で始まり、note で中身を区別する画像の本文を返す。形式は先頭のバイトだけで判定する（ADR）ので、
// 画像として解読できる必要はない。
func imageBody(magic []byte, note string) []byte {
	return append(append([]byte{}, magic...), note...)
}

// imageOfSize は、magic で始まる size バイトの本文を返す。
func imageOfSize(magic []byte, size int) []byte {
	return append(append([]byte{}, magic...), bytes.Repeat([]byte{0}, size-len(magic))...)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func imageKey(sha string) string {
	return "games/" + testGameID + "/images/" + sha
}

// putOrder は、PUT したキーを呼んだ順に返す。
func (e *env) putOrder() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string{}, e.puts...)
}

func (e *env) cachedImage(t *testing.T, sha string) ([]byte, bool) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(e.imageDir, sha))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false
	}
	if err != nil {
		t.Fatalf("read cache %s: %v", sha, err)
	}
	return b, true
}

// cacheFiles は、キャッシュのフォルダにあるファイルの名前を返す。フォルダがなければ空を返す。
func (e *env) cacheFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(e.imageDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("read cache dir: %v", err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// setImage は、set-image と同じく、画像をキャッシュに入れてから DB の imageSha256 を書き換える。
func (e *env) setImage(t *testing.T, body []byte, now time.Time) string {
	t.Helper()
	sha, err := e.core.CacheImage(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("CacheImage: %v", err)
	}
	if err := e.store.EditGame(context.Background(), testGameID, store.GameEdit{ImageSha256: &sha}, now); err != nil {
		t.Fatalf("EditGame: %v", err)
	}
	return sha
}

// remoteGameJSONWithImage は、ローカルより新しく、imageSha256 が sha の game.json を返す。
func remoteGameJSONWithImage(sha string) string {
	return fmt.Sprintf(`{"id":%q,"title":%q,"imageSha256":%q,"createdAt":%q,"updatedAt":%q}`,
		testGameID, remoteTitle, sha, olderStamp, newerStamp)
}

func TestSyncImage(t *testing.T) {
	ctx := context.Background()
	later := testPlayedAt.Add(time.Hour)

	t.Run("8-I1 画像を PUT してから game.json を PUT する", func(t *testing.T) {
		e := newBareEnv(t)
		sha := e.setImage(t, testPNG, later)

		got, err := e.core.Sync(ctx, testGameID)
		if err != nil {
			t.Fatalf("Sync: %v", err)
		}
		if diff := cmp.Diff(metasync.Result{GameInfo: metasync.GameInfoUploaded, Image: metasync.ImageUploaded}, got); diff != "" {
			t.Errorf("Result (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]string{imageKey(sha), gameKey}, e.putOrder()); diff != "" {
			t.Errorf("PUT したキーの順 (-want +got):\n%s", diff)
		}
		if body := e.remoteBody(t, imageKey(sha)); body != string(testPNG) {
			t.Errorf("リモートの画像が違う: %q", body)
		}
	})

	t.Run("8-I1 画像の PUT に失敗したら、game.json を PUT しない", func(t *testing.T) {
		e := newBareEnv(t)
		sha := e.setImage(t, testPNG, later)
		errInjected := errors.New("injected")
		e.remote.Before = func(op memstore.Op, key string) error {
			if op == memstore.OpPut && key == imageKey(sha) {
				return errInjected
			}
			if op == memstore.OpPut && key == gameKey {
				t.Errorf("game.json を PUT した")
			}
			return nil
		}

		if _, err := e.core.Sync(ctx, testGameID); !errors.Is(err, errInjected) {
			t.Errorf("Sync = %v, want %v", err, errInjected)
		}
	})

	t.Run("8-I1 リモートに画像があれば PUT しない", func(t *testing.T) {
		e := newBareEnv(t)
		sha := e.setImage(t, testPNG, later)
		e.putRemote(t, imageKey(sha), string(testPNG))

		got, err := e.core.Sync(ctx, testGameID)
		if err != nil {
			t.Fatalf("Sync: %v", err)
		}
		if diff := cmp.Diff(metasync.Result{GameInfo: metasync.GameInfoUploaded, Image: metasync.ImageUnchanged}, got); diff != "" {
			t.Errorf("Result (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]string{gameKey}, e.putOrder()); diff != "" {
			t.Errorf("PUT したキー (-want +got):\n%s", diff)
		}
	})

	t.Run("8-I3 game.json が指す画像がキャッシュになければ取得する", func(t *testing.T) {
		e := newBareEnv(t)
		e.putRemote(t, gameKey, remoteGameJSONWithImage(testImage))
		e.putRemote(t, imageKey(testImage), string(testPNG))

		got, err := e.core.Sync(ctx, testGameID)
		if err != nil {
			t.Fatalf("Sync: %v", err)
		}
		if diff := cmp.Diff(metasync.Result{GameInfo: metasync.GameInfoDownloaded, Image: metasync.ImageDownloaded}, got); diff != "" {
			t.Errorf("Result (-want +got):\n%s", diff)
		}
		if b, ok := e.cachedImage(t, testImage); !ok || !bytes.Equal(b, testPNG) {
			t.Errorf("キャッシュの画像 = %q (ok %v), want %q", b, ok, testPNG)
		}
	})

	t.Run("8-I3 game.json が指す画像がキャッシュにあれば取得しない", func(t *testing.T) {
		e := newBareEnv(t)
		e.setImage(t, testPNG, testPlayedAt) // キャッシュに入れる。DB はこの後リモートの game.json で置き換わる
		e.putRemote(t, gameKey, remoteGameJSONWithImage(testImage))
		e.putRemote(t, imageKey(testImage), string(testPNG))

		got, err := e.core.Sync(ctx, testGameID)
		if err != nil {
			t.Fatalf("Sync: %v", err)
		}
		if diff := cmp.Diff(metasync.Result{GameInfo: metasync.GameInfoDownloaded, Image: metasync.ImageUnchanged}, got); diff != "" {
			t.Errorf("Result (-want +got):\n%s", diff)
		}
		for _, key := range e.getKeys() {
			if key == imageKey(testImage) {
				t.Errorf("キャッシュにある画像を GET した")
			}
		}
	})

	tooLarge := imageOfSize(pngMagic, maxImageSize+1)
	gif := imageBody([]byte("GIF89a"), "gif")
	invalid := []struct {
		name string
		key  string
		body []byte
	}{
		{"8-V12 中身の sha256 がキーと違う画像はキャッシュに入れない", testImage, imageBody(pngMagic, "other")},
		{"8-V13 10 MiB を超える画像はキャッシュに入れない", sha256Hex(tooLarge), tooLarge},
		{"8-V14 PNG・JPEG・WebP でない画像はキャッシュに入れない", sha256Hex(gif), gif},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			e := newBareEnv(t)
			e.putRemote(t, gameKey, remoteGameJSONWithImage(tt.key))
			e.putRemote(t, imageKey(tt.key), string(tt.body))

			got, err := e.core.Sync(ctx, testGameID)
			if err != nil {
				t.Fatalf("Sync: %v", err)
			}
			if diff := cmp.Diff(metasync.Result{GameInfo: metasync.GameInfoDownloaded, Image: metasync.ImageInvalid}, got); diff != "" {
				t.Errorf("Result (-want +got):\n%s", diff)
			}
			if files := e.cacheFiles(t); len(files) != 0 {
				t.Errorf("キャッシュに入れた: %v", files)
			}
		})
	}

	t.Run("8-I4 キャッシュの画像の中身が sha256 と違えば、画像を PUT せずに game.json を PUT する", func(t *testing.T) {
		e := newBareEnv(t)
		sha := e.setImage(t, testPNG, later)
		path := filepath.Join(e.imageDir, sha)
		if err := os.WriteFile(path, imageBody(pngMagic, "corrupted"), 0o600); err != nil {
			t.Fatalf("corrupt cache: %v", err)
		}

		got, err := e.core.Sync(ctx, testGameID)
		if err != nil {
			t.Fatalf("Sync: %v", err)
		}
		if diff := cmp.Diff(metasync.Result{GameInfo: metasync.GameInfoUploaded, Image: metasync.ImageMissing}, got); diff != "" {
			t.Errorf("Result (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]string{gameKey}, e.putOrder()); diff != "" {
			t.Errorf("PUT したキー (-want +got):\n%s", diff)
		}
		if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("壊れたキャッシュの画像が残っている: %v", err)
		}
	})

	t.Run("8-I4 画像がキャッシュにもリモートにもなければ、imageSha256 を入れたまま game.json を PUT する", func(t *testing.T) {
		e := newBareEnv(t)
		execDB(t, e.dbPath, `UPDATE games SET image_sha256 = ? WHERE id = ?`, testImage, testGameID)
		e.addSession(t, sessionA, 60)

		got, err := e.core.Sync(ctx, testGameID)
		if err != nil {
			t.Fatalf("Sync: %v", err)
		}
		want := metasync.Result{GameInfo: metasync.GameInfoUploaded, Image: metasync.ImageMissing, UploadedSessions: 1}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("Result (-want +got):\n%s", diff)
		}
		if body := e.remoteBody(t, gameKey); !strings.Contains(body, `"imageSha256":"`+testImage+`"`) {
			t.Errorf("game.json に imageSha256 がない: %s", body)
		}
		if got := e.localGameInfo(t).ImageSha256; got != testImage {
			t.Errorf("DB の imageSha256 = %q, want %q", got, testImage)
		}
	})

	t.Run("8章/画像/取得する画像がリモートになければ、画像なしで続ける", func(t *testing.T) {
		e := newBareEnv(t)
		e.putRemote(t, gameKey, remoteGameJSONWithImage(testImage))
		e.putRemoteSession(t, sessionA, 60)

		got, err := e.core.Sync(ctx, testGameID)
		if err != nil {
			t.Fatalf("Sync: %v", err)
		}
		want := metasync.Result{GameInfo: metasync.GameInfoDownloaded, Image: metasync.ImageMissing, DownloadedSessions: 1}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("Result (-want +got):\n%s", diff)
		}
	})

	t.Run("8章/画像/画像の GET に失敗したら、プレイ履歴を同期せずにエラーを返す", func(t *testing.T) {
		e := newBareEnv(t)
		e.putRemote(t, gameKey, remoteGameJSONWithImage(testImage))
		e.putRemote(t, imageKey(testImage), string(testPNG))
		e.addSession(t, sessionA, 60)
		errInjected := errors.New("injected")
		e.remote.Before = func(op memstore.Op, key string) error {
			if op == memstore.OpGet && key == imageKey(testImage) {
				return errInjected
			}
			if strings.Contains(key, "/sessions/") {
				t.Errorf("プレイ履歴を同期した: op %d, key %s", op, key)
			}
			return nil
		}

		if _, err := e.core.Sync(ctx, testGameID); !errors.Is(err, errInjected) {
			t.Errorf("Sync = %v, want %v", err, errInjected)
		}
	})

	t.Run("8章/画像/画像を変えても、古い画像を削除しない", func(t *testing.T) {
		e := newBareEnv(t)
		first := e.setImage(t, testPNG, later)
		if _, err := e.core.Sync(ctx, testGameID); err != nil {
			t.Fatalf("Sync 1: %v", err)
		}
		e.remote.Before = func(op memstore.Op, key string) error {
			if op == memstore.OpDelete {
				t.Errorf("削除した: %s", key)
			}
			return nil
		}
		second := e.setImage(t, imageBody(jpegMagic, "second"), later.Add(time.Hour))
		if _, err := e.core.Sync(ctx, testGameID); err != nil {
			t.Fatalf("Sync 2: %v", err)
		}

		for _, sha := range []string{first, second} {
			e.remoteBody(t, imageKey(sha))
		}
	})

	t.Run("8章/画像/2 台の PC で、設定した画像が相手のキャッシュに入る", func(t *testing.T) {
		remote := memstore.New()
		pcA, pcB := newEnvOn(t, remote), newEnvOn(t, remote)
		sha := pcA.setImage(t, testPNG, later)
		for _, pc := range []*env{pcA, pcB} {
			if _, err := pc.core.Sync(ctx, testGameID); err != nil {
				t.Fatalf("Sync: %v", err)
			}
		}

		if got := pcB.localGameInfo(t).ImageSha256; got != sha {
			t.Errorf("PC B の imageSha256 = %q, want %q", got, sha)
		}
		if b, ok := pcB.cachedImage(t, sha); !ok || !bytes.Equal(b, testPNG) {
			t.Errorf("PC B のキャッシュの画像 = %q (ok %v), want %q", b, ok, testPNG)
		}
	})
}

func TestCacheImage(t *testing.T) {
	tests := []struct {
		name string
		body []byte
		ok   bool
	}{
		{"8-I2 PNG の画像を受け入れる", imageBody(pngMagic, "png"), true},
		{"8-I2 JPEG の画像を受け入れる", imageBody(jpegMagic, "jpeg"), true},
		{"8-I2 WebP の画像を受け入れる", imageBody(webpMagic, "webp"), true},
		{"8-I2 ちょうど 10 MiB の画像を受け入れる", imageOfSize(pngMagic, maxImageSize), true},
		{"8-I2 10 MiB を超える画像を拒否する", imageOfSize(pngMagic, maxImageSize+1), false},
		{"8-I2 GIF の画像を拒否する", imageBody([]byte("GIF89a"), "gif"), false},
		{"8-I2 WebP でない RIFF を拒否する", imageBody([]byte("RIFF\x00\x00\x00\x00WAVE"), "wav"), false},
		{"8-I2 先頭が足りない PNG を拒否する", pngMagic[:7], false},
		{"8-I2 空の画像を拒否する", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newBareEnv(t)

			sha, err := e.core.CacheImage(bytes.NewReader(tt.body))
			if !tt.ok {
				if err == nil {
					t.Error("CacheImage がエラーを返さなかった")
				}
				if files := e.cacheFiles(t); len(files) != 0 {
					t.Errorf("キャッシュに入れた: %v", files)
				}
				return
			}
			if err != nil {
				t.Fatalf("CacheImage: %v", err)
			}
			if want := sha256Hex(tt.body); sha != want {
				t.Errorf("sha256 = %s, want %s", sha, want)
			}
			if b, ok := e.cachedImage(t, sha); !ok || !bytes.Equal(b, tt.body) {
				t.Errorf("キャッシュの画像が違う (ok %v, %d バイト)", ok, len(b))
			}
			if diff := cmp.Diff([]string{sha}, e.cacheFiles(t)); diff != "" {
				t.Errorf("キャッシュのファイル (-want +got):\n%s", diff)
			}
		})
	}

	// Windows では、開かれているファイルへのリネームが失敗するので、キャッシュにある画像は書き直さない
	t.Run("8章/画像/キャッシュにある画像は書き直さない", func(t *testing.T) {
		e := newBareEnv(t)
		sha := e.setImage(t, testPNG, testPlayedAt)
		path := filepath.Join(e.imageDir, sha)
		old := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatalf("chtimes: %v", err)
		}

		if _, err := e.core.CacheImage(bytes.NewReader(testPNG)); err != nil {
			t.Fatalf("CacheImage: %v", err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if !info.ModTime().Equal(old) {
			t.Errorf("書き直した: mtime %v", info.ModTime())
		}
	})
}
