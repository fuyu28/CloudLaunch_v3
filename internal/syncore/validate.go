// 仕様書 6 章「不正な manifest」の規則を検査する。Pull は remote の manifest 全体に、
// local の manifest の計算（仕様書 2 章）はファイルの path に、同じ path の規則をかける。
package syncore

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var drivePattern = regexp.MustCompile(`[A-Za-z]:`)

var reservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true, "CONIN$": true, "CONOUT$": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
	"COM¹": true, "COM²": true, "COM³": true, "LPT¹": true, "LPT²": true, "LPT³": true,
}

// validateManifest は、entries が 6-M1〜6-M12 のどれかに当たればエラーを返す。
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

// validatePaths は、paths のどれかが path の規則（6-M1〜6-M5・6-M10〜6-M12）に当たればエラーを返す。
func validatePaths(paths []string) error {
	seen := make(map[string]string, len(paths))
	for _, p := range paths {
		if err := validatePath(p); err != nil {
			return fmt.Errorf("path %q: %w", p, err)
		}
		// Windows はファイル名を大文字に揃えて比べるので、ToLower ではなく ToUpper で揃える
		folded := strings.ToUpper(p)
		if other, ok := seen[folded]; ok {
			return fmt.Errorf("paths %q and %q differ only in case", other, p)
		}
		seen[folded] = p
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
	case drivePattern.MatchString(p):
		return errors.New("contains a drive letter")
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
		if reservedNames[strings.ToUpper(stem)] {
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
