// Windows のデータディレクトリ（仕様書 3 章の保存先）。
package platform

import (
	"errors"
	"os"
	"path/filepath"
)

// DataDir は、CLI と GUI が同じ DB・同期ロック・バックアップを使うよう、両方の既定の保存先を 1 か所で決める。
func DataDir() (string, error) {
	d := os.Getenv("LOCALAPPDATA")
	if d == "" {
		return "", errors.New("LOCALAPPDATA is not set")
	}
	return filepath.Join(d, "CloudLaunch"), nil
}
