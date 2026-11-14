package metasync_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/fuyu28/CloudLaunch_v3/internal/metasync"
	"github.com/fuyu28/CloudLaunch_v3/internal/objstore/memstore"
)

// remoteTombstone は deletedJSON の値。recordedTombstone は markRemoteDeleted が DB に書く値で、リモートの値と区別できる。
var (
	remoteTombstone   = metasync.Tombstone{DeletedAt: testPlayedAt, DeviceName: "Laptop"}
	recordedTombstone = metasync.Tombstone{DeletedAt: time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC), DeviceName: "Laptop"}
)

var invalidTombstones = []struct {
	name string
	body string
}{
	{"8-T4 JSON として読めない", `{`},
	{"8-T4 64 KiB を超える", deletedJSON + strings.Repeat(" ", 64<<10)},
	{"8-T5 deletedAt がない", `{"deviceName":"Laptop"}`},
	{"8-T5 deviceName が空", `{"deletedAt":"2026-11-03T01:02:03.004Z","deviceName":""}`},
	{"8-T6 deletedAt が日時の形でない", `{"deletedAt":"2026-11-03","deviceName":"Laptop"}`},
}

// putGameData は、testGameID の配下にゲームの tombstone 以外のデータを、remoteGameA の配下にほかのゲームのデータを置く。
func (e *env) putGameData(t *testing.T) {
	t.Helper()
	e.putRemoteGame(t, testGameID, "Game")
	e.putRemote(t, remoteGameKey(testGameID, "objects/"+testImage), "save")
	e.putRemoteSession(t, sessionA, 60)
	e.putRemoteGame(t, remoteGameA, "Other")
}

func (e *env) remoteKeys(t *testing.T) []string {
	t.Helper()
	keys, err := e.remote.List(context.Background(), "games/")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	return keys
}

// recorded は、DB のゲームの tombstone の記録を返す。
func (e *env) recorded(t *testing.T) (metasync.Tombstone, bool) {
	t.Helper()
	ts, ok, err := e.store.RemoteDeleted(context.Background(), testGameID)
	if err != nil {
		t.Fatalf("RemoteDeleted: %v", err)
	}
	return ts, ok
}

