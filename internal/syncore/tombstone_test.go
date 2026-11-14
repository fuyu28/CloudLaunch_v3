package syncore_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/fuyu28/CloudLaunch_v3/internal/objstore/memstore"
	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

const otherGameID = "0192d4e8-0000-7000-8000-0000000000a1"

// recordedTombstone は recordTombstone で DB に書く値で、リモートの deleted.json の値（testTombstone）と区別できる。
var recordedTombstone = syncore.Tombstone{DeletedAt: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), DeviceName: "Tablet"}

var invalidTombstones = []struct {
	name string
	body string
}{
	{"8-T4 JSON として読めない", `{`},
	{"8-T4 64 KiB を超える", testTombstoneJSON + strings.Repeat(" ", 64<<10)},
	{"8-T5 deletedAt がない", `{"deviceName":"Laptop"}`},
	{"8-T5 deviceName が空", `{"deletedAt":"2026-11-03T01:02:03.004Z","deviceName":""}`},
	{"8-T6 deletedAt が日時の形でない", `{"deletedAt":"2026-11-03","deviceName":"Laptop"}`},
}

// putGameData は、testGameID の配下にゲームの tombstone 以外のデータを、otherGameID の配下にほかのゲームのデータを置く。
func (e env) putGameData(t *testing.T) {
	t.Helper()
	e.putRemoteFiles(t, map[string]string{"slot1.sav": "save"})
	e.putObject(t, "games/"+testGameID+"/game.json", "{}")
	e.putObject(t, "games/"+testGameID+"/sessions/0192d4e8-0000-7000-8000-0000000000c1.json", "{}")
	e.putObject(t, "games/"+otherGameID+"/HEAD", testGeneration)
	e.putObject(t, "games/"+otherGameID+"/game.json", "{}")
}

// afterCleanup は、putGameData と deleted.json を置いた後に配下を削除したときに残るキー。
func afterCleanup() []string {
	return []string{tombstoneKey(), "games/" + otherGameID + "/HEAD", "games/" + otherGameID + "/game.json"}
}

func assertTombstone(t *testing.T, got syncore.Tombstone, ok bool, err error, want syncore.Tombstone, wantOK bool) {
	t.Helper()
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if ok != wantOK {
		t.Fatalf("ok = %v, want %v", ok, wantOK)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("tombstone (-want +got):\n%s", diff)
	}
}

func TestReadRemoteTombstone(t *testing.T) {
	ctx := context.Background()

	t.Run("8章/tombstone/リモートだけの確認は、DB の記録を見ずに deleted.json だけを読む", func(t *testing.T) {
		e := newEnv(t)
		e.recordTombstone(t, recordedTombstone)
		e.putTombstone(t)

		got, ok, err := e.core.ReadRemoteTombstone(ctx, testGameID)
		assertTombstone(t, got, ok, err, testTombstone, true)
	})

	t.Run("8章/tombstone/リモートだけの確認は、登録していないゲームも読む", func(t *testing.T) {
		e := newEnv(t)

		got, ok, err := e.core.ReadRemoteTombstone(ctx, otherGameID)
		assertTombstone(t, got, ok, err, syncore.Tombstone{}, false)
	})

	t.Run("8章/tombstone/リモートだけの確認は、不正な deleted.json を ErrInvalidTombstone で返す", func(t *testing.T) {
		e := newEnv(t)
		e.putObject(t, tombstoneKey(), `{`)

		if _, _, err := e.core.ReadRemoteTombstone(ctx, testGameID); !errors.Is(err, syncore.ErrInvalidTombstone) {
			t.Errorf("err = %v, want ErrInvalidTombstone", err)
		}
	})

	t.Run("8章/tombstone/リモートだけの確認は、読めなければ ErrInvalidTombstone でないエラーを返す", func(t *testing.T) {
		e := newEnv(t)
		injected := errors.New("injected: get deleted.json")
		failOn(e, memstore.OpGet, "deleted.json", injected)

		_, _, err := e.core.ReadRemoteTombstone(ctx, testGameID)
		if !errors.Is(err, injected) || errors.Is(err, syncore.ErrInvalidTombstone) {
			t.Errorf("err = %v, want ErrInvalidTombstone でない元のエラー", err)
		}
	})
}

