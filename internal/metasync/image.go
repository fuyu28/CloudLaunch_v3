// ゲームの画像の同期とキャッシュ（仕様書 8 章「画像」）。
package metasync

import (
	"errors"
	"io"
)

func (c *Core) CacheImage(r io.Reader) (string, error) {
	return "", errors.New("not implemented")
}
