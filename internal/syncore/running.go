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

type ProcessChecker interface {
	Running(exePath string) (bool, error)
}

// ensureGameNotRunning は、セーブフォルダに触れる前と、長くかかる処理（転送やバックアップのコピー）の後に呼ぶ。その間に
// ゲームが起動されうるため（仕様書 7 章）。Push・Pull で承認後の確認より先に呼ぶのは、起動されたゲームがセーブを書くと、
// 承認後の確認が先に ErrPlanStale を返し、本当の理由が伝わらないため。
func (c *Core) ensureGameNotRunning(exePath string) error {
	if c.cfg.Processes == nil {
		return errProcessesNotSet
	}
	// platform が syncore のエラーを返すと platform が syncore に依存するので、見つからないことはここで確かめる
	if _, err := os.Stat(exePath); errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s: %w", exePath, ErrGameExeNotFound)
	}
	running, err := c.cfg.Processes.Running(exePath)
	if err != nil {
		return fmt.Errorf("check whether the game is running: %w", err)
	}
	if running {
		return ErrGameRunning
	}
	return nil
}
