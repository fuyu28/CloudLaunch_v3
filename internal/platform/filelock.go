// OS のファイルロック。PC 全体の同期ロック（仕様書 3 章「排他」）に使う。ロックそのものは OS ごとのファイルにある。
package platform

import (
	"fmt"
	"os"
	"path/filepath"
)

// FileLock は、path のファイルに対する、待たずに失敗する排他ロック。プロセスが異常終了すれば OS が外す。
type FileLock struct {
	path string
}

// NewFileLock はファイルを開かない。ファイルとその親ディレクトリは、TryLock が必要なら作る。
func NewFileLock(path string) *FileLock {
	return &FileLock{path: path}
}

func (l *FileLock) open() (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return nil, fmt.Errorf("create directory of lock file: %w", err)
	}
	f, err := os.OpenFile(l.path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	return f, nil
}
