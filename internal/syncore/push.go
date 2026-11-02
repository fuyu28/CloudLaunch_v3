// Push 手順（仕様書 5 章）。
package syncore

import (
	"context"
	"errors"
)

// ErrPlanStale は、承認した後にセーブフォルダかリモートの状態が変わったことを表す。判定からやり直す。
var ErrPlanStale = errors.New("plan is stale")

func (c *Core) Push(ctx context.Context, plan Plan) error {
	return errors.New("push is not implemented yet")
}
