// 仕様書 6 章「不正な manifest」の規則を検査する。Pull は remote の manifest 全体に、
// local の manifest の計算（仕様書 2 章）はファイルの path に、同じ path の規則をかける。
package syncore

import (
	"errors"
	"fmt"
	"strings"
)

var reservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true, "CONIN$": true, "CONOUT$": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
	"COM¹": true, "COM²": true, "COM³": true, "LPT¹": true, "LPT²": true, "LPT³": true,
}

// validateManifest は、entries が 6-M1〜6-M13 のどれかに当たればエラーを返す。
// entries が nil なのは、JSON の entries がないか null のとき（6-M6）で、[] なら長さ 0 の nil でないスライスになる。
func validateManifest(entries []Entry) error {
	if entries == nil {
		return errors.New("entries is missing or null")
	}
	paths := make([]string, len(entries))
	for i, e := range entries {
		if !isSHA256Hex(e.SHA256) {
			return fmt.Errorf("path %q: sha256 %q is not 64 lowercase hex digits", e.Path, e.SHA256)
		}
		if e.Size < 0 {
			return fmt.Errorf("path %q: negative size %d", e.Path, e.Size)
		}
		if i > 0 && paths[i-1] >= e.Path {
			return fmt.Errorf("paths %q and %q are not in strictly ascending order", paths[i-1], e.Path)
		}
		paths[i] = e.Path
	}
	return validatePaths(paths)
}

type pathNode struct {
	spelling string
	isDir    bool
	// from は、この名前を登録した path。エラーで、どのファイルを直せばよいかを示すために持つ
	from string
}

// validatePaths は、paths のどれかが path の規則（6-M1〜6-M5・6-M10〜6-M13）に当たればエラーを返す。
// local にかけても仕様書 2 章の検証と同じになる。local で 6-M13 に当たる組み合わせは、大文字小文字を区別する
// フォルダの `a` と `A/b.sav` のように綴りが違うものだけで、それは 6-M5 にも当たるため。
//
// 6-M5 と 6-M13 は、各 path のディレクトリの部分（短い順）と path 全体を、大文字に揃えた名前で 1 つの map に
// 登録しながら調べる。ファイルとディレクトリを同じ map に置くので、並び順によらず衝突を見つけられる。
func validatePaths(paths []string) error {
	seen := make(map[string]pathNode, len(paths))
	for _, p := range paths {
		if err := validatePath(p); err != nil {
			return fmt.Errorf("path %q: %w", p, err)
		}
		elems := strings.Split(p, "/")
		// 接頭辞ごとに揃え直すと path の深さ×長さの手間になるので、要素ごとに揃えて継ぎ足す。
		// Builder が返した文字列は、後で書き足しても変わらない
		var folded strings.Builder
		end := 0
		for i, elem := range elems {
			if i > 0 {
				folded.WriteByte('/')
				end++
			}
			end += len(elem)
			spelling := p[:end]
			// Windows はファイル名を大文字に揃えて比べるので、ToLower ではなく ToUpper で揃える
			folded.WriteString(strings.ToUpper(elem))
			key := folded.String()
			node := pathNode{spelling: spelling, isDir: i < len(elems)-1, from: p}
			other, ok := seen[key]
			switch {
			case !ok:
				seen[key] = node
			case other.isDir != node.isDir:
				return fmt.Errorf("paths %q and %q use %q as both a file and a directory", other.from, p, spelling)
			case !node.isDir || other.spelling != node.spelling:
				return fmt.Errorf("paths %q and %q differ only in case (%q and %q)", other.from, p, other.spelling, spelling)
			}
		}
	}
	return nil
}

func validatePath(p string) error {
	switch {
	case p == "":
		return errors.New("empty")
	case strings.HasPrefix(p, "/"):
		return errors.New("absolute")
	case strings.Contains(p, `\`):
		return errors.New(`contains \`)
	// 6-M2 のドライブレターも、6-M12 の : で拒否する
	case strings.ContainsAny(p, `<>:"|?*`):
		return errors.New("contains a character not allowed on Windows")
	case strings.ContainsFunc(p, func(r rune) bool { return r < 0x20 }):
		return errors.New("contains a control character")
	}
	for _, elem := range strings.Split(p, "/") {
		if elem == "" || elem == "." || elem == ".." {
			return fmt.Errorf("has element %q", elem)
		}
		if strings.HasSuffix(elem, ".") || strings.HasSuffix(elem, " ") {
			return fmt.Errorf("element %q ends with a dot or space", elem)
		}
		stem, _, _ := strings.Cut(elem, ".")
		// Windows は末尾の空白を取り除いてからデバイス名と比べる。先頭の空白は取り除かない
		if reservedNames[strings.ToUpper(strings.TrimRight(stem, " "))] {
			return fmt.Errorf("element %q is a reserved device name on Windows", elem)
		}
	}
	return nil
}

func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range []byte(s) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
