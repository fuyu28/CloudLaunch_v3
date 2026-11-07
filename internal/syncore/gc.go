// 古い世代の削除と GC（仕様書 2 章「世代の保持と GC」・5-P8）。
package syncore

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/sync/errgroup"
)

const keepGenerations = 10

var objectNamePattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// collectGarbage は、失敗しても Push を失敗にしないので、エラーを返さずにログに出す（仕様書 5-P8）。
func (c *Core) collectGarbage(ctx context.Context, gameID, head string) {
	if err := c.gc(ctx, gameID, head); err != nil {
		c.logger.WarnContext(ctx, "garbage collection failed", "game", gameID, "err", err)
	}
}

// gc は、古い世代を削除してから、残った世代のどれからも参照されない object を削除する。世代を削除しきれなかったときや、
// 残った世代の manifest を読めないときは、object を削除しない。消せなかった世代や読めなかった世代が参照する
// object を消すと、その世代を Pull できなくなるため。自分が書く形でない名前のキーは、数えも消しもしない。
func (c *Core) gc(ctx context.Context, gameID, head string) error {
	prefix := manifestsPrefix(gameID)
	keys, err := c.remote.List(ctx, prefix)
	if err != nil {
		return fmt.Errorf("list manifests: %w", err)
	}
	generations := []string{}
	for _, key := range keys {
		generation, ok := strings.CutSuffix(strings.TrimPrefix(key, prefix), ".json")
		if ok && generationPattern.MatchString(generation) {
			generations = append(generations, generation)
		}
	}
	// UUIDv7 の文字列の順は、作った時刻の順になる
	sort.Strings(generations)
	cut := max(0, len(generations)-keepGenerations)
	var drop, keep []string
	for i, generation := range generations {
		if i < cut && generation != head {
			drop = append(drop, manifestKey(gameID, generation))
		} else {
			keep = append(keep, generation)
		}
	}
	if err := c.deleteAll(ctx, drop); err != nil {
		return fmt.Errorf("delete old generations: %w", err)
	}

	referenced := map[string]bool{}
	for _, generation := range keep {
		m, err := c.readManifest(ctx, gameID, generation)
		if err != nil {
			return err
		}
		for _, e := range m.Entries {
			referenced[e.SHA256] = true
		}
	}

	objects, err := c.remote.List(ctx, objectsPrefix(gameID))
	if err != nil {
		return fmt.Errorf("list objects: %w", err)
	}
	var unused []string
	for _, key := range objects {
		name := strings.TrimPrefix(key, objectsPrefix(gameID))
		if objectNamePattern.MatchString(name) && !referenced[name] {
			unused = append(unused, key)
		}
	}
	if err := c.deleteAll(ctx, unused); err != nil {
		return fmt.Errorf("delete unused objects: %w", err)
	}
	if len(drop) > 0 || len(unused) > 0 {
		c.logger.InfoContext(ctx, "collected garbage", "game", gameID, "generations", len(drop), "objects", len(unused))
	}
	return nil
}

func (c *Core) deleteAll(ctx context.Context, keys []string) error {
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(transferLimit)
	for _, key := range keys {
		g.Go(func() error {
			// キャンセルされた後は、待っていた削除を始めない
			if err := ctx.Err(); err != nil {
				return err
			}
			return c.remote.Delete(ctx, key)
		})
	}
	return g.Wait()
}
