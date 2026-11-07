package syncore_test

import (
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"

	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

// FuzzPull不正なmanifest は、2 つの path を持つ manifest を Pull し、6-L2 で拒否して何も変えないか、
// 成功してセーブフォルダの中だけにそのとおり書くかのどちらかであることを確かめる。規則を写して比べるのではなく、
// 実際のファイルシステムに書けたかで判定するので、検証を通ったのに書けない path（名前が変わって手順 5 で
// 失敗するなど）や、外に書いてしまう path も、仕様書 6 章の規則に書いていない穴として見つかる。
func FuzzPull不正なmanifest(f *testing.F) {
	for _, tt := range invalidPathCases {
		second := "ok.sav"
		if len(tt.paths) > 1 {
			second = tt.paths[1]
		}
		f.Add(tt.paths[0], second)
	}
	// 境目の名前。正当なものと不正なものを混ぜる
	for _, seed := range [][2]string{
		{"slot1.sav", "dir/slot2.sav"},
		{"aux .sav", "ok.sav"},
		{" aux.sav", "ok.sav"},
		{"COM10", "aux_1.sav"},
		{"a..b.sav", "dir/..c"},
		{"ı.sav", "i.sav"},
		{"ſ.sav", "s.sav"},
		{"セーブ/スロット1.sav", "セーブ/スロット2.sav"},
		{"a", "ab/c.sav"},
		{"x.sav", "x/b.sav"},
	} {
		f.Add(seed[0], seed[1])
	}

	f.Fuzz(func(t *testing.T, p1, p2 string) {
		// remote の manifest は JSON から読むので、正しくない UTF-8 は現れない（U+FFFD に置き換わって並びも変わる）
		if p1 == p2 || !utf8.ValidString(p1) || !utf8.ValidString(p2) {
			t.Skip("同じ path か、正しくない UTF-8")
		}
		// 並べずに渡すと多くの入力が 6-M9 で拒否され、path の規則に届かない
		if p2 < p1 {
			p1, p2 = p2, p1
		}
		e := newEnv(t)
		// 名前の長さの上限は仕様書に規則がなく、未決事項にある。決まるまで、Linux で作れない長さは飛ばす。
		// path 全体は、stage のディレクトリの名前の分（64 バイト）の余裕を見て PATH_MAX と比べる
		for _, p := range []string{p1, p2} {
			tooLongElem := slices.ContainsFunc(strings.Split(p, "/"), func(elem string) bool { return len(elem) > 255 })
			if tooLongElem || len(filepath.Dir(e.saveDir))+64+len(p) >= 4096 {
				t.Skip("名前が長すぎる（仕様書 11 章の未決事項）")
			}
		}
		e.putObject(t, objectKey("x"), "x")
		e.putGeneration(t, []syncore.Entry{
			{Path: p1, SHA256: sha256Hex("x"), Size: 1},
			{Path: p2, SHA256: sha256Hex("x"), Size: 1},
		})
		plan := e.planWant(t, syncore.DecisionPull)

		err := e.core.Pull(t.Context(), plan, syncore.PullOptions{})
		if err != nil {
			// Pull は 6-L2 の検証の後に同期ロックを取る。取得した object の数で判定すると、取得の前に
			// ディレクトリを作れずに失敗した穴を、拒否と取り違える
			if acquired, _ := e.lock.state(); acquired != 0 || errors.Is(err, syncore.ErrPlanStale) {
				t.Fatalf("検証を通ったのに Pull が失敗した: %v", err)
			}
			assertAborted(t, e, map[string]string{})
			return
		}

		want := map[string]string{p1: "x", p2: "x"}
		if diff := cmp.Diff(want, readFiles(t, e.saveDir)); diff != "" {
			t.Errorf("セーブフォルダ (-want +got):\n%s", diff)
		}
		if outside := filesOutsideSaveDir(t, e); len(outside) != 0 {
			t.Errorf("セーブフォルダの外にファイルを作った: %v", outside)
		}
	})
}

// filesOutsideSaveDir は、env の一時ディレクトリのうち、セーブフォルダと、テストの準備で作ったファイル（DB と
// 実行ファイル）の外にあるファイルを返す。Pull 前のセーブフォルダは空なので、バックアップにもファイルはできない。
func filesOutsideSaveDir(t *testing.T, e env) []string {
	t.Helper()
	root := filepath.Dir(e.saveDir)
	var outside []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == e.saveDir {
			return filepath.SkipDir
		}
		if d.IsDir() || p == e.exePath || strings.HasPrefix(p, e.dbPath) {
			return nil
		}
		outside = append(outside, p)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return outside
}
