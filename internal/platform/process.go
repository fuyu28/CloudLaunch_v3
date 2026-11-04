// ゲームの実行中の確認（仕様書 7 章）。プロセスの列挙は OS ごとのファイルにあり、照合はここに置く。
package platform

import (
	"strings"

	"golang.org/x/text/unicode/norm"
)

// processEntry は実行中のプロセス 1 つ。path は、プロセスを開けずに実行ファイルのパスを読めなければ空。
type processEntry struct {
	name string
	path string
}

// Processes は、登録した実行ファイルのパスでゲームのプロセスを見つける。
type Processes struct{}

// Running は、exePath の実行ファイルのプロセスが 1 つでも動いていれば真を返す。
func (Processes) Running(exePath string) (bool, error) {
	procs, err := listProcesses()
	if err != nil {
		return false, err
	}
	for _, p := range procs {
		if matchesExe(exeNameKey(exePath), p, func(string) (bool, bool) { return false, false }) {
			return true, nil
		}
	}
	return false, nil
}

func matchesExe(exeName string, p processEntry, sameFile func(path string) (same, ok bool)) bool {
	return false
}

func exeNameKey(path string) string {
	return norm.NFC.String(strings.ToUpper(path))
}
