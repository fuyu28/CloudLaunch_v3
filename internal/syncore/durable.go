// ディスクへの書き出し（仕様書 3 章）。Pull の stage への取得と、バックアップへのコピーが使う。
package syncore

import (
	"io"
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
