// 上書き（仕様書 7 章）。判定によらず Push・Pull を行うときの入口の確かめと、local と remote が同じときの扱い。
package syncore

import (
	"context"
	"errors"
	"fmt"
)

var (
	// ErrLocalMissing は、local にセーブがないので、アップロードで上書きできないことを表す（仕様書 7-O3）
	ErrLocalMissing = errors.New("local save is missing")
	// ErrRemoteMissing は、remote にセーブがないので、ダウンロードで上書きできないことを表す（仕様書 7-O4）
	ErrRemoteMissing = errors.New("remote save is missing")
)

// PushOptions の Overwrite が真なら、判定によらず plan を受け付ける。偽なら、判定が Push の plan だけを受け付ける。
type PushOptions struct {
	Overwrite bool
}

// PullOptions の Overwrite が真なら、判定によらず plan を受け付ける。偽なら、判定が Pull の plan だけを受け付ける。
type PullOptions struct {
	Overwrite bool
}

// SameContent は、local と remote が同じことを表す。上書きでも、このときは転送しない（仕様書 7-O5）。
func (p Plan) SameContent() bool {
	return p.Local.Hash != "" && p.Local.Hash == p.Remote.Hash
}

func checkDecision(plan Plan, overwrite bool, want Decision) error {
	if overwrite || plan.Decision == want {
		return nil
	}
	return fmt.Errorf("decision is %s, not %s", plan.Decision, want)
}

// finishSameContent は、上書きで local と remote が同じときに、転送せずに base を記録する（仕様書 7-O5）。
// 転送しなくても、承認した後に local か HEAD が変わっていれば ErrPlanStale を返す。利用者が見た「同じ」が
// もう正しくなく、頼んだ上書きが行われないまま成功と伝わるのを防ぐため。同期ロックを取った後に呼ぶ。
func (c *Core) finishSameContent(ctx context.Context, plan Plan, saveFolder string) error {
	if err := ensureLocalUnchanged(ctx, saveFolder, plan); err != nil {
		return err
	}
	if err := c.ensureRemoteUnchanged(ctx, plan); err != nil {
		return err
	}
	if !plan.RecordBase {
		return nil
	}
	if err := c.cfg.Local.SetBase(ctx, plan.GameID, plan.Local.Hash); err != nil {
		return fmt.Errorf("set base: %w", err)
	}
	return nil
}
