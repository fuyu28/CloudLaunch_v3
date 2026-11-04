package platform

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// holdLockEnv があるとき、TestFileLockHelperProcess は子プロセスとしてロックを握り続ける。
const holdLockEnv = "CLOUDLAUNCH_TEST_HOLD_LOCK"

func mustLock(t *testing.T, l *FileLock) func() error {
	t.Helper()
	unlock, ok, err := l.TryLock()
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	if !ok {
		t.Fatal("TryLock: 取れなかった")
	}
	return unlock
}

func assertLocked(t *testing.T, l *FileLock) {
	t.Helper()
	unlock, ok, err := l.TryLock()
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	if ok {
		_ = unlock()
		t.Fatal("TryLock: ほかが握っているのに取れた")
	}
}

func TestFileLock(t *testing.T) {
	t.Run("3章/排他/同じファイルの 2 つ目のロックは取れない", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "sync.lock")
		unlock := mustLock(t, NewFileLock(path))
		defer func() { _ = unlock() }()

		assertLocked(t, NewFileLock(path))
	})

	t.Run("3章/排他/解放すれば取れる", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "sync.lock")
		unlock := mustLock(t, NewFileLock(path))
		if err := unlock(); err != nil {
			t.Fatalf("unlock: %v", err)
		}

		unlock = mustLock(t, NewFileLock(path))
		if err := unlock(); err != nil {
			t.Fatalf("unlock: %v", err)
		}
	})

	t.Run("3章/排他/ロックを握ったプロセスが終われば取れる", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "nested", "sync.lock")
		cmd := exec.Command(os.Args[0], "-test.run=^TestFileLockHelperProcess$")
		cmd.Env = append(os.Environ(), holdLockEnv+"="+path)
		out, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatalf("StdoutPipe: %v", err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatalf("start helper: %v", err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
		if line, err := bufio.NewReader(out).ReadString('\n'); err != nil || line != "locked\n" {
			t.Fatalf("helper の出力 = %q, %v, want %q", line, err, "locked\n")
		}

		assertLocked(t, NewFileLock(path))

		// 異常終了を起こし、OS がロックを外すことを確かめる（仕様書 3 章）
		if err := cmd.Process.Kill(); err != nil {
			t.Fatalf("kill helper: %v", err)
		}
		_ = cmd.Wait()
		// Windows はプロセスの終了とロックの解放が同時とは限らないので、しばらく待つ
		deadline := time.Now().Add(5 * time.Second)
		for {
			unlock, ok, err := NewFileLock(path).TryLock()
			if err != nil {
				t.Fatalf("TryLock: %v", err)
			}
			if ok {
				_ = unlock()
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("ロックを握ったプロセスが終わっても取れない")
			}
			time.Sleep(50 * time.Millisecond)
		}
	})
}

// TestFileLockHelperProcess は、TestFileLock の子プロセスとして動くときだけ、ロックを握って待ち続ける。
func TestFileLockHelperProcess(t *testing.T) {
	path := os.Getenv(holdLockEnv)
	if path == "" {
		t.Skip("TestFileLock の子プロセスとしてだけ動く")
	}
	unlock, ok, err := NewFileLock(path).TryLock()
	if err != nil || !ok {
		fmt.Fprintf(os.Stderr, "TryLock: ok = %v, err = %v\n", ok, err)
		os.Exit(2)
	}
	fmt.Println("locked")
	time.Sleep(time.Minute)
	// unlock を捨てると、開いたファイルが GC で閉じられてロックが外れうるので、待つ間は参照を保つ
	runtime.KeepAlive(unlock)
	os.Exit(0)
}
