// Pull 手順（仕様書 6 章）。不正な manifest の検証は validate.go にある。
package syncore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
)

type PullOptions struct {
	// Overwrite が真なら、判定によらず plan を受け付ける（仕様書 7 章の上書き）
	Overwrite bool
	// Progress は、ダウンロードについて、PushOptions.Progress と同じ約束で呼ぶ
	Progress func(Progress)
}

// Pull は、上書き（opts.Overwrite）でなければ、判定が Pull の plan だけを受け付ける。入れ替えを始めた後に失敗した
// ときは、ジャーナルを残してエラーを返す。残った状態は仕様書 6 章の復旧表の行に当たり、Recover が片付ける。
func (c *Core) Pull(ctx context.Context, plan Plan, opts PullOptions) error {
	if err := checkNotDeleted(plan); err != nil {
		return fmt.Errorf("pull %s: %w", plan.GameID, err)
	}
	if err := checkDecision(plan, opts.Overwrite, DecisionPull); err != nil {
		return fmt.Errorf("pull %s: %w", plan.GameID, err)
	}
	if opts.Overwrite && plan.Remote.Hash == "" {
		return fmt.Errorf("pull %s: %w", plan.GameID, ErrRemoteMissing)
	}
	if err := validateManifest(plan.Remote.Manifest.Entries); err != nil {
		return fmt.Errorf("pull %s: invalid manifest %s: %w", plan.GameID, plan.Remote.Generation, err)
	}
	// 手順 9 で見つけると、live を入れ替えた後なので old とジャーナルが残る。何も変える前に止める
	if err := c.checkBackupConfig(); err != nil {
		return fmt.Errorf("pull %s: %w", plan.GameID, err)
	}
	release, err := c.acquire(ctx)
	if err != nil {
		return fmt.Errorf("pull %s: %w", plan.GameID, err)
	}
	defer release()
	game, err := c.cfg.Local.GameLocal(ctx, plan.GameID)
	if err != nil {
		return fmt.Errorf("read game %s: %w", plan.GameID, err)
	}
	live := game.SaveFolderPath
	if err := c.ensureGameNotRunning(game.ExePath); err != nil {
		return fmt.Errorf("pull %s: %w", plan.GameID, err)
	}
	if opts.Overwrite && plan.SameContent() {
		startProgress(ctx, opts.Progress, 0, 0)
		return c.finishSameContent(ctx, plan, live)
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
	startedAt := c.cfg.Now()
	if err := c.cfg.Local.InsertPullOperation(ctx, opID, plan.GameID, live, stage, old, startedAt); err != nil {
		return fmt.Errorf("write pull journal: %w", err)
	}

	if err := c.prepareStage(ctx, plan, game, stage, opts.Progress); err != nil {
		return c.abortPull(ctx, opID, stage, err)
	}

	hadLive, err := exists(live)
	if err != nil {
		return c.abortPull(ctx, opID, stage, err)
	}
	if hadLive {
		if err := c.cfg.Rename(live, old); err != nil {
			err = fmt.Errorf("move save folder to old: %w", err)
			// live が残っていれば、リネームしていないので 6-A1 と同じく片付ける。live がなければ、リネームが済んでから
			// 書き出しに失敗している。ジャーナルを消すとセーブフォルダがないまま復旧されないので、残して 6-R2 に任せる
			if stillLive, statErr := exists(live); statErr == nil && stillLive {
				return c.abortPull(ctx, opID, stage, err)
			}
			return err
		}
	}
	// ここから先で失敗したときは、ジャーナルを残す。残った状態は復旧表の行に当たり、Recover が片付ける
	if err := c.cfg.Rename(stage, live); err != nil {
		return fmt.Errorf("move stage to save folder: %w", err)
	}
	// live を入れ替えた後は、キャンセルされても最後まで進める。途中で止めると、セーブは新しくなったのに
	// Pull が失敗と伝わり、old とジャーナルが残る
	ctx = context.WithoutCancel(ctx)
	if err := c.cfg.Local.SetBase(ctx, plan.GameID, plan.Remote.Hash); err != nil {
		return fmt.Errorf("set base: %w", err)
	}
	if hadLive {
		if err := c.backupOld(ctx, plan.GameID, old, startedAt, opID); err != nil {
			return err
		}
	}
	if err := c.cfg.Local.DeletePullOperation(ctx, opID); err != nil {
		return fmt.Errorf("delete pull journal: %w", err)
	}
	c.cfg.Logger.InfoContext(ctx, "pulled", "game", plan.GameID, "generation", plan.Remote.Generation)
	return nil
}

// prepareStage は手順 4〜6 を行う。
func (c *Core) prepareStage(ctx context.Context, plan Plan, game GameLocal, stage string, progress func(Progress)) error {
	if err := os.Mkdir(stage, 0o755); err != nil {
		return fmt.Errorf("create stage: %w", err)
	}
	// createdAt は表示のためだけに持つ項目なので、読めなくても Pull を止めない（6-F1）
	mtime, err := time.Parse(timeLayout, plan.Remote.Manifest.CreatedAt)
	if err != nil {
		c.cfg.Logger.WarnContext(ctx, "createdAt is not readable; keep mtime of pulled files", "game", plan.GameID, "createdAt", plan.Remote.Manifest.CreatedAt)
		mtime = time.Time{}
	}
	if err := c.buildStage(ctx, plan, game.SaveFolderPath, stage, mtime, progress); err != nil {
		// live からコピーする中身が承認の後に変わると、手順 6 より前に止まる（6-F3）。手順 6 で止まったときと同じく
		// 確認し直せば済むと呼び出し側に伝わるよう、live が変わっていれば ErrPlanStale を足す
		if staleErr := ensureLocalUnchanged(ctx, game.SaveFolderPath, plan); errors.Is(staleErr, ErrPlanStale) {
			return errors.Join(err, staleErr)
		}
		return err
	}

	if err := c.ensureGameNotRunning(game.ExePath); err != nil {
		return fmt.Errorf("pull %s: %w", plan.GameID, err)
	}
	if err := ensureLocalUnchanged(ctx, game.SaveFolderPath, plan); err != nil {
		return err
	}
	return c.ensureRemoteUnchanged(ctx, plan)
}

// abortPull は stage を削除してからジャーナルを削除する。stage を削除できなければ、ジャーナルを残して Recover に任せる。
// キャンセルで中止したときも片付けられるよう、ジャーナルの削除はキャンセルの効かない ctx で行う。
func (c *Core) abortPull(ctx context.Context, opID, stage string, cause error) error {
	if err := os.RemoveAll(stage); err != nil {
		return errors.Join(cause, fmt.Errorf("remove stage: %w", err))
	}
	if err := c.cfg.Local.DeletePullOperation(context.WithoutCancel(ctx), opID); err != nil {
		return errors.Join(cause, fmt.Errorf("delete pull journal: %w", err))
	}
	return cause
}

// fetchEntries は、同じ sha256 のエントリが複数あっても最初の 1 つだけを返す（6-F2）。Changes の Bytes もこれで数え、
// 確認ダイアログの「転送するサイズ」と Pull の転送量を一致させる。
func fetchEntries(remote, local []Entry) []Entry {
	have := make(map[string]bool, len(local))
	for _, e := range local {
		have[e.SHA256] = true
	}
	var fetch []Entry
	for _, e := range remote {
		if have[e.SHA256] {
			continue
		}
		have[e.SHA256] = true
		fetch = append(fetch, e)
	}
	return fetch
}

// buildStage は手順 4・5 を行う。fetchEntries の分を取得してから、残りのエントリを、承認した時点の local で同じ sha256
// だった live のファイルか、先に取得した stage のファイルからコピーする（6-F2）。コピーの元が承認の後に書き換わっていても、
// コピーの途中では確かめず、手順 5 の検証に任せる（6-F3）。
func (c *Core) buildStage(ctx context.Context, plan Plan, live, stage string, mtime time.Time, progress func(Progress)) error {
	fetch := fetchEntries(plan.Remote.Manifest.Entries, plan.Local.Entries)
	if err := c.downloadAll(ctx, plan.GameID, stage, fetch, mtime, progress); err != nil {
		return err
	}

	sources := make(map[string]string, len(plan.Local.Entries)+len(fetch))
	for _, e := range plan.Local.Entries {
		if _, ok := sources[e.SHA256]; !ok {
			sources[e.SHA256] = filepath.Join(live, filepath.FromSlash(e.Path))
		}
	}
	fetched := make(map[string]bool, len(fetch))
	for _, e := range fetch {
		sources[e.SHA256] = filepath.Join(stage, filepath.FromSlash(e.Path))
		fetched[e.Path] = true
	}
	for _, e := range plan.Remote.Manifest.Entries {
		if fetched[e.Path] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := c.copyToStage(sources[e.SHA256], filepath.Join(stage, filepath.FromSlash(e.Path)), mtime); err != nil {
			return err
		}
	}

	staged, _, err := localEntries(ctx, stage)
	if err != nil {
		return err
	}
	if ManifestHash(staged) != plan.Remote.Hash {
		return fmt.Errorf("staged files do not match manifest %s", plan.Remote.Generation)
	}
	return nil
}

// copyToStage は、バックアップの copyFile と違い、コピーの元の mtime を保たない。
func (c *Core) copyToStage(src, dst string, mtime time.Time) error {
	f, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open copy source: %w", err)
	}
	defer func() { _ = f.Close() }()
	return c.writeStageFile(dst, f, mtime)
}

