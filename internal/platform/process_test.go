package platform

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// sleepEnv があるとき、TestProcessHelperSleep は子プロセスとして待ち続ける。
const sleepEnv = "CLOUDLAUNCH_TEST_SLEEP"

// copyTestBinary は、テストのバイナリを別のパスにコピーする。テストのバイナリ自身は常に動いているので、
// 「起動していない」状態を作るには別のパスが要る。ファイル名は同じなので、同じ名前で別のパスのプロセス
// （このテストのプロセス自身）があっても、実行中と判定しないことも確かめられる。
func copyTestBinary(t *testing.T) string {
	t.Helper()
	src, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatalf("open test binary: %v", err)
	}
	defer func() { _ = src.Close() }()
	dst := filepath.Join(t.TempDir(), "child", filepath.Base(os.Args[0]))
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		t.Fatalf("create copy: %v", err)
	}
	if _, err := io.Copy(f, src); err != nil {
		t.Fatalf("copy test binary: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close copy: %v", err)
	}
	return dst
}

// waitRunning は、Running が want になるまで待つ。プロセスの起動と終了は、列挙に現れるまで少しかかる。
func waitRunning(t *testing.T, exePath string, want bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := Processes{}.Running(exePath)
		if err != nil {
			t.Fatalf("Running: %v", err)
		}
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("Running(%q) = %v のまま, want %v", exePath, got, want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestProcesses(t *testing.T) {
	t.Run("7章/実行中の確認/登録した実行ファイルがなければエラー", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "missing", filepath.Base(os.Args[0]))
		if got, err := (Processes{}).Running(missing); err == nil {
			t.Errorf("Running = %v, nil, want an error", got)
		}
	})

	exe := copyTestBinary(t)
	cmd := exec.Command(exe, "-test.run=^TestProcessHelperSleep$")
	cmd.Env = append(os.Environ(), sleepEnv+"=1")
	waitRunning(t, exe, false)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	t.Run("7章/実行中の確認/起動した子プロセスのパスで実行中になる", func(t *testing.T) {
		waitRunning(t, exe, true)
	})

	t.Run("7章/実行中の確認/Windows では大文字小文字を変えたパスでも実行中になる", func(t *testing.T) {
		if runtime.GOOS != "windows" {
			t.Skip("大文字小文字を区別しないのは Windows のファイルシステムだけ")
		}
		waitRunning(t, strings.ToUpper(exe), true)
	})

	t.Run("7章/実行中の確認/リンクを経由して登録したパスでも実行中になる", func(t *testing.T) {
		link := filepath.Join(t.TempDir(), "link-"+filepath.Base(exe))
		if err := os.Symlink(exe, link); err != nil {
			t.Skipf("シンボリックリンクを作れない: %v", err)
		}
		waitRunning(t, link, true)
	})

	t.Run("7章/実行中の確認/kill した後は実行中でない", func(t *testing.T) {
		if err := cmd.Process.Kill(); err != nil {
			t.Fatalf("kill child: %v", err)
		}
		_ = cmd.Wait()
		waitRunning(t, exe, false)
	})
}

// TestProcessHelperSleep は、TestProcesses の子プロセスとして動くときだけ、待ち続ける。
func TestProcessHelperSleep(t *testing.T) {
	if os.Getenv(sleepEnv) == "" {
		t.Skip("TestProcesses の子プロセスとしてだけ動く")
	}
	time.Sleep(time.Minute)
	os.Exit(0)
}
