// ゲームの実行中の確認（仕様書 7 章）。
package syncore

import "errors"

var ErrGameRunning = errors.New("the game is running")

// ProcessChecker は、exePath の実行ファイルのプロセスが実行中かを返す。
type ProcessChecker interface {
	Running(exePath string) (bool, error)
}
