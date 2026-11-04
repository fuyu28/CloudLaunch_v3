package platform

import "testing"

func TestMatchesExe(t *testing.T) {
	// sameFile は、プロセスのパスを Stat できたか（ok）と、登録した実行ファイルと同じファイルか（same）を返す偽物
	type sameFileResult struct{ same, ok bool }
	tests := []struct {
		name     string
		exe      string
		proc     processEntry
		sameFile sameFileResult
		want     bool
	}{
		{
			// 短い名前で登録しても、同じファイルなら実行中
			name:     "7章/実行中の確認/パスが同じファイルを指せば実行中",
			exe:      `C:\PROGRA~2\Foo\GAME~1.EXE`,
			proc:     processEntry{name: "game.exe", path: `C:\Program Files (x86)\Foo\game.exe`},
			sameFile: sameFileResult{same: true, ok: true},
			want:     true,
		},
		{
			name:     "7章/実行中の確認/同じファイル名でも、別のファイルなら実行中でない",
			exe:      `C:\Games\Foo\game.exe`,
			proc:     processEntry{name: "game.exe", path: `D:\Other\game.exe`},
			sameFile: sameFileResult{same: false, ok: true},
			want:     false,
		},
		{
			name: "7章/実行中の確認/パスが取れないプロセスは、ファイル名が一致すれば実行中",
			exe:  `C:\Games\Foo\game.exe`,
			proc: processEntry{name: "game.exe"},
			want: true,
		},
		{
			name:     "7章/実行中の確認/パスを Stat できないプロセスは、ファイル名が一致すれば実行中",
			exe:      `C:\Games\Foo\game.exe`,
			proc:     processEntry{name: "game.exe", path: `C:\Games\Foo\game.exe`},
			sameFile: sameFileResult{same: false, ok: false},
			want:     true,
		},
		{
			name: "7章/実行中の確認/パスが取れず、ファイル名も違えば実行中でない",
			exe:  `C:\Games\Foo\game.exe`,
			proc: processEntry{name: "other.exe"},
			want: false,
		},
		{
			// 登録したパスは「/」区切り・小文字・濁点が分かれた形（NFD）、プロセスの名前は大文字・NFC
			name: "7章/実行中の確認/名前の大文字小文字・NFC の違いはそろえて比べる",
			exe:  "C:/games/\u3075\u3099\u30fc.exe",
			proc: processEntry{name: "\u3076\u30fc.EXE"},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sameFile := func(path string) (bool, bool) {
				if path != tt.proc.path {
					t.Errorf("sameFile(%q), want %q", path, tt.proc.path)
				}
				return tt.sameFile.same, tt.sameFile.ok
			}
			if got := matchesExe(exeNameKey(tt.exe), tt.proc, sameFile); got != tt.want {
				t.Errorf("matchesExe(%q, %+v) = %v, want %v", tt.exe, tt.proc, got, tt.want)
			}
		})
	}
}