func assertTombstone(t *testing.T, got metasync.Tombstone, ok bool, err error, want metasync.Tombstone, wantOK bool) {
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

func TestCheckTombstone(t *testing.T) {
	ctx := context.Background()

	t.Run("8-T1 deleted.json がなければ、削除されていないと返す", func(t *testing.T) {
		e := newBareEnv(t)
		e.putGameData(t)

		got, ok, err := e.core.CheckTombstone(ctx, testGameID)
		assertTombstone(t, got, ok, err, metasync.Tombstone{}, false)
	})

	t.Run("8-T2 status 用の確認は、記録も削除もしない", func(t *testing.T) {
		e := newBareEnv(t)
		e.putGameData(t)
		e.putRemote(t, remoteGameKey(testGameID, "deleted.json"), deletedJSON)
		before := e.remoteKeys(t)

		got, ok, err := e.core.CheckTombstone(ctx, testGameID)
		assertTombstone(t, got, ok, err, remoteTombstone, true)
		if _, rec := e.recorded(t); rec {
			t.Error("DB に記録した")
		}
		if diff := cmp.Diff(before, e.remoteKeys(t)); diff != "" {
			t.Errorf("リモートのキーが変わった (-before +after):\n%s", diff)
		}
	})

	t.Run("8-T3 deleted.json を読めなければ、エラーにする", func(t *testing.T) {
		e := newBareEnv(t)
		e.failOn(memstore.OpGet, remoteGameKey(testGameID, "deleted.json"))

		if _, _, err := e.core.CheckTombstone(ctx, testGameID); err == nil {
			t.Error("エラーを返さなかった")
		}
	})

	for _, tc := range invalidTombstones {
		t.Run(tc.name+"とき、エラーにする", func(t *testing.T) {
			e := newBareEnv(t)
			e.putRemote(t, remoteGameKey(testGameID, "deleted.json"), tc.body)

			if _, _, err := e.core.CheckTombstone(ctx, testGameID); err == nil {
				t.Error("エラーを返さなかった")
			}
		})
	}

	t.Run("8-T8 記録があれば、status 用の確認はリモートを読まずに記録を返す", func(t *testing.T) {
		e := newBareEnv(t)
		markRemoteDeleted(t, e.dbPath)

		got, ok, err := e.core.CheckTombstone(ctx, testGameID)
		assertTombstone(t, got, ok, err, recordedTombstone, true)
		if gets := e.getKeys(); len(gets) != 0 {
			t.Errorf("リモートを読んだ: %v", gets)
		}
	})
}

func TestHandleTombstone(t *testing.T) {
	ctx := context.Background()
	tombstoneKey := remoteGameKey(testGameID, "deleted.json")
	// afterCleanup は、putGameData と deleted.json を置いた後に掃除したときに残るキー
	afterCleanup := []string{
		tombstoneKey,
		remoteGameKey(remoteGameA, "HEAD"),
		remoteGameKey(remoteGameA, "game.json"),
	}

	t.Run("8-T1 deleted.json がなければ、削除されていないと返し、何も削除しない", func(t *testing.T) {
		e := newBareEnv(t)
		e.putGameData(t)
		before := e.remoteKeys(t)

		got, ok, err := e.core.HandleTombstone(ctx, testGameID)
		assertTombstone(t, got, ok, err, metasync.Tombstone{}, false)
		if diff := cmp.Diff(before, e.remoteKeys(t)); diff != "" {
			t.Errorf("リモートのキーが変わった (-before +after):\n%s", diff)
		}
		if _, rec := e.recorded(t); rec {
			t.Error("DB に記録した")
		}
	})

	t.Run("8-T2 deleted.json があれば、DB に記録し、tombstone 以外を削除する", func(t *testing.T) {
		e := newBareEnv(t)
		e.putGameData(t)
		e.putRemote(t, tombstoneKey, deletedJSON)

		got, ok, err := e.core.HandleTombstone(ctx, testGameID)
		assertTombstone(t, got, ok, err, remoteTombstone, true)
		rec, recOK := e.recorded(t)
		if !recOK {
			t.Fatal("DB に記録しなかった")
		}
		if diff := cmp.Diff(remoteTombstone, rec); diff != "" {
			t.Errorf("記録 (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(afterCleanup, e.remoteKeys(t)); diff != "" {
			t.Errorf("残ったキー (-want +got):\n%s", diff)
		}
	})

	t.Run("8-T2 削除の途中で失敗しても、次の確認で消し残りを削除する", func(t *testing.T) {
		e := newBareEnv(t)
		e.putGameData(t)
		e.putRemote(t, tombstoneKey, deletedJSON)
		record := e.remote.Before
		e.failOn(memstore.OpDelete, remoteGameKey(testGameID, "game.json"))

		got, ok, err := e.core.HandleTombstone(ctx, testGameID)
		if err == nil {
			t.Fatal("エラーを返さなかった")
		}
		// 削除に失敗しても、記録したので「リモートで削除されました」と表示させる
		if !ok || got != remoteTombstone {
			t.Errorf("tombstone = %+v, %v, want %+v, true", got, ok, remoteTombstone)
		}
		if _, rec := e.recorded(t); !rec {
			t.Error("削除の前に DB に記録しなかった")
		}

		e.remote.Before = record
		got, ok, err = e.core.HandleTombstone(ctx, testGameID)
		assertTombstone(t, got, ok, err, remoteTombstone, true)
		if diff := cmp.Diff(afterCleanup, e.remoteKeys(t)); diff != "" {
			t.Errorf("残ったキー (-want +got):\n%s", diff)
		}
	})

	t.Run("8-T2 記録のあるゲームでも、消し残りを削除し、記録は書き換えずに記録を返す（8-X10）", func(t *testing.T) {
		e := newBareEnv(t)
		e.putGameData(t)
		e.putRemote(t, tombstoneKey, deletedJSON)
		markRemoteDeleted(t, e.dbPath)

		got, ok, err := e.core.HandleTombstone(ctx, testGameID)
		assertTombstone(t, got, ok, err, recordedTombstone, true)
		rec, _ := e.recorded(t)
		if diff := cmp.Diff(recordedTombstone, rec); diff != "" {
			t.Errorf("記録 (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(afterCleanup, e.remoteKeys(t)); diff != "" {
			t.Errorf("残ったキー (-want +got):\n%s", diff)
		}
	})

	t.Run("8-T3 deleted.json を読めなければ、エラーにし、記録も削除もしない", func(t *testing.T) {
		e := newBareEnv(t)
		e.putGameData(t)
		e.putRemote(t, tombstoneKey, deletedJSON)
		before := e.remoteKeys(t)
		e.failOn(memstore.OpGet, tombstoneKey)

		if _, _, err := e.core.HandleTombstone(ctx, testGameID); err == nil {
			t.Error("エラーを返さなかった")
		}
		if _, rec := e.recorded(t); rec {
			t.Error("DB に記録した")
		}
		if diff := cmp.Diff(before, e.remoteKeys(t)); diff != "" {
			t.Errorf("リモートのキーが変わった (-before +after):\n%s", diff)
		}
	})

	for _, tc := range invalidTombstones {
		t.Run(tc.name+"とき、エラーにし、記録も削除もしない", func(t *testing.T) {
			e := newBareEnv(t)
			e.putGameData(t)
			e.putRemote(t, tombstoneKey, tc.body)
			before := e.remoteKeys(t)

			if _, _, err := e.core.HandleTombstone(ctx, testGameID); err == nil {
				t.Error("エラーを返さなかった")
			}
			if _, rec := e.recorded(t); rec {
				t.Error("DB に記録した")
			}
			if diff := cmp.Diff(before, e.remoteKeys(t)); diff != "" {
				t.Errorf("リモートのキーが変わった (-before +after):\n%s", diff)
			}
		})
	}

	recordedCases := []struct {
		name    string
		prepare func(t *testing.T, e *env)
	}{
		{"8-T8 記録があり deleted.json がなければ、何も削除せずに記録を返す", func(*testing.T, *env) {}},
		{"8-T8 記録があれば、deleted.json を読めなくても、エラーにせず何も削除せずに記録を返す", func(t *testing.T, e *env) {
			e.putRemote(t, tombstoneKey, deletedJSON)
			e.failOn(memstore.OpGet, tombstoneKey)
		}},
		{"8-T8 記録があれば、deleted.json が不正でも、エラーにせず何も削除せずに記録を返す", func(t *testing.T, e *env) {
			e.putRemote(t, tombstoneKey, `{`)
		}},
	}
	for _, tc := range recordedCases {
		t.Run(tc.name, func(t *testing.T) {
			e := newBareEnv(t)
			e.putGameData(t)
			markRemoteDeleted(t, e.dbPath)
			tc.prepare(t, e)
			before := e.remoteKeys(t)

			got, ok, err := e.core.HandleTombstone(ctx, testGameID)
			assertTombstone(t, got, ok, err, recordedTombstone, true)
			if diff := cmp.Diff(before, e.remoteKeys(t)); diff != "" {
				t.Errorf("リモートのキーが変わった (-before +after):\n%s", diff)
			}
		})
	}
}

func TestDeleteRemoteGame(t *testing.T) {
	ctx := context.Background()
	tombstoneKey := remoteGameKey(testGameID, "deleted.json")
	afterDelete := []string{
		tombstoneKey,
		remoteGameKey(remoteGameA, "HEAD"),
		remoteGameKey(remoteGameA, "game.json"),
	}

	t.Run("8-X5 deleted.json を PUT し、DB にゲームの tombstone を記録する", func(t *testing.T) {
		e := newBareEnv(t)
		e.putGameData(t)

		if err := e.core.DeleteRemoteGame(ctx, testGameID, remoteTombstone); err != nil {
			t.Fatalf("DeleteRemoteGame: %v", err)
		}
		if body := e.remoteBody(t, tombstoneKey); body != deletedJSON {
			t.Errorf("deleted.json = %s, want %s", body, deletedJSON)
		}
		rec, recOK := e.recorded(t)
		if !recOK {
			t.Fatal("DB に記録しなかった")
		}
		if diff := cmp.Diff(remoteTombstone, rec); diff != "" {
			t.Errorf("記録 (-want +got):\n%s", diff)
		}
	})

	t.Run("8-X6 配下のゲームの tombstone 以外を削除し、ほかのゲームのキーは残す", func(t *testing.T) {
		e := newBareEnv(t)
		e.putGameData(t)

		if err := e.core.DeleteRemoteGame(ctx, testGameID, remoteTombstone); err != nil {
			t.Fatalf("DeleteRemoteGame: %v", err)
		}
		if diff := cmp.Diff(afterDelete, e.remoteKeys(t)); diff != "" {
			t.Errorf("残ったキー (-want +got):\n%s", diff)
		}
	})

	t.Run("8-X9 deleted.json の PUT に失敗したら、記録も削除もしない", func(t *testing.T) {
		e := newBareEnv(t)
		e.putGameData(t)
		before := e.remoteKeys(t)
		e.failOn(memstore.OpPut, tombstoneKey)

		err := e.core.DeleteRemoteGame(ctx, testGameID, remoteTombstone)
		if !errors.Is(err, errInjected) || errors.Is(err, metasync.ErrDeleteIncomplete) {
			t.Fatalf("err = %v, want ErrDeleteIncomplete でない元のエラー", err)
		}
		if _, rec := e.recorded(t); rec {
			t.Error("DB に記録した")
		}
		if diff := cmp.Diff(before, e.remoteKeys(t)); diff != "" {
			t.Errorf("リモートのキーが変わった (-before +after):\n%s", diff)
		}
	})

	t.Run("8-X10 PUT の後で配下の削除に失敗したら、記録を残し、次の tombstone の確認で消し残りを削除する", func(t *testing.T) {
		e := newBareEnv(t)
		e.putGameData(t)
		record := e.remote.Before
		e.failOn(memstore.OpDelete, remoteGameKey(testGameID, "game.json"))

		err := e.core.DeleteRemoteGame(ctx, testGameID, remoteTombstone)
		if !errors.Is(err, metasync.ErrDeleteIncomplete) || !errors.Is(err, errInjected) {
			t.Fatalf("err = %v, want ErrDeleteIncomplete で包んだ元のエラー", err)
		}
		if _, rec := e.recorded(t); !rec {
			t.Error("DB に記録しなかった")
		}
		if body := e.remoteBody(t, tombstoneKey); body != deletedJSON {
			t.Errorf("deleted.json = %s, want %s", body, deletedJSON)
		}

		e.remote.Before = record
		got, ok, err := e.core.HandleTombstone(ctx, testGameID)
		assertTombstone(t, got, ok, err, remoteTombstone, true)
		if diff := cmp.Diff(afterDelete, e.remoteKeys(t)); diff != "" {
			t.Errorf("残ったキー (-want +got):\n%s", diff)
		}
	})
}
