// ハッシュキャッシュ（仕様書 3 章）を使った、ゲームのセーブフォルダの manifest の計算。
package syncore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
)

// racyWindow より計算を始めた時刻に近い mtime のファイルは、キャッシュを使わない。計算の直後に同じ size のまま
// 書き換わると、mtime の刻みが粗いファイルシステム（FAT32 は 2 秒）では mtime も同じになり、変化を見落とすため
const racyWindow = 2 * time.Second

// HashCacheEntry は、ハッシュキャッシュの 1 行。Path は区切りを / にした相対パス。HashedAt は、SHA256 の計算を始めた時刻。
type HashCacheEntry struct {
	Path     string
	Size     int64
	MTimeNs  int64
	SHA256   string
	HashedAt time.Time
}

// ForgetHashes は、ゲームのハッシュキャッシュを消す。次の計算は、すべてのファイルを読む。size と mtime を保ったまま
// 中身が変わったファイルを、キャッシュを使う限り見落とすため、計算し直させる手段として使う（仕様書 3 章）。
func (c *Core) ForgetHashes(ctx context.Context, gameID string) error {
	if err := c.local.DeleteHashCache(ctx, gameID); err != nil {
		return fmt.Errorf("forget hashes of %s: %w", gameID, err)
	}
	return nil
}

// saveFolderEntries は、判定と承認後の確かめが、ゲームのセーブフォルダの entries を path の昇順で返す。
// size と mtime がキャッシュと一致し、mtime がキャッシュの計算を始めた時刻より racyWindow 以上古いファイルは、
// 読まずにキャッシュの sha256 を使う。終わったら、ゲームのキャッシュを今あるファイルの分だけで置き換える。
func (c *Core) saveFolderEntries(ctx context.Context, gameID, root string) ([]Entry, error) {
	startedAt := c.now()
	rels, err := listFiles(root)
	if err != nil {
		return nil, err
	}
	cached, err := c.local.HashCache(ctx, gameID)
	if err != nil {
		return nil, fmt.Errorf("read hash cache: %w", err)
	}
	byPath := make(map[string]HashCacheEntry, len(cached))
	for _, row := range cached {
		byPath[row.Path] = row
	}

	rows := make([]HashCacheEntry, len(rels))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(runtime.NumCPU())
	for i, rel := range rels {
		g.Go(func() error {
			path := filepath.ToSlash(rel)
			info, err := os.Lstat(filepath.Join(root, rel))
			if err != nil {
				return fmt.Errorf("stat %s: %w", path, err)
			}
			mtime := info.ModTime()
			if row, ok := byPath[path]; ok && row.Size == info.Size() && row.MTimeNs == mtime.UnixNano() &&
				mtime.Before(row.HashedAt.Add(-racyWindow)) {
				rows[i] = row
				return nil
			}
			// stat の後に書き換わると、行の mtime は書き換わる前の値になる。次の計算では mtime が一致しないので、計算し直される
			sum, size, err := hashFile(gctx, filepath.Join(root, rel))
			if err != nil {
				return err
			}
			rows[i] = HashCacheEntry{Path: path, Size: size, MTimeNs: mtime.UnixNano(), SHA256: sum, HashedAt: startedAt}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	slices.SortFunc(rows, func(a, b HashCacheEntry) int { return strings.Compare(a.Path, b.Path) })
	if err := c.local.ReplaceHashCache(ctx, gameID, rows); err != nil {
		return nil, fmt.Errorf("write hash cache: %w", err)
	}
	entries := make([]Entry, len(rows))
	for i, row := range rows {
		entries[i] = Entry{Path: row.Path, SHA256: row.SHA256, Size: row.Size}
	}
	return entries, nil
}
