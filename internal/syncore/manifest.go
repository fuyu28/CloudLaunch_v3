// セーブフォルダからの manifest の計算と、manifest ハッシュ（仕様書 2 章）。
package syncore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
)

type Entry struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type Manifest struct {
	Version    int     `json:"version"`
	CreatedAt  string  `json:"createdAt"`
	DeviceName string  `json:"deviceName"`
	Entries    []Entry `json:"entries"`
}

var emptyManifestHash = ManifestHash(nil)

// ManifestHash は entries を並べ替えず、渡された順のまま正規化 JSON にする。
func ManifestHash(entries []Entry) string {
	b := []byte{'['}
	for i, e := range entries {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, `{"path":`...)
		b = appendJSONString(b, e.Path)
		b = append(b, `,"sha256":`...)
		b = appendJSONString(b, e.SHA256)
		b = append(b, `,"size":`...)
		b = strconv.AppendInt(b, e.Size, 10)
		b = append(b, '}')
	}
	b = append(b, ']')
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// appendJSONString は RFC 8785 のエスケープで書く。encoding/json は U+2028・U+2029 もエスケープし、JCS と一致しないので使わない
func appendJSONString(b []byte, s string) []byte {
	const hexDigits = "0123456789abcdef"
	b = append(b, '"')
	for i := range len(s) {
		c := s[i]
		switch c {
		case '"', '\\':
			b = append(b, '\\', c)
		case '\b':
			b = append(b, `\b`...)
		case '\f':
			b = append(b, `\f`...)
		case '\n':
			b = append(b, `\n`...)
		case '\r':
			b = append(b, `\r`...)
		case '\t':
			b = append(b, `\t`...)
		default:
			if c < 0x20 {
				b = append(b, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xf])
			} else {
				b = append(b, c)
			}
		}
	}
	return append(b, '"')
}

// isPlainDir は、種類のビットが ModeDir だけかを返す。IsDir だけで見ないのは、Go が Windows で、
// name surrogate でないディレクトリの reparse point（OneDrive のクラウドのフォルダなど）に
// ModeDir|ModeIrregular を付けるため。IsDir は ModeIrregular を見落とし、中まで読んでしまう（仕様書 2 章）
func isPlainDir(t fs.FileMode) bool {
	return t == fs.ModeDir
}

// listFiles は root 以下の通常のファイルの相対パスを返す。root がなければ空を返す。
func listFiles(root string) ([]string, error) {
	info, err := os.Lstat(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stat save folder: %w", err)
	}
	if !isPlainDir(info.Mode().Type()) {
		return nil, fmt.Errorf("save folder %s is not a plain directory (%s)", root, info.Mode().Type())
	}

	var rels []string
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if isPlainDir(d.Type()) {
			return nil
		}
		// 通常のファイル以外を飛ばすと、リンク先のセーブが同期されないまま失われうる（仕様書 2 章）
		if d.IsDir() {
			return fmt.Errorf("%s is a directory reparse point or another special directory (%s)", p, d.Type())
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file (%s)", p, d.Type())
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rels = append(rels, rel)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk save folder: %w", err)
	}
	return rels, nil
}

func hashFile(ctx context.Context, path string) (string, int64, error) {
	if err := ctx.Err(); err != nil {
		return "", 0, err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return "", 0, fmt.Errorf("read %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), size, nil
}
