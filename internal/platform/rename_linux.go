// Linux の、ディスクへ書き出すリネーム（仕様書 3 章「ディスクへの書き出し」）。開発用の最小限の実装。
package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Rename は、リネームした後に、移した元と先の親ディレクトリを fsync してから戻る。移す先があれば失敗する。
// rename(2) は移す先のファイルや空のディレクトリを黙って置き換えるので、RENAME_NOREPLACE を付ける。
func Rename(oldpath, newpath string) error {
	if err := unix.Renameat2(unix.AT_FDCWD, oldpath, unix.AT_FDCWD, newpath, unix.RENAME_NOREPLACE); err != nil {
		return fmt.Errorf("rename %s to %s: %w", oldpath, newpath, err)
	}
	oldDir, newDir := filepath.Dir(oldpath), filepath.Dir(newpath)
	err := syncDir(newDir)
	if oldDir != newDir {
		err = errors.Join(err, syncDir(oldDir))
	}
	return err
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open %s: %w", dir, err)
	}
	err = f.Sync()
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("sync %s: %w", dir, err)
	}
	return nil
}
