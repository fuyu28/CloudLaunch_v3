package platform

import "testing"

func TestMatchesExe(t *testing.T) {
	const exe = `C:\Games\Foo\game.exe`
	tests := []struct {
		name string
		exe  string
		proc processEntry
		want bool
	}{
		{
			name: "7章/実行中の確認/パスが完全に一致すれば実行中",
			exe:  exe,
			proc: processEntry{name: "game.exe", path: `C:\Games\Foo\game.exe`},
			want: true,
		},
		{
			// 登録したパスは「/」区切り・大文字・濁点が分かれた形（NFD）、プロセスのパスは「\」区切り・NFC
			name: "7章/実行中の確認/大文字小文字・区切り・NFC の違いはそろえて比べる",
			exe:  "C:/GAMES/\u3075\u3099\u30fc/Game.EXE",
			proc: processEntry{name: "game.exe", path: "C:\\Games\\\u3076\u30fc\\game.exe"},
			want: true,
		},
		{
			name: "7章/実行中の確認/ファイル名が違えば実行中でない",
			exe:  exe,
			proc: processEntry{name: "other.exe", path: `C:\Games\Foo\other.exe`},
			want: false,
		},
		{
			name: "7章/実行中の確認/同じファイル名で別のパスなら実行中でない",
			exe:  exe,
			proc: processEntry{name: "game.exe", path: `D:\Other\game.exe`},
			want: false,
		},
		{
			name: "7章/実行中の確認/パスが取れないプロセスは、ファイル名が一致すれば実行中",
			exe:  exe,
			proc: processEntry{name: "Game.exe"},
			want: true,
		},
		{
			name: "7章/実行中の確認/パスが取れず、ファイル名も違えば実行中でない",
			exe:  exe,
			proc: processEntry{name: "other.exe"},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchesExe(tt.exe, tt.proc); got != tt.want {
				t.Errorf("matchesExe(%q, %+v) = %v, want %v", tt.exe, tt.proc, got, tt.want)
			}
		})
	}
}
