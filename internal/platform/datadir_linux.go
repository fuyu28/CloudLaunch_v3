// Linux のデータディレクトリ（XDG Base Directory の慣例）。開発用の最小限の実装。
package platform

import (
	"fmt"
	"os"
	"path/filepath"
)

// DataDir は、CLI と GUI が同じ DB・同期ロック・バックアップを使うよう、両方の既定の保存先を 1 か所で決める。
func DataDir() (string, error) {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "cloudlaunch"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	return filepath.Join(home, ".local", "share", "cloudlaunch"), nil
}
