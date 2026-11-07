// 上書き（仕様書 7 章）。判定によらず Push・Pull を行うときの入口の確かめ。
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

// acceptPlan は、Push・Pull が plan を受け付けるかを確かめる。sourceHash は転送元（Push なら local、Pull なら
// remote）のハッシュで、上書きのときに空なら errSourceMissing を返す。done が真なら、local と remote が同じなので、
// 転送せずに成功とする（必要なら base を記録した後）。
func (c *Core) acceptPlan(ctx context.Context, plan Plan, overwrite bool, want Decision, sourceHash string, errSourceMissing error) (done bool, err error) {
	if !overwrite {
		if plan.Decision != want {
			return false, fmt.Errorf("decision is %v, not %v", plan.Decision, want)
		}
		return false, nil
	}
	if sourceHash == "" {
		return false, errSourceMissing
	}
	// 中身が同じなら、転送しても結果が変わらない（仕様書 7-O5）
	if plan.Local.Hash != plan.Remote.Hash {
		return false, nil
	}
	if plan.RecordBase {
		if err := c.RecordBase(ctx, plan); err != nil {
			return true, err
		}
	}
	return true, nil
}
