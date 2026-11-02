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
	"runtime"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/sync/errgroup"
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

// localEntries は root 以下の通常のファイルを path の昇順で返す。root がなければ空を返す。
func localEntries(ctx context.Context, root string) ([]Entry, error) {
	info, err := os.Lstat(root)
	if errors.Is(err, fs.ErrNotExist) {
		return []Entry{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stat save folder: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("save folder %s is not a directory (%s)", root, info.Mode().Type())
	}

	var rels []string
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		// 通常のファイル以外を飛ばすと、リンク先のセーブが同期されないまま失われうる（仕様書 11 章の未決事項）
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

	entries := make([]Entry, len(rels))
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(runtime.NumCPU())
	for i, rel := range rels {
		g.Go(func() error {
			sum, size, err := hashFile(ctx, filepath.Join(root, rel))
			if err != nil {
				return err
			}
			entries[i] = Entry{Path: filepath.ToSlash(rel), SHA256: sum, Size: size}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	// WalkDir はディレクトリごとに名前で並べるので、"a/b" が "a.sav" より先に来る。区切りを / にした path のバイト列の順に並べ直す
	slices.SortFunc(entries, func(a, b Entry) int { return strings.Compare(a.Path, b.Path) })
	return entries, nil
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
