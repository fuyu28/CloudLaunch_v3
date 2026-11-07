// Windows の、ディスクへ書き出すリネーム（仕様書 3 章「ディスクへの書き出し」）。
package platform

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// Rename は、リネームがディスクに届くまで戻らない。移す先があれば失敗する。
// 親ディレクトリを FlushFileBuffers で書き出す方法は、ディレクトリのハンドルで効くかの文書がないので採らず、
// MOVEFILE_WRITE_THROUGH に任せる。MOVEFILE_REPLACE_EXISTING を付けないのは、移す先があれば
// 何かが食い違っているので、上書きせずに止めるため。
func Rename(oldpath, newpath string) error {
	from, err := windows.UTF16PtrFromString(oldpath)
	if err != nil {
		return fmt.Errorf("rename %s: %w", oldpath, err)
	}
	to, err := windows.UTF16PtrFromString(newpath)
	if err != nil {
		return fmt.Errorf("rename %s: %w", newpath, err)
	}
	if err := windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return fmt.Errorf("rename %s to %s: %w", oldpath, newpath, err)
	}
	return nil
}
