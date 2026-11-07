// 承認した後の確かめ（仕様書 7 章）。Push・Pull・上書きが、承認した時点から local と HEAD が変わっていないかを確かめる。
package syncore

import (
	"context"
	"errors"
	"fmt"
)

// ErrPlanStale は、承認した後にセーブフォルダかリモートの状態が変わったことを表す。判定からやり直す。
var ErrPlanStale = errors.New("plan is stale")

// ensureLocalUnchanged は、Push（5-P3）と Pull（6-L6）が、承認の後にセーブフォルダが変わっていないかを確かめる（仕様書 7 章）。
// DB の base ではなく plan.Base を使う。plan.Local.Hash は plan.Base から計算したもので、同じ base で計算し直さないと比べられない
func ensureLocalUnchanged(ctx context.Context, folder string, plan Plan) error {
	entries, err := localEntries(ctx, folder)
	if err != nil {
		return err
	}
	if localHash(entries, plan.Base) != plan.Local.Hash {
		return fmt.Errorf("save folder changed after approval: %w", ErrPlanStale)
	}
	return nil
}

// ensureHeadUnchanged は、Push（5-P5）・Pull（6-L6）・上書きで同じとき（7-O5）が、承認の後に HEAD が変わっていないかを確かめる。
func (c *Core) ensureHeadUnchanged(ctx context.Context, plan Plan) error {
	head, err := c.readHead(ctx, plan.GameID)
	if err != nil {
		return err
	}
	if head != plan.Remote.Generation {
		return fmt.Errorf("HEAD changed from %q to %q after approval: %w", plan.Remote.Generation, head, ErrPlanStale)
	}
	return nil
}
