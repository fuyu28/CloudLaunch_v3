// Windows のプロセスの列挙（Toolhelp と QueryFullProcessImageName）。
package platform

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

func listProcesses() ([]processEntry, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("CreateToolhelp32Snapshot: %w", err)
	}
	defer func() { _ = windows.CloseHandle(snap) }()

	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	var procs []processEntry
	buf := make([]uint16, windows.MAX_LONG_PATH)
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if e.ProcessID == 0 {
			continue
		}
		procs = append(procs, processEntry{name: windows.UTF16ToString(e.ExeFile[:]), path: imagePath(e.ProcessID, buf)})
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return nil, fmt.Errorf("Process32Next: %w", err)
	}
	return procs, nil
}

// imagePath は、開けないプロセス（保護されたシステムのプロセス、終わったばかりのプロセスなど）では空を返す。
// buf は呼び出し側が使い回す。
func imagePath(pid uint32, buf []uint16) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer func() { _ = windows.CloseHandle(h) }()
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}