func TestCheckTombstone(t *testing.T) {
	ctx := context.Background()

	t.Run("8-T1 deleted.json がなければ、削除されていないと返す", func(t *testing.T) {
		e := newEnv(t)
		e.putGameData(t)

		got, ok, err := e.core.CheckTombstone(ctx, testGameID)
		assertTombstone(t, got, ok, err, syncore.Tombstone{}, false)
	})

	t.Run("8-T2 status 用の確認は、記録も削除もしない", func(t *testing.T) {
		e := newEnv(t)
		e.putGameData(t)
		e.putTombstone(t)
		before := e.keys(t, "games/")

		got, ok, err := e.core.CheckTombstone(ctx, testGameID)
		assertTombstone(t, got, ok, err, testTombstone, true)
		if _, rec := e.recorded(t); rec {
			t.Error("DB に記録した")
		}
		if diff := cmp.Diff(before, e.keys(t, "games/")); diff != "" {
			t.Errorf("リモートのキーが変わった (-before +after):\n%s", diff)
		}
	})

	t.Run("8-T3 deleted.json を読めなければ、エラーにする", func(t *testing.T) {
		e := newEnv(t)
		failOn(e, memstore.OpGet, "deleted.json", errors.New("injected: get deleted.json"))

		if _, _, err := e.core.CheckTombstone(ctx, testGameID); err == nil {
			t.Error("エラーを返さなかった")
		}
	})

	for _, tc := range invalidTombstones {
		t.Run(tc.name+"とき、エラーにする", func(t *testing.T) {
			e := newEnv(t)
			e.putObject(t, tombstoneKey(), tc.body)

			if _, _, err := e.core.CheckTombstone(ctx, testGameID); !errors.Is(err, syncore.ErrInvalidTombstone) {
				t.Errorf("err = %v, want ErrInvalidTombstone", err)
			}
		})
	}

	t.Run("8-T8 記録があれば、status 用の確認はリモートを読まずに記録を返す", func(t *testing.T) {
		e := newEnv(t)
		e.recordTombstone(t, recordedTombstone)
		failOn(e, memstore.OpGet, "", errors.New("リモートを読んだ"))

		got, ok, err := e.core.CheckTombstone(ctx, testGameID)
		assertTombstone(t, got, ok, err, recordedTombstone, true)
	})
}

