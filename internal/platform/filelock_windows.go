// Windows のファイルロック（LockFileEx）。
package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// TryLock は、ほかのハンドルがロックを握っていれば ok に false を返す。LockFileEx はハンドルごとに
// 効くので、同じプロセスの中でも、別に開いたハンドルとは取り合う。
func (l *FileLock) TryLock() (unlock func() error, ok bool, err error) {
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return nil, false, fmt.Errorf("create directory of lock file: %w", err)
	}
	f, err := os.OpenFile(l.path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, false, fmt.Errorf("open lock file: %w", err)
	}
	h := windows.Handle(f.Fd())
	// ファイルの先頭 1 バイトをロックする。中身は書かないので、ロックする範囲はどこでもよい
	ol := new(windows.Overlapped)
	if err := windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol); err != nil {
		_ = f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("LockFileEx %s: %w", l.path, err)
	}
	return func() error {
		err := windows.UnlockFileEx(h, 0, 1, 0, ol)
		return errors.Join(err, f.Close())
	}, true, nil
}
