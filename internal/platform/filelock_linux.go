// Linux のファイルロック（flock）。開発用の最小限の実装。
package platform

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// TryLock は、ほかの開いたファイルがロックを握っていれば ok に false を返す。flock は開いたファイルごとに
// 効くので、同じプロセスの中でも、別に開いたファイルとは取り合う。
func (l *FileLock) TryLock() (unlock func() error, ok bool, err error) {
	f, err := l.open()
	if err != nil {
		return nil, false, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("flock %s: %w", l.path, err)
	}
	return func() error {
		err := unix.Flock(int(f.Fd()), unix.LOCK_UN)
		return errors.Join(err, f.Close())
	}, true, nil
}
