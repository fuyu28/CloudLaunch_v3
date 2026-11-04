// Pull 手順（仕様書 6 章）と、不正な manifest の検証。
package syncore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
)

var drivePattern = regexp.MustCompile(`[A-Za-z]:`)

// Pull は判定が Pull の plan だけを受け付ける。入れ替えを始めた後に失敗したときは、ジャーナルを残してエラーを返す。
// 残った状態は仕様書 6 章の復旧表の行に当たり、Recover が片付ける。
func (c *Core) Pull(ctx context.Context, plan Plan) error {
	if plan.Decision != DecisionPull {
		return fmt.Errorf("pull %s: decision is %v, not Pull", plan.GameID, plan.Decision)
	}
	if err := validateEntries(plan.Remote.Manifest.Entries); err != nil {
		return fmt.Errorf("pull %s: invalid manifest %s: %w", plan.GameID, plan.Remote.Generation, err)
	}
	live, _, err := c.local.GameLocal(ctx, plan.GameID)
	if err != nil {
		return fmt.Errorf("read game %s: %w", plan.GameID, err)
	}

	// stage と old は、リネームで入れ替えられるよう live と同じディレクトリに置く（仕様書 3 章）
	parent := filepath.Dir(live)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create parent of save folder: %w", err)
	}
	u, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("generate op id: %w", err)
	}
	opID := u.String()
	stage := filepath.Join(parent, ".cloudlaunch-stage-"+opID)
	old := filepath.Join(parent, ".cloudlaunch-old-"+opID)
	startedAt := c.now()
	if err := c.local.InsertPullOperation(ctx, opID, plan.GameID, live, stage, old, startedAt); err != nil {
		return fmt.Errorf("write pull journal: %w", err)
	}

	if err := c.prepareStage(ctx, plan, live, stage); err != nil {
		return c.abortPull(ctx, opID, stage, err)
	}

	hadLive := true
	if _, err := os.Lstat(live); errors.Is(err, fs.ErrNotExist) {
		hadLive = false
	} else if err != nil {
		return c.abortPull(ctx, opID, stage, fmt.Errorf("stat save folder: %w", err))
	}
	if hadLive {
		if err := c.rename(live, old); err != nil {
			return c.abortPull(ctx, opID, stage, fmt.Errorf("move save folder to old: %w", err))
		}
	}
	// ここから先で失敗したときは、ジャーナルを残す。残った状態は復旧表の行に当たり、Recover が片付ける
	if err := c.rename(stage, live); err != nil {
		return fmt.Errorf("move stage to save folder: %w", err)
	}
	if err := c.local.SetBase(ctx, plan.GameID, plan.Remote.Hash); err != nil {
		return fmt.Errorf("set base: %w", err)
	}
	if hadLive {
		if err := c.moveToBackup(plan.GameID, old, backupName(startedAt, opID)); err != nil {
			return fmt.Errorf("move old save folder to backup: %w", err)
		}
		c.pruneBackups(ctx, plan.GameID)
	}
	if err := c.local.DeletePullOperation(ctx, opID); err != nil {
		return fmt.Errorf("delete pull journal: %w", err)
	}
	c.logger.InfoContext(ctx, "pulled", "game", plan.GameID, "generation", plan.Remote.Generation)
	return nil
}

// prepareStage は手順 4〜6 を行う。
func (c *Core) prepareStage(ctx context.Context, plan Plan, live, stage string) error {
	if err := os.Mkdir(stage, 0o755); err != nil {
		return fmt.Errorf("create stage: %w", err)
	}
	if err := c.downloadAll(ctx, plan.GameID, stage, plan.Remote.Manifest.Entries); err != nil {
		return err
	}

	staged, err := localEntries(ctx, stage)
	if err != nil {
		return err
	}
	if ManifestHash(staged) != plan.Remote.Hash {
		return fmt.Errorf("downloaded files do not match manifest %s", plan.Remote.Generation)
	}

	liveEntries, err := localEntries(ctx, live)
	if err != nil {
		return err
	}
	// DB の base ではなく plan.Base を使う。plan.Local.Hash は plan.Base から計算したもので、同じ base で計算し直さないと比べられない
	if localHash(liveEntries, plan.Base) != plan.Local.Hash {
		return fmt.Errorf("save folder changed after approval: %w", ErrPlanStale)
	}
	head, err := c.readHead(ctx, plan.GameID)
	if err != nil {
		return err
	}
	if head != plan.Remote.Generation {
		return fmt.Errorf("HEAD changed from %q to %q after approval: %w", plan.Remote.Generation, head, ErrPlanStale)
	}
	return nil
}

// abortPull は stage を削除してからジャーナルを削除する。stage を削除できなければ、ジャーナルを残して Recover に任せる。
func (c *Core) abortPull(ctx context.Context, opID, stage string, cause error) error {
	if err := os.RemoveAll(stage); err != nil {
		return errors.Join(cause, fmt.Errorf("remove stage: %w", err))
	}
	if err := c.local.DeletePullOperation(ctx, opID); err != nil {
		return errors.Join(cause, fmt.Errorf("delete pull journal: %w", err))
	}
	return cause
}

func (c *Core) downloadAll(ctx context.Context, gameID, stage string, entries []Entry) error {
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(transferLimit)
	for _, e := range entries {
		dst := filepath.Join(stage, filepath.FromSlash(e.Path))
		g.Go(func() error { return c.downloadObject(ctx, objectKey(gameID, e.SHA256), dst) })
	}
	return g.Wait()
}

func (c *Core) downloadObject(ctx context.Context, key, dst string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create directory in stage: %w", err)
	}
	r, err := c.remote.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("download object: %w", err)
	}
	defer func() { _ = r.Close() }()
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("create file in stage: %w", err)
	}
	_, err = io.Copy(f, r)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}
	return nil
}

// validateEntries は、仕様書 6 章「不正な manifest」の規則に当たる path があればエラーを返す。
func validateEntries(entries []Entry) error {
	seen := make(map[string]string, len(entries))
	for _, e := range entries {
		if err := validatePath(e.Path); err != nil {
			return fmt.Errorf("path %q: %w", e.Path, err)
		}
		// Windows はファイル名を大文字に揃えて比べるので、ToLower ではなく ToUpper で揃える
		folded := strings.ToUpper(e.Path)
		if other, ok := seen[folded]; ok {
			return fmt.Errorf("paths %q and %q differ only in case", other, e.Path)
		}
		seen[folded] = e.Path
	}
	return nil
}

func validatePath(p string) error {
	switch {
	case p == "":
		return errors.New("empty")
	case strings.HasPrefix(p, "/"):
		return errors.New("absolute")
	case strings.Contains(p, `\`):
		return errors.New(`contains \`)
	case drivePattern.MatchString(p):
		return errors.New("contains a drive letter")
	}
	for _, elem := range strings.Split(p, "/") {
		if elem == "" || elem == "." || elem == ".." {
			return fmt.Errorf("has element %q", elem)
		}
	}
	return nil
}
