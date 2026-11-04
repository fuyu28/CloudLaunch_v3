// ゲームの実行中の確認（仕様書 7 章）。プロセスの列挙は OS ごとのファイルにあり、照合はここに置く。
package platform

import (
	"fmt"
	"os"
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

// Running は、exePath の実行ファイルのプロセスが 1 つでも動いていれば真を返す。exePath がなければエラーを返す。
func (Processes) Running(exePath string) (bool, error) {
	exeInfo, err := os.Stat(exePath)
	if err != nil {
		return false, fmt.Errorf("stat game executable: %w", err)
	}
	procs, err := listProcesses()
	if err != nil {
		return false, err
	}
	sameFile := func(path string) (same, ok bool) {
		info, err := os.Stat(path)
		if err != nil {
			return false, false
		}
		return os.SameFile(exeInfo, info), true
	}
	name := exeNameKey(exePath)
	for _, p := range procs {
		if matchesExe(name, p, sameFile) {
			return true, nil
		}
	}
	return false, nil
}

// matchesExe は、プロセスのパスが登録した実行ファイルと同じファイルを指せば真を返す。パスの文字列ではなく
// ファイルの同一性で比べるのは、ジャンクション・subst・短い名前（PROGRA~2）のように、同じファイルを別の書き方で
// 指すときにも見落とさないため。パスが取れないか Stat できないプロセスは、ファイル名だけで真にする。管理者で動く
// ゲームを見落とさないためで、誤って真にしても同期を見送るだけで済む。
// 前身の版の「含む」「フォルダを含む」の照合は、外部コマンドの出力の文字化けへの手当てだったので引き継がない。
func matchesExe(exeName string, p processEntry, sameFile func(path string) (same, ok bool)) bool {
	if p.path != "" {
		if same, ok := sameFile(p.path); ok {
			return same
		}
	}
	return exeNameKey(p.name) == exeName
}

// exeNameKey は、パスのファイル名を、名前だけで比べるための形にする。大文字にそろえるのは Windows のファイル名の
// 比べ方に合わせるため（validateEntries と同じ）、NFC にするのは濁点の表し方の違いを吸収するため。
func exeNameKey(path string) string {
	name := path[strings.LastIndexAny(path, `/\`)+1:]
	return norm.NFC.String(strings.ToUpper(name))
}
