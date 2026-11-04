// Linux のプロセスの列挙（/proc）。開発用の最小限の実装。
package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// listProcesses は、/proc/{pid}/exe を読めないプロセス（ほかの利用者のもの、終わったばかりのものなど）を飛ばす。
// Windows と違って名前だけで照合しないのは、/proc/{pid}/comm が名前を 15 バイトで切るため。
func listProcesses() ([]processEntry, error) {
	dirs, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("read /proc: %w", err)
	}
	var procs []processEntry
	for _, d := range dirs {
		if _, err := strconv.Atoi(d.Name()); err != nil {
			continue
		}
		path, err := os.Readlink(filepath.Join("/proc", d.Name(), "exe"))
		if err != nil {
			continue
		}
		procs = append(procs, processEntry{name: filepath.Base(path), path: path})
	}
	return procs, nil
}