func TestHandleTombstone(t *testing.T) {
	ctx := context.Background()

	t.Run("8-T1 deleted.json がなければ、削除されていないと返し、何も削除しない", func(t *testing.T) {
		e := newEnv(t)
		e.putGameData(t)
		before := e.keys(t, "games/")

		got, ok, err := e.core.HandleTombstone(ctx, testGameID)
		assertTombstone(t, got, ok, err, syncore.Tombstone{}, false)
		if diff := cmp.Diff(before, e.keys(t, "games/")); diff != "" {
			t.Errorf("リモートのキーが変わった (-before +after):\n%s", diff)
		}
		if _, rec := e.recorded(t); rec {
			t.Error("DB に記録した")
		}
	})

	t.Run("8-T2 deleted.json があれば、DB に記録し、tombstone 以外を削除する", func(t *testing.T) {
		e := newEnv(t)
		e.putGameData(t)
		e.putTombstone(t)

		got, ok, err := e.core.HandleTombstone(ctx, testGameID)
		assertTombstone(t, got, ok, err, testTombstone, true)
		rec, recOK := e.recorded(t)
		if !recOK {
			t.Fatal("DB に記録しなかった")
		}
		if diff := cmp.Diff(testTombstone, rec); diff != "" {
			t.Errorf("記録 (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(afterCleanup(), e.keys(t, "games/")); diff != "" {
			t.Errorf("残ったキー (-want +got):\n%s", diff)
		}
	})

	t.Run("8-T2 削除の途中で失敗しても、次の確認で消し残りを削除する", func(t *testing.T) {
		e := newEnv(t)
		e.putGameData(t)
		e.putTombstone(t)
		failOn(e, memstore.OpDelete, "/game.json", errors.New("injected: delete game.json"))

		got, ok, err := e.core.HandleTombstone(ctx, testGameID)
		if err == nil {
			t.Fatal("エラーを返さなかった")
		}
		// 削除に失敗しても、記録したので「リモートで削除されました」と表示させる
		if !ok || got != testTombstone {
			t.Errorf("tombstone = %+v, %v, want %+v, true", got, ok, testTombstone)
		}
		if _, rec := e.recorded(t); !rec {
			t.Error("削除の前に DB に記録しなかった")
		}

		e.remote.Before = nil
		got, ok, err = e.core.HandleTombstone(ctx, testGameID)
		assertTombstone(t, got, ok, err, testTombstone, true)
		if diff := cmp.Diff(afterCleanup(), e.keys(t, "games/")); diff != "" {
			t.Errorf("残ったキー (-want +got):\n%s", diff)
		}
	})

	t.Run("8-T2 記録のあるゲームでも、消し残りを削除し、記録は書き換えずに記録を返す（8-X10）", func(t *testing.T) {
		e := newEnv(t)
		e.putGameData(t)
		e.putTombstone(t)
		e.recordTombstone(t, recordedTombstone)

		got, ok, err := e.core.HandleTombstone(ctx, testGameID)
		assertTombstone(t, got, ok, err, recordedTombstone, true)
		rec, _ := e.recorded(t)
		if diff := cmp.Diff(recordedTombstone, rec); diff != "" {
			t.Errorf("記録 (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(afterCleanup(), e.keys(t, "games/")); diff != "" {
			t.Errorf("残ったキー (-want +got):\n%s", diff)
		}
	})

	t.Run("8-T3 deleted.json を読めなければ、エラーにし、記録も削除もしない", func(t *testing.T) {
		e := newEnv(t)
		e.putGameData(t)
		e.putTombstone(t)
		before := e.keys(t, "games/")
		failOn(e, memstore.OpGet, "deleted.json", errors.New("injected: get deleted.json"))

		if _, _, err := e.core.HandleTombstone(ctx, testGameID); err == nil {
			t.Error("エラーを返さなかった")
		}
		if _, rec := e.recorded(t); rec {
			t.Error("DB に記録した")
		}
		e.remote.Before = nil
		if diff := cmp.Diff(before, e.keys(t, "games/")); diff != "" {
			t.Errorf("リモートのキーが変わった (-before +after):\n%s", diff)
		}
	})

	for _, tc := range invalidTombstones {
		t.Run(tc.name+"とき、エラーにし、記録も削除もしない", func(t *testing.T) {
			e := newEnv(t)
			e.putGameData(t)
			e.putObject(t, tombstoneKey(), tc.body)
			before := e.keys(t, "games/")

			if _, _, err := e.core.HandleTombstone(ctx, testGameID); !errors.Is(err, syncore.ErrInvalidTombstone) {
				t.Errorf("err = %v, want ErrInvalidTombstone", err)
			}
			if _, rec := e.recorded(t); rec {
				t.Error("DB に記録した")
			}
			if diff := cmp.Diff(before, e.keys(t, "games/")); diff != "" {
				t.Errorf("リモートのキーが変わった (-before +after):\n%s", diff)
			}
		})
	}

	recordedCases := []struct {
		name    string
		prepare func(t *testing.T, e env)
	}{
		{"8-T8 記録があり deleted.json がなければ、何も削除せずに記録を返す", func(*testing.T, env) {}},
		{"8-T8 記録があれば、deleted.json を読めなくても、エラーにせず何も削除せずに記録を返す", func(t *testing.T, e env) {
			e.putTombstone(t)
			failOn(e, memstore.OpGet, "deleted.json", errors.New("injected: get deleted.json"))
		}},
		{"8-T8 記録があれば、deleted.json が不正でも、エラーにせず何も削除せずに記録を返す", func(t *testing.T, e env) {
			e.putObject(t, tombstoneKey(), `{`)
		}},
	}
	for _, tc := range recordedCases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.putGameData(t)
			e.recordTombstone(t, recordedTombstone)
			tc.prepare(t, e)
			before := e.keys(t, "games/")

			got, ok, err := e.core.HandleTombstone(ctx, testGameID)
			assertTombstone(t, got, ok, err, recordedTombstone, true)
			if diff := cmp.Diff(before, e.keys(t, "games/")); diff != "" {
				t.Errorf("リモートのキーが変わった (-before +after):\n%s", diff)
			}
		})
	}
}

func TestStatusTombstone(t *testing.T) {
	t.Run("8-T2 tombstone があれば判定しない", func(t *testing.T) {
		e := newEnv(t)
		files := map[string]string{"slot1.sav": "local"}
		writeFiles(t, e.saveDir, files)
		e.putRemoteFiles(t, map[string]string{"slot1.sav": "remote"})
		e.putTombstone(t)
		// 判定しないので、HEAD を読めば失敗させる
		failOn(e, memstore.OpGet, "HEAD", errors.New("read HEAD"))

		plan, err := e.status(t)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		want := syncore.Plan{GameID: testGameID, RemoteDeleted: &testTombstone}
		if diff := cmp.Diff(want, plan); diff != "" {
			t.Errorf("plan (-want +got):\n%s", diff)
		}
	})

	t.Run("8-T3 確認が失敗すれば、Status はエラーを返す", func(t *testing.T) {
		e := newEnv(t)
		injected := errors.New("read deleted.json")
		failOn(e, memstore.OpGet, "deleted.json", injected)

		if _, err := e.status(t); !errors.Is(err, injected) {
			t.Errorf("err = %v, want %v", err, injected)
		}
	})
}

