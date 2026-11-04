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
		if matchesExe(exePath, p) {
			return true, nil
		}
	}
	return false, nil
}

// matchesExe は、ファイル名が一致することを前提に、パスが完全に一致すれば真を返す。パスが読めないプロセスは、
// ファイル名だけで真にする。管理者で動くゲームを見落とさないためで、誤って真にしても同期を見送るだけで済む。
// 前身の版の「含む」「フォルダを含む」の照合は、外部コマンドの出力の文字化けへの手当てだったので引き継がない。
func matchesExe(exePath string, p processEntry) bool {
	exe := normalizePath(exePath)
	if normalizePath(p.name) != baseName(exe) {
		return false
	}
	if p.path == "" {
		return true
	}
	return normalizePath(p.path) == exe
}

// normalizePath は、区切りを「\」に、大文字を小文字にそろえ、NFC に正規化する。登録したパス（利用者が入力したもの）と
// プロセスのパスで、濁点を分けて表す（NFD）かどうかが違っても一致させるため。
func normalizePath(p string) string {
	return norm.NFC.String(strings.ToLower(strings.ReplaceAll(p, "/", `\`)))
}

func baseName(normalized string) string {
	return normalized[strings.LastIndex(normalized, `\`)+1:]
}
