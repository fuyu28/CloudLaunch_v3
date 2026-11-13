// ゲームの画像の同期とキャッシュ（仕様書 8 章「画像」）。画像は sha256 をキーにして、リモートの images/ と、ローカルの
// キャッシュのフォルダに、変換せずに置く。
package metasync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

// maxImageSize は、仕様書 8-I2・8-V13 の 10 MiB。
const maxImageSize = 10 << 20

var (
	pngMagic  = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	jpegMagic = []byte{0xff, 0xd8, 0xff}
)

func imagesPrefix(gameID string) string {
	return "games/" + gameID + "/images/"
}

func (c *Core) imagePath(sha string) string {
	return filepath.Join(c.cfg.ImageDir, sha)
}

// CacheImage は、画像を検証して（仕様書 8-I2）キャッシュに入れ、sha256 を返す。上限の 1 バイト先までしか読まない。
// リモートには触れない。リモートへは、次の同期で game.json を PUT する前に PUT する（8-I1）。
func (c *Core) CacheImage(r io.Reader) (string, error) {
	body, err := io.ReadAll(io.LimitReader(r, maxImageSize+1))
	if err != nil {
		return "", fmt.Errorf("read image: %w", err)
	}
	if err := checkImage(body); err != nil {
		return "", fmt.Errorf("8-I2: %w", err)
	}
	sha := sha256Hex(body)
	// Windows では、表示や同期で開かれているファイルへのリネームが失敗するので、すでにあれば書き直さない
	if _, err := os.Stat(c.imagePath(sha)); err == nil {
		return sha, nil
	}
	if err := c.writeCache(sha, body); err != nil {
		return "", err
	}
	return sha, nil
}

// checkImage は、大きさと形式を確かめる。形式は先頭のバイトだけで判定し、画像として解読できるかは確かめない。
func checkImage(body []byte) error {
	if len(body) > maxImageSize {
		return fmt.Errorf("image exceeds %d bytes", maxImageSize)
	}
	webp := len(body) >= 12 && string(body[:4]) == "RIFF" && string(body[8:12]) == "WEBP"
	if !bytes.HasPrefix(body, pngMagic) && !bytes.HasPrefix(body, jpegMagic) && !webp {
		return errors.New("image is not PNG, JPEG or WebP")
	}
	return nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// writeCache は、同じフォルダの一時ファイルに書いてからリネームする。途中まで書いたファイルを sha256 の名前で残さないため。
// キャッシュはリモートから取り直せるので、fsync はしない。
func (c *Core) writeCache(sha string, body []byte) error {
	if err := os.MkdirAll(c.cfg.ImageDir, 0o700); err != nil {
		return fmt.Errorf("create image cache dir: %w", err)
	}
	f, err := os.CreateTemp(c.cfg.ImageDir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("create image cache file: %w", err)
	}
	_, werr := f.Write(body)
	if err := errors.Join(werr, f.Close()); err != nil {
		_ = os.Remove(f.Name())
		return fmt.Errorf("write image cache file: %w", err)
	}
	if err := os.Rename(f.Name(), c.imagePath(sha)); err != nil {
		_ = os.Remove(f.Name())
		return fmt.Errorf("rename image cache file: %w", err)
	}
	return nil
}

// uploadImage は、g の画像がリモートになければ、キャッシュから PUT する。画像は書き換えない object なので、キーがあれば
// 中身は同じとみなす。そのため、壊れた中身を一度 PUT すると直らないので、PUT の前に sha256 を確かめ、違えばキャッシュから
// 消す。キャッシュにない（壊れていた）ときは、ログに残して ImageMissing を返す。
func (c *Core) uploadImage(ctx context.Context, g GameInfo) (ImageOutcome, error) {
	if g.ImageSha256 == "" {
		return ImageNone, nil
	}
	prefix := imagesPrefix(g.ID)
	key := prefix + g.ImageSha256
	keys, err := c.cfg.Remote.List(ctx, prefix)
	if err != nil {
		return 0, fmt.Errorf("list images: %w", err)
	}
	if slices.Contains(keys, key) {
		return ImageUnchanged, nil
	}
	body, err := os.ReadFile(c.imagePath(g.ImageSha256))
	if errors.Is(err, fs.ErrNotExist) {
		c.cfg.Logger.Warn("skip game.json upload: image is neither cached nor in remote", "gameId", g.ID, "imageSha256", g.ImageSha256)
		return ImageMissing, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read cached image: %w", err)
	}
	if sha256Hex(body) != g.ImageSha256 {
		c.cfg.Logger.Warn("skip game.json upload: cached image is corrupted", "gameId", g.ID, "imageSha256", g.ImageSha256)
		if err := os.Remove(c.imagePath(g.ImageSha256)); err != nil {
			return 0, fmt.Errorf("remove corrupted cached image: %w", err)
		}
		return ImageMissing, nil
	}
	if err := c.cfg.Remote.Put(ctx, key, bytes.NewReader(body), int64(len(body))); err != nil {
		return 0, fmt.Errorf("put image: %w", err)
	}
	return ImageUploaded, nil
}

// cacheRemoteImage は、sha の画像がキャッシュになければ取得して入れる（8-I3）。リモートにないものと、不正なもの
// （8-V12〜8-V14）は、ログに残して画像なしのまま返す。上限の 1 バイト先までしか読まない。
func (c *Core) cacheRemoteImage(ctx context.Context, gameID, sha string) (ImageOutcome, error) {
	if sha == "" {
		return ImageNone, nil
	}
	if _, err := os.Stat(c.imagePath(sha)); err == nil {
		return ImageUnchanged, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return 0, fmt.Errorf("stat cached image: %w", err)
	}
	r, err := c.cfg.Remote.Get(ctx, imagesPrefix(gameID)+sha)
	if errors.Is(err, fs.ErrNotExist) {
		c.cfg.Logger.Warn("skip image missing in remote", "gameId", gameID, "imageSha256", sha)
		return ImageMissing, nil
	}
	if err != nil {
		return 0, fmt.Errorf("get image: %w", err)
	}
	defer func() { _ = r.Close() }()
	body, err := io.ReadAll(io.LimitReader(r, maxImageSize+1))
	if err != nil {
		return 0, fmt.Errorf("read image: %w", err)
	}
	if err := validateRemoteImage(body, sha); err != nil {
		c.cfg.Logger.Warn("skip invalid remote image", "gameId", gameID, "imageSha256", sha, "err", err)
		return ImageInvalid, nil
	}
	if err := c.writeCache(sha, body); err != nil {
		return 0, err
	}
	return ImageDownloaded, nil
}

func validateRemoteImage(body []byte, sha string) error {
	if len(body) > maxImageSize {
		return fmt.Errorf("8-V13: image exceeds %d bytes", maxImageSize)
	}
	if got := sha256Hex(body); got != sha {
		return fmt.Errorf("8-V12: sha256 %s does not match key", got)
	}
	if err := checkImage(body); err != nil {
		return fmt.Errorf("8-V14: %w", err)
	}
	return nil
}
