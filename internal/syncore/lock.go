// PC 全体の同期ロック（仕様書 3 章「排他」）。
package syncore

import "errors"

// ErrSyncInProgress は、PC 全体の同期ロックが取れなかったことを表す。
var ErrSyncInProgress = errors.New("another sync is in progress")

// Locker は、待たずに失敗する排他ロック。取れなかったときは ok に false、err に nil を返す。
type Locker interface {
	TryLock() (unlock func() error, ok bool, err error)
}
