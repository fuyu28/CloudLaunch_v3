// OS のファイルロック。PC 全体の同期ロック（仕様書 3 章「排他」）に使う。実装は OS ごとのファイルにある。
package platform

// FileLock は、path のファイルに対する、待たずに失敗する排他ロック。プロセスが異常終了すれば OS が外す。
type FileLock struct {
	path string
}

// NewFileLock はファイルを開かない。ファイルとその親ディレクトリは、TryLock が必要なら作る。
func NewFileLock(path string) *FileLock {
	return &FileLock{path: path}
}
