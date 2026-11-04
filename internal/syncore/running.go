// ゲームの実行中の確認（仕様書 7 章）。
package syncore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

var (
	ErrGameRunning = errors.New("the game is running")
	// ErrGameExeNotFound は、登録した実行ファイルがなく、実行中かを確かめられないことを表す。設定を直してもらう
	ErrGameExeNotFound = errors.New("the game executable is not found")
)

// ProcessChecker は、exePath の実行ファイルのプロセスが実行中かを返す。
type ProcessChecker interface {
	Running(exePath string) (bool, error)
}

// ensureGameNotRunning は、Push と Pull が、ロックを取った直後と、転送の後（承認後の確認の直前）の 2 回呼ぶ。
// 転送は長くかかることがあり、その間にゲームが起動されうるため（仕様書 7 章）。承認後の確認より先に呼ぶのは、
// 起動されたゲームがセーブを書くと、承認後の確認が先に ErrPlanStale を返し、本当の理由が伝わらないため。
func (c *Core) ensureGameNotRunning(exePath string) error {
	if c.processes == nil {
		return errors.New("process checker is not configured")
	}
	// platform が syncore のエラーを返すと platform が syncore に依存するので、見つからないことはここで確かめる
	if _, err := os.Stat(exePath); errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s: %w", exePath, ErrGameExeNotFound)
	}
	running, err := c.processes.Running(exePath)
	if err != nil {
		return fmt.Errorf("check whether the game is running: %w", err)
	}
	if running {
		return ErrGameRunning
	}
	return nil
}
