// ローカルのファイルの操作のうち、複数の手順が使うもの。ディスクへの書き出し（仕様書 3 章）と、パスがあるかの確かめ。
package syncore

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
)

// writeFileSynced は、dst を新しく作って r の中身を書き、ディスクへ書き出してから閉じる。dst が既にあれば失敗する。
func (c *Core) writeFileSynced(dst string, r io.Reader) error {
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, r)
	if err == nil {
		err = c.cfg.SyncFile(f)
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}

// exists は、存在しない以外のエラーを、存在しないとみなさずに包んで返す（仕様書 6 章で、パスの状態を確かめられない
// ときは行を残してエラーにするため）。
func exists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat %s: %w", path, err)
	}
	return true, nil
}
