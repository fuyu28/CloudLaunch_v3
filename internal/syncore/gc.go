// 古い世代の削除と GC（仕様書 2 章「世代の保持と GC」・5-P8）。
package syncore

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"
)

const keepGenerations = 10

// collectGarbage は、失敗しても Push を失敗にしないので、エラーを返さずにログに出す（仕様書 5-P8）。
func (c *Core) collectGarbage(ctx context.Context, gameID, pushed string) {
	err := c.gc(ctx, gameID, pushed)
	switch {
	case err == nil:
	case errors.Is(err, context.Canceled):
		c.cfg.Logger.InfoContext(context.WithoutCancel(ctx), "garbage collection canceled", "game", gameID)
	default:
		c.cfg.Logger.WarnContext(context.WithoutCancel(ctx), "garbage collection failed", "game", gameID, "err", err)
	}
}

// gc は、古い世代を削除してから、残った世代のどれからも参照されない object を削除する。
//
// pushed（この Push の世代）と、世代を一覧した後に読んだ HEAD の世代は、新しい 10 個に入らなくても残す。
// Push の後に別の PC が HEAD を書き換えていることがあるため。object は世代より先に一覧する。逆にすると、
// 世代の一覧から object の一覧までの間に別の PC が書いた世代を見落とし、その世代の object を消す時間が長くなる。
//
// 世代を削除しきれなかったときや、残った世代の manifest を読めないときは、object を削除しない。消せなかった世代や
// 読めなかった世代が参照する object を消すと、その世代を Pull できなくなるため。自分が書く形でない名前のキーは、
// 数えも消しもしない。
func (c *Core) gc(ctx context.Context, gameID, pushed string) error {
	objects, err := c.cfg.Remote.List(ctx, objectsPrefix(gameID))
	if err != nil {
		return fmt.Errorf("list objects: %w", err)
	}
	generations, err := c.listGenerations(ctx, gameID)
	if err != nil {
		return err
	}
	head, err := c.readHead(ctx, gameID)
	if err != nil {
		return err
	}

	cut := max(0, len(generations)-keepGenerations)
	var drop, keep []string
	for i, generation := range generations {
		if i < cut && generation != pushed && generation != head {
			drop = append(drop, manifestKey(gameID, generation))
		} else {
			keep = append(keep, generation)
		}
	}
	if err := c.cfg.Remote.DeleteMany(ctx, drop); err != nil {
		return fmt.Errorf("delete old generations: %w", err)
	}

	referenced, err := c.referencedObjects(ctx, gameID, keep)
	if err != nil {
		return err
	}
	var unused []string
	for _, key := range objects {
		name := strings.TrimPrefix(key, objectsPrefix(gameID))
		if isSHA256Hex(name) && !referenced[name] {
			unused = append(unused, key)
		}
	}
	if err := c.cfg.Remote.DeleteMany(ctx, unused); err != nil {
		return fmt.Errorf("delete unused objects: %w", err)
	}
	if len(drop) > 0 || len(unused) > 0 {
		c.cfg.Logger.InfoContext(ctx, "collected garbage", "game", gameID, "generations", len(drop), "objects", len(unused))
	}
	return nil
}

// listGenerations は、世代を古い順に返す。UUIDv7 の文字列の順は、作った時刻の順になる。
func (c *Core) listGenerations(ctx context.Context, gameID string) ([]string, error) {
	prefix := manifestsPrefix(gameID)
	keys, err := c.cfg.Remote.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("list manifests: %w", err)
	}
	generations := []string{}
	for _, key := range keys {
		generation, ok := strings.CutSuffix(strings.TrimPrefix(key, prefix), ".json")
		if ok && generationPattern.MatchString(generation) {
			generations = append(generations, generation)
		}
	}
	sort.Strings(generations)
	return generations, nil
}

// referencedObjects は、generations の manifest を並列に読み、参照されている object の sha256 を返す。
func (c *Core) referencedObjects(ctx context.Context, gameID string, generations []string) (map[string]bool, error) {
	var mu sync.Mutex
	referenced := map[string]bool{}
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(transferLimit)
	for _, generation := range generations {
		g.Go(func() error {
			m, err := c.readManifest(ctx, gameID, generation)
			if err != nil {
				return err
			}
			mu.Lock()
			defer mu.Unlock()
			for _, e := range m.Entries {
				referenced[e.SHA256] = true
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return referenced, nil
}
