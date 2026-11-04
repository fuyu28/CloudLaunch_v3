// ゲームの実行中の確認（仕様書 7 章）。
package syncore

import (
	"errors"
	"fmt"
)

var ErrGameRunning = errors.New("the game is running")

// ProcessChecker は、exePath の実行ファイルのプロセスが実行中かを返す。
type ProcessChecker interface {
	Running(exePath string) (bool, error)
}

// ensureGameNotRunning は、Push と Pull が、ロックを取った直後と、転送の後（承認後の確認）の 2 回呼ぶ。
// 転送は長くかかることがあり、その間にゲームが起動されうるため（仕様書 7 章）。
func (c *Core) ensureGameNotRunning(exePath string) error {
	if c.processes == nil {
		return errors.New("process checker is not configured")
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