// writeStageFile は、取得とコピーで mtime（6-F1）と書き出しの扱いが食い違わないよう、stage への書き込みをまとめる。
// mtime がゼロ値なら合わせない。
func (c *Core) writeStageFile(dst string, r io.Reader, mtime time.Time) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create directory in stage: %w", err)
	}
	if err := c.writeFileSyncedAt(dst, r, mtime); err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}
	return nil
}

func (c *Core) downloadAll(ctx context.Context, gameID, stage string, entries []Entry, mtime time.Time, progress func(Progress)) error {
	var size int64
	for _, e := range entries {
		size += e.Size
	}
	reporter := startProgress(ctx, progress, len(entries), size)
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(transferLimit)
	for _, e := range entries {
		dst := filepath.Join(stage, filepath.FromSlash(e.Path))
		g.Go(func() error {
			if err := c.downloadObject(ctx, objectKey(gameID, e.SHA256), dst, mtime); err != nil {
				reporter.fail()
				return err
			}
			reporter.done(e.Size)
			return nil
		})
	}
	return g.Wait()
}

func (c *Core) downloadObject(ctx context.Context, key, dst string, mtime time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r, err := c.cfg.Remote.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("download object: %w", err)
	}
	defer func() { _ = r.Close() }()
	return c.writeStageFile(dst, r, mtime)
}
