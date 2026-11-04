// Push 手順（仕様書 5 章）。
package syncore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sync/errgroup"
)

// ErrPlanStale は、承認した後にセーブフォルダかリモートの状態が変わったことを表す。判定からやり直す。
var ErrPlanStale = errors.New("plan is stale")

const (
	transferLimit = 8
	timeLayout    = "2006-01-02T15:04:05.000Z"
)

// Push は判定が Push の plan だけを受け付ける。手順 8（古い世代の削除と GC）は行わない。
func (c *Core) Push(ctx context.Context, plan Plan) error {
	if plan.Decision != DecisionPush {
		return fmt.Errorf("push %s: decision is %v, not Push", plan.GameID, plan.Decision)
	}
	release, err := c.acquire(ctx)
	if err != nil {
		return fmt.Errorf("push %s: %w", plan.GameID, err)
	}
	defer release()
	game, err := c.local.GameLocal(ctx, plan.GameID)
	if err != nil {
		return fmt.Errorf("read game %s: %w", plan.GameID, err)
	}
	saveFolder := game.SaveFolderPath
	if err := c.ensureGameNotRunning(game.ExePath); err != nil {
		return fmt.Errorf("push %s: %w", plan.GameID, err)
	}

	if err := c.uploadMissing(ctx, plan.GameID, saveFolder, plan.Local.Entries); err != nil {
		return err
	}
	if err := c.ensureGameNotRunning(game.ExePath); err != nil {
		return fmt.Errorf("push %s: %w", plan.GameID, err)
	}
	// アップロード中の検証は、アップロードしたファイルしか読まない。リモートに object があったファイルの変化や、
	// 増えたファイルは、ここでハッシュし直して見つける（仕様書 5-P3）
	if err := ensureLocalUnchanged(ctx, saveFolder, plan); err != nil {
		return err
	}

	generation, err := c.newGeneration()
	if err != nil {
		return err
	}
	body, err := json.Marshal(Manifest{
		Version:    1,
		CreatedAt:  c.now().UTC().Format(timeLayout),
		DeviceName: c.deviceName,
		Entries:    plan.Local.Entries,
	})
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	if err := c.remote.Put(ctx, manifestKey(plan.GameID, generation), bytes.NewReader(body), int64(len(body))); err != nil {
		return err
	}

	// 排他ではなく、他の PC の Push と重なりうる時間を短くするだけである（仕様書 5 章）
	head, err := c.readHead(ctx, plan.GameID)
	if err != nil {
		return err
	}
	if head != plan.Remote.Generation {
		return fmt.Errorf("HEAD changed from %q to %q after approval: %w", plan.Remote.Generation, head, ErrPlanStale)
	}

	// HEAD の PUT を始めた後は、キャンセルされても最後まで進める。リモートに HEAD が書けた後で止めると
	// （応答を待つ間のキャンセルも含む）、成功した Push を失敗と伝える
	ctx = context.WithoutCancel(ctx)
	if err := c.remote.Put(ctx, headKey(plan.GameID), strings.NewReader(generation), int64(len(generation))); err != nil {
		return err
	}
	if err := c.local.SetBase(ctx, plan.GameID, plan.Local.Hash); err != nil {
		return fmt.Errorf("set base: %w", err)
	}
	c.logger.InfoContext(ctx, "pushed", "game", plan.GameID, "generation", generation)
	return nil
}

// uploadMissing は objects/ の一覧にない object だけを、sha256 の重複を除いて並列にアップロードする。
func (c *Core) uploadMissing(ctx context.Context, gameID, root string, entries []Entry) error {
	keys, err := c.remote.List(ctx, objectsPrefix(gameID))
	if err != nil {
		return err
	}
	have := make(map[string]bool, len(keys))
	for _, k := range keys {
		have[k] = true
	}

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(transferLimit)
	for _, e := range entries {
		key := objectKey(gameID, e.SHA256)
		if have[key] {
			continue
		}
		have[key] = true
		g.Go(func() error { return c.uploadObject(ctx, key, root, e) })
	}
	return g.Wait()
}

// uploadObject は、ファイルを全部メモリに読んで manifest と一致することを確かめてから PUT する。
// 読みながら PUT して後で比べると、違ったときに消す前に中断すれば、objects/{sha256} に違う中身が残るため。
func (c *Core) uploadObject(ctx context.Context, key, root string, e Entry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(e.Path)))
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s was removed after approval: %w", e.Path, ErrPlanStale)
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", e.Path, err)
	}
	sum := sha256.Sum256(b)
	if int64(len(b)) != e.Size || hex.EncodeToString(sum[:]) != e.SHA256 {
		return fmt.Errorf("%s changed after approval: %w", e.Path, ErrPlanStale)
	}
	return c.remote.Put(ctx, key, bytes.NewReader(b), int64(len(b)))
}
