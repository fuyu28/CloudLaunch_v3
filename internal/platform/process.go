// ゲームの実行中の確認（仕様書 7 章）。プロセスの列挙は OS ごとのファイルにあり、照合はここに置く。
package platform

// processEntry は実行中のプロセス 1 つ。path は、プロセスを開けずに実行ファイルのパスを読めなければ空。
type processEntry struct {
	name string
	path string
}

// Processes は、登録した実行ファイルのパスでゲームのプロセスを見つける。
type Processes struct{}

func matchesExe(exePath string, p processEntry) bool {
	return false
}