func TestPushPullTombstone(t *testing.T) {
	t.Run("8-T7 承認後に tombstone が書かれたら、Push は HEAD を書かずに止める", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, map[string]string{"slot1.sav": "local"})
		plan := e.planWant(t, syncore.DecisionPush)
		e.putTombstoneOnGet(1)

		err := e.core.Push(t.Context(), plan, syncore.PushOptions{})
		if !errors.Is(err, syncore.ErrRemoteDeleted) {
			t.Fatalf("err = %v, want ErrRemoteDeleted", err)
		}
		if _, ok := e.getObject(t, headKey()); ok {
			t.Error("HEAD を書いた")
		}
		if base := e.base(t); base != "" {
			t.Errorf("base = %q, want 空", base)
		}
	})

	t.Run("8-T7 承認後に tombstone が書かれたら、Pull は stage とジャーナルを削除して止める", func(t *testing.T) {
		e := newEnv(t)
		local := map[string]string{"slot1.sav": "local"}
		writeFiles(t, e.saveDir, local)
		e.setBase(t, syncore.ManifestHash(entriesOf(local)))
		e.putRemoteFiles(t, map[string]string{"slot1.sav": "remote"})
		plan := e.planWant(t, syncore.DecisionPull)
		e.putTombstoneOnGet(1)

		err := e.core.Pull(t.Context(), plan, syncore.PullOptions{})
		if !errors.Is(err, syncore.ErrRemoteDeleted) {
			t.Fatalf("err = %v, want ErrRemoteDeleted", err)
		}
		if diff := cmp.Diff(local, readFiles(t, e.saveDir)); diff != "" {
			t.Errorf("セーブフォルダが変わった (-want +got):\n%s", diff)
		}
		if stages := e.workDirs(t, "stage"); len(stages) != 0 {
			t.Errorf("stage が残った: %v", stages)
		}
		if n := e.journalCount(t); n != 0 {
			t.Errorf("ジャーナルが %d 行残った", n)
		}
	})

	t.Run("8-T7 承認後の削除で HEAD も消えたら、状況の変化ではなく削除として止める", func(t *testing.T) {
		e := newEnv(t)
		local := map[string]string{"slot1.sav": "local"}
		writeFiles(t, e.saveDir, local)
		e.setBase(t, syncore.ManifestHash(entriesOf(local)))
		e.putRemoteFiles(t, map[string]string{"slot1.sav": "remote"})
		plan := e.planWant(t, syncore.DecisionPull)
		// ゲームの削除（8-X6）は、object を取得した後に HEAD を消す
		e.remote.Before = func(op memstore.Op, key string) error {
			if op == memstore.OpGet && key == headKey() {
				return fmt.Errorf("get %q: %w", key, fs.ErrNotExist)
			}
			return nil
		}
		e.putTombstoneOnGet(1)

		err := e.core.Pull(t.Context(), plan, syncore.PullOptions{})
		if !errors.Is(err, syncore.ErrRemoteDeleted) {
			t.Fatalf("err = %v, want ErrRemoteDeleted", err)
		}
	})

	t.Run("8-T7 上書きで中身が同じときも、承認後に tombstone が書かれたら base を記録せずに止める", func(t *testing.T) {
		e := newEnv(t)
		files := map[string]string{"slot1.sav": "same"}
		writeFiles(t, e.saveDir, files)
		e.putRemoteFiles(t, files)
		plan := e.planWant(t, syncore.DecisionNoChange)
		e.putTombstoneOnGet(1)

		err := e.core.Push(t.Context(), plan, syncore.PushOptions{Overwrite: true})
		if !errors.Is(err, syncore.ErrRemoteDeleted) {
			t.Fatalf("err = %v, want ErrRemoteDeleted", err)
		}
		if base := e.base(t); base != "" {
			t.Errorf("base = %q, want 空", base)
		}
	})

	t.Run("8-T8 記録のあるゲームは、上書きでも Push・Pull を拒否する", func(t *testing.T) {
		e := newEnv(t)
		writeFiles(t, e.saveDir, map[string]string{"slot1.sav": "local"})
		e.putRemoteFiles(t, map[string]string{"slot1.sav": "remote"})
		e.recordTombstone(t, testTombstone)
		plan, err := e.status(t)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}

		if err := e.core.Push(t.Context(), plan, syncore.PushOptions{Overwrite: true}); !errors.Is(err, syncore.ErrRemoteDeleted) {
			t.Errorf("Push: err = %v, want ErrRemoteDeleted", err)
		}
		if err := e.core.Pull(t.Context(), plan, syncore.PullOptions{Overwrite: true}); !errors.Is(err, syncore.ErrRemoteDeleted) {
			t.Errorf("Pull: err = %v, want ErrRemoteDeleted", err)
		}
		if acquired, _ := e.lock.state(); acquired != 0 {
			t.Errorf("同期ロックを %d 回取った", acquired)
		}
		if _, ok := e.getObject(t, headKey()); !ok {
			t.Error("HEAD が消えた")
		}
		if diff := cmp.Diff(map[string]string{"slot1.sav": "local"}, readFiles(t, e.saveDir)); diff != "" {
			t.Errorf("セーブフォルダが変わった (-want +got):\n%s", diff)
		}
	})
}
