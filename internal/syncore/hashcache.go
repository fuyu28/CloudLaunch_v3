// ハッシュキャッシュ（仕様書 3 章）と、セーブフォルダのファイルの並列のハッシュ計算。
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

func (e HashCacheEntry) equal(o HashCacheEntry) bool {
	return e.Path == o.Path && e.Size == o.Size && e.MTimeNs == o.MTimeNs && e.SHA256 == o.SHA256 && e.HashedAt.Equal(o.HashedAt)
}

// ForgetHashes は、ゲームのハッシュキャッシュを消す。次の計算は、すべてのファイルを読む。size と mtime を保ったまま
// 中身が変わったファイルを、キャッシュを使う限り見落とすため、計算し直させる手段として使う（仕様書 3 章）。
func (c *Core) ForgetHashes(ctx context.Context, gameID string) error {
	if err := c.local.DeleteHashCache(ctx, gameID); err != nil {
		return fmt.Errorf("forget hashes of %s: %w", gameID, err)
	}
	return nil
}

// dropHashCache は、キャッシュが古いかもしれないと分かったときに消す。キャッシュはなくても判定できるので、
// 失敗してもログに出すだけにする。
func (c *Core) dropHashCache(ctx context.Context, gameID string) {
	ctx = context.WithoutCancel(ctx)
	if err := c.ForgetHashes(ctx, gameID); err != nil {
		c.logger.WarnContext(ctx, "hash cache could not be dropped", "game", gameID, "err", err)
	}
}

// saveFolderEntries は、判定と承認後の確かめが使う、ゲームのセーブフォルダの entries を path の昇順で返す。
// size と mtime がキャッシュと一致し、mtime がキャッシュの計算を始めた時刻より racyWindow 以上古いファイルは、
// 読まずにキャッシュの sha256 を使う。終わったら、ゲームのキャッシュを今あるファイルの分だけで置き換える。
// キャッシュはなくても判定できるので、読めなければすべてのファイルを読み、書けなければログに出して続ける。
func (c *Core) saveFolderEntries(ctx context.Context, gameID, root string) ([]Entry, error) {
	startedAt := c.now()
	rels, err := listFiles(root)
	if err != nil {
		return nil, err
	}
	cached, err := c.local.HashCache(ctx, gameID)
	if err != nil {
		c.logger.WarnContext(ctx, "hash cache could not be read; hashing all files", "game", gameID, "err", err)
		cached = nil
	}
	byPath := make(map[string]HashCacheEntry, len(cached))
	for _, row := range cached {
		byPath[row.Path] = row
	}

	rows, err := hashFiles(ctx, root, rels, byPath, startedAt)
	if err != nil {
		return nil, err
	}
	if !slices.EqualFunc(rows, cached, HashCacheEntry.equal) {
		if err := c.local.ReplaceHashCache(ctx, gameID, rows); err != nil {
			c.logger.WarnContext(ctx, "hash cache could not be written", "game", gameID, "err", err)
		}
	}
	return entriesOfRows(rows), nil
}

// localEntries は root 以下の通常のファイルを、キャッシュを使わずにハッシュして path の昇順で返す。root がなければ空を返す。
func localEntries(ctx context.Context, root string) ([]Entry, error) {
	rels, err := listFiles(root)
	if err != nil {
		return nil, err
	}
	rows, err := hashFiles(ctx, root, rels, nil, time.Time{})
	if err != nil {
		return nil, err
	}
	return entriesOfRows(rows), nil
}

// hashFiles は root の下の rels を並列にハッシュし、区切りを / にした path のバイト列の順で返す。cached に、size と
// mtime が一致し、mtime が計算を始めた時刻より racyWindow 以上古い行があれば、読まずにその行を使う。
// WalkDir はディレクトリごとに名前で並べるので、"a/b" が "a.sav" より先に来る。そのため並べ直す。
func hashFiles(ctx context.Context, root string, rels []string, cached map[string]HashCacheEntry, startedAt time.Time) ([]HashCacheEntry, error) {
	rows := make([]HashCacheEntry, len(rels))
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(runtime.NumCPU())
	for i, rel := range rels {
		g.Go(func() error {
			path := filepath.ToSlash(rel)
			info, err := os.Lstat(filepath.Join(root, rel))
			if err != nil {
				return fmt.Errorf("stat %s: %w", path, err)
			}
			mtime := info.ModTime()
			if row, ok := cached[path]; ok && row.Size == info.Size() && row.MTimeNs == mtime.UnixNano() &&
				mtime.Before(row.HashedAt.Add(-racyWindow)) {
				rows[i] = row
				return nil
			}
			// stat の後に書き換わると、行の mtime は書き換わる前の値になる。次の計算では mtime が一致しないので、計算し直される
			sum, size, err := hashFile(ctx, filepath.Join(root, rel))
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
	return rows, nil
}

func entriesOfRows(rows []HashCacheEntry) []Entry {
	entries := make([]Entry, len(rows))
	for i, row := range rows {
		entries[i] = Entry{Path: row.Path, SHA256: row.SHA256, Size: row.Size}
	}
	return entries
}
