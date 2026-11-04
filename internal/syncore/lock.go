// PC 全体の同期ロック（仕様書 3 章「排他」）。
package syncore

import (
	"context"
	"errors"
	"fmt"
)

// ErrSyncInProgress は、PC 全体の同期ロックが取れなかったことを表す。
var ErrSyncInProgress = errors.New("another sync is in progress")

// Locker は、待たずに失敗する排他ロック。取れなかったときは ok に false、err に nil を返す。
type Locker interface {
	TryLock() (unlock func() error, ok bool, err error)
}

// acquire は、Push・Pull・Recover が何かを変える前に呼ぶ。返した release は、成功しても失敗しても呼ぶ。
// Locker がなければエラーにし、ロックなしで黙って動く道を作らない。
func (c *Core) acquire(ctx context.Context) (release func(), err error) {
	if c.locker == nil {
		return nil, errors.New("sync lock is not configured")
	}
	unlock, ok, err := c.locker.TryLock()
	if err != nil {
		return nil, fmt.Errorf("take sync lock: %w", err)
	}
	if !ok {
		return nil, ErrSyncInProgress
	}
	return func() {
		// 同期はもう終わっていて、プロセスが終われば OS がロックを外すので、結果は変えない（仕様書 3 章）
		if err := unlock(); err != nil {
			c.logger.WarnContext(ctx, "could not release sync lock", "error", err)
		}
	}, nil
}
