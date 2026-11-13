package metasync_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/fuyu28/CloudLaunch_v3/internal/metasync"
	"github.com/fuyu28/CloudLaunch_v3/internal/objstore/memstore"
	"github.com/fuyu28/CloudLaunch_v3/internal/store"
)

const (
	remoteGameA = "0192d4e8-0000-7000-8000-0000000000a1"
	remoteGameB = "0192d4e8-0000-7000-8000-0000000000a2"
	remoteGameC = "0192d4e8-0000-7000-8000-0000000000a4"
	// otherLocalGame は、testGameID のほかにこの PC に登録するゲーム
	otherLocalGame = "0192d4e8-0000-7000-8000-0000000000a3"
	deletedJSON    = `{"deletedAt":"2026-11-03T01:02:03.004Z","deviceName":"Laptop"}`
)

var errInjected = errors.New("injected")

func remoteGameKey(gameID, name string) string {
	return "games/" + gameID + "/" + name
}

// fullGameJSON は、brand と imageSha256 を持ち、createdAt と updatedAt が違う game.json を返す。
func fullGameJSON(gameID, title string) string {
	return fmt.Sprintf(`{"id":%q,"title":%q,"brand":"Brand","imageSha256":%q,"createdAt":%q,"updatedAt":%q}`,
		gameID, title, testImage, olderStamp, newerStamp)
}

func fullGameInfo(t *testing.T, gameID, title string) metasync.GameInfo {
	t.Helper()
	return metasync.GameInfo{
		ID: gameID, Title: title, Brand: "Brand", ImageSha256: testImage,
		CreatedAt: mustTime(t, olderStamp), UpdatedAt: mustTime(t, newerStamp),
	}
}

// putRemoteGame は、game.json と、セーブの HEAD をリモートに置く。HEAD は、ゲームの接頭辞の下に game.json 以外のキーも
// あることを表す。
func (e *env) putRemoteGame(t *testing.T, gameID, title string) {
	t.Helper()
	e.putRemote(t, remoteGameKey(gameID, "game.json"), fullGameJSON(gameID, title))
	e.putRemote(t, remoteGameKey(gameID, "HEAD"), "0192d4e8-0000-7000-8000-0000000000ff")
}

// failOn は、PUT・GET の記録を続けたまま、op で key を扱うときに errInjected を返す。
func (e *env) failOn(op memstore.Op, key string) {
	record := e.remote.Before
	e.remote.Before = func(o memstore.Op, k string) error {
		if err := record(o, k); err != nil {
			return err
		}
		if o == op && k == key {
			return errInjected
		}
		return nil
	}
}

// getsUnder は、gameID の接頭辞の下で GET したキーを返す。
func (e *env) getsUnder(gameID string) []string {
	var keys []string
	for _, k := range e.getKeys() {
		if strings.HasPrefix(k, "games/"+gameID+"/") {
			keys = append(keys, k)
		}
	}
	return keys
}

func TestRemoteGames(t *testing.T) {
	ctx := context.Background()

	t.Run("8-N1 リモートのゲームを game.json とともに一覧する", func(t *testing.T) {
		e := newBareEnv(t)
		e.putRemoteGame(t, remoteGameB, "B")
		e.putRemoteGame(t, remoteGameA, "A")

		got, err := e.core.RemoteGames(ctx)
		if err != nil {
			t.Fatalf("RemoteGames: %v", err)
		}
		want := metasync.RemoteGames{Games: []metasync.GameInfo{fullGameInfo(t, remoteGameA, "A"), fullGameInfo(t, remoteGameB, "B")}}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("RemoteGames (-want +got):\n%s", diff)
		}
	})

	t.Run("8-N5 登録済みのゲームは、リモートを読まずに除く", func(t *testing.T) {
		e := newBareEnv(t)
		if err := e.store.CreateGame(ctx, store.NewGame{
			ID: otherLocalGame, Title: "Other", SaveFolderPath: "/save2", ExePath: "/game2.exe", CreatedAt: testPlayedAt,
		}); err != nil {
			t.Fatalf("CreateGame: %v", err)
		}
		// ゲームの tombstone の記録があるゲームも、登録済みとして除く
		markRemoteDeleted(t, e.dbPath)
		e.putRemoteGame(t, testGameID, "Game")
		e.putRemoteGame(t, otherLocalGame, "Other")

		got, err := e.core.RemoteGames(ctx)
		if err != nil {
			t.Fatalf("RemoteGames: %v", err)
		}
		if diff := cmp.Diff(metasync.RemoteGames{}, got); diff != "" {
			t.Errorf("RemoteGames (-want +got):\n%s", diff)
		}
		if gets := e.getKeys(); len(gets) != 0 {
			t.Errorf("GET した: %v", gets)
		}
	})

	t.Run("8-N6 gameId が UUIDv7 の形でない接頭辞は、リモートを読まずに除く", func(t *testing.T) {
		e := newBareEnv(t)
		for _, id := range []string{"not-a-uuid", "0192d4e8-0000-4000-8000-0000000000a1", strings.ToUpper(remoteGameA)} {
			e.putRemote(t, remoteGameKey(id, "game.json"), fullGameJSON(id, "X"))
		}

		got, err := e.core.RemoteGames(ctx)
		if err != nil {
			t.Fatalf("RemoteGames: %v", err)
		}
		if diff := cmp.Diff(metasync.RemoteGames{Skipped: 3}, got); diff != "" {
			t.Errorf("RemoteGames (-want +got):\n%s", diff)
		}
		if gets := e.getKeys(); len(gets) != 0 {
			t.Errorf("GET した: %v", gets)
		}
	})

	t.Run("8-N7 ゲームの tombstone があれば、game.json を読まずに除く", func(t *testing.T) {
		e := newBareEnv(t)
		e.putRemoteGame(t, remoteGameA, "A")
		e.putRemote(t, remoteGameKey(remoteGameA, "deleted.json"), deletedJSON)

		got, err := e.core.RemoteGames(ctx)
		if err != nil {
			t.Fatalf("RemoteGames: %v", err)
		}
		if diff := cmp.Diff(metasync.RemoteGames{}, got); diff != "" {
			t.Errorf("RemoteGames (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]string{remoteGameKey(remoteGameA, "deleted.json")}, e.getKeys()); diff != "" {
			t.Errorf("GET したキー (-want +got):\n%s", diff)
		}
	})

	for _, tt := range []struct {
		name string
		body string
	}{
		{"8-N8 ゲームの tombstone が JSON として読めなければ（8-T4）、除く", `{`},
		{"8-N8 ゲームの tombstone の deletedAt がなければ（8-T5）、除く", `{"deviceName":"Laptop"}`},
		{"8-N8 ゲームの tombstone の deviceName が空なら（8-T5）、除く", `{"deletedAt":"2026-11-03T01:02:03.004Z","deviceName":""}`},
		{"8-N8 ゲームの tombstone の deletedAt が日時の形でなければ（8-T6）、除く", `{"deletedAt":"2026-11-03","deviceName":"Laptop"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newBareEnv(t)
			e.putRemoteGame(t, remoteGameA, "A")
			e.putRemote(t, remoteGameKey(remoteGameA, "deleted.json"), tt.body)

			got, err := e.core.RemoteGames(ctx)
			if err != nil {
				t.Fatalf("RemoteGames: %v", err)
			}
			if diff := cmp.Diff(metasync.RemoteGames{Skipped: 1}, got); diff != "" {
				t.Errorf("RemoteGames (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff([]string{remoteGameKey(remoteGameA, "deleted.json")}, e.getKeys()); diff != "" {
				t.Errorf("GET したキー (-want +got):\n%s", diff)
			}
		})
	}

	for _, tt := range []struct {
		name string
		// body が空なら、game.json を置かない
		body string
	}{
		{"8-N9 game.json がなければ、除く", ""},
		{"8-N9 game.json が JSON として読めなければ（8-V7）、除く", `{`},
		{"8-N9 game.json の id がキーのゲームと違えば（8-V8）、除く", fullGameJSON(remoteGameB, "A")},
		{"8-N9 game.json の title が空なら（8-V9）、除く", fullGameJSON(remoteGameA, "")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newBareEnv(t)
			e.putRemote(t, remoteGameKey(remoteGameA, "HEAD"), "0192d4e8-0000-7000-8000-0000000000ff")
			if tt.body != "" {
				e.putRemote(t, remoteGameKey(remoteGameA, "game.json"), tt.body)
			}

			got, err := e.core.RemoteGames(ctx)
			if err != nil {
				t.Fatalf("RemoteGames: %v", err)
			}
			if diff := cmp.Diff(metasync.RemoteGames{Skipped: 1}, got); diff != "" {
				t.Errorf("RemoteGames (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("8-N10 除いたゲームの件数を返す", func(t *testing.T) {
		e := newBareEnv(t)
		// 登録済みのゲームと、ゲームの tombstone があるゲームは、取り込めないゲームに数えない
		e.putRemoteGame(t, testGameID, "Game")
		e.putRemoteGame(t, remoteGameB, "B")
		e.putRemote(t, remoteGameKey(remoteGameB, "deleted.json"), deletedJSON)
		e.putRemoteGame(t, remoteGameA, "A")
		e.putRemote(t, remoteGameKey("not-a-uuid", "game.json"), "{}")     // 8-N6
		e.putRemote(t, remoteGameKey(otherLocalGame, "deleted.json"), `{`) // 8-N8
		e.putRemote(t, remoteGameKey(remoteGameC, "game.json"), `{`)       // 8-N9

		got, err := e.core.RemoteGames(ctx)
		if err != nil {
			t.Fatalf("RemoteGames: %v", err)
		}
		want := metasync.RemoteGames{Games: []metasync.GameInfo{fullGameInfo(t, remoteGameA, "A")}, Skipped: 3}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("RemoteGames (-want +got):\n%s", diff)
		}
	})

	for _, tt := range []struct {
		name string
		op   memstore.Op
		key  string
	}{
		{"8-N11 games/ の一覧に失敗したら、エラーにする", memstore.OpList, "games/"},
		{"8-N11 ゲームの tombstone の読み込みに失敗したら、一覧の全体をエラーにする", memstore.OpGet, remoteGameKey(remoteGameB, "deleted.json")},
		{"8-N11 game.json の読み込みに失敗したら、一覧の全体をエラーにする", memstore.OpGet, remoteGameKey(remoteGameB, "game.json")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newBareEnv(t)
			e.putRemoteGame(t, remoteGameA, "A")
			e.putRemoteGame(t, remoteGameB, "B")
			e.failOn(tt.op, tt.key)

			if _, err := e.core.RemoteGames(ctx); !errors.Is(err, errInjected) {
				t.Errorf("RemoteGames = %v, want %v", err, errInjected)
			}
		})
	}
}

func TestImportGame(t *testing.T) {
	ctx := context.Background()

	t.Run("8-N3 game.json の全項目と時刻を保ったまま登録する", func(t *testing.T) {
		e := newBareEnv(t)
		e.putRemoteGame(t, remoteGameA, "A")

		got, err := e.core.ImportGame(ctx, remoteGameA, "/saveA", "/gameA.exe")
		if err != nil {
			t.Fatalf("ImportGame: %v", err)
		}
		want := fullGameInfo(t, remoteGameA, "A")
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("ImportGame (-want +got):\n%s", diff)
		}
		stored, err := e.store.GameInfo(ctx, remoteGameA)
		if err != nil {
			t.Fatalf("GameInfo: %v", err)
		}
		if diff := cmp.Diff(want, stored); diff != "" {
			t.Errorf("DB のゲーム情報 (-want +got):\n%s", diff)
		}
		local, err := e.store.GameLocal(ctx, remoteGameA)
		if err != nil {
			t.Fatalf("GameLocal: %v", err)
		}
		if local.SaveFolderPath != "/saveA" || local.ExePath != "/gameA.exe" {
			t.Errorf("GameLocal = %+v, want /saveA and /gameA.exe", local)
		}
	})

	t.Run("8-N4 登録した後は base がない", func(t *testing.T) {
		e := newBareEnv(t)
		e.putRemoteGame(t, remoteGameA, "A")

		if _, err := e.core.ImportGame(ctx, remoteGameA, "/saveA", "/gameA.exe"); err != nil {
			t.Fatalf("ImportGame: %v", err)
		}
		local, err := e.store.GameLocal(ctx, remoteGameA)
		if err != nil {
			t.Fatalf("GameLocal: %v", err)
		}
		if local.BaseManifestHash != "" {
			t.Errorf("base = %q, want none", local.BaseManifestHash)
		}
	})

	t.Run("8-N12 登録の直前に読み直した game.json で登録する", func(t *testing.T) {
		e := newBareEnv(t)
		e.putRemoteGame(t, remoteGameA, "Old")
		if _, err := e.core.RemoteGames(ctx); err != nil {
			t.Fatalf("RemoteGames: %v", err)
		}
		e.putRemote(t, remoteGameKey(remoteGameA, "game.json"), fullGameJSON(remoteGameA, "New"))

		got, err := e.core.ImportGame(ctx, remoteGameA, "/saveA", "/gameA.exe")
		if err != nil {
			t.Fatalf("ImportGame: %v", err)
		}
		if got.Title != "New" {
			t.Errorf("title = %q, want New", got.Title)
		}
	})

	for _, tt := range []struct {
		name    string
		deleted string
		// game は game.json の本文。空なら置かない
		game string
	}{
		{"8-N12 登録の直前にゲームの tombstone があれば、登録しない", deletedJSON, fullGameJSON(remoteGameA, "A")},
		{"8-N12 登録の直前にゲームの tombstone が不正なら、登録しない", `{`, fullGameJSON(remoteGameA, "A")},
		{"8-N12 登録の直前に game.json がなければ、登録しない", "", ""},
		{"8-N12 登録の直前に game.json が不正なら、登録しない", "", fullGameJSON(remoteGameB, "A")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newBareEnv(t)
			if tt.deleted != "" {
				e.putRemote(t, remoteGameKey(remoteGameA, "deleted.json"), tt.deleted)
			}
			if tt.game != "" {
				e.putRemote(t, remoteGameKey(remoteGameA, "game.json"), tt.game)
			}

			if _, err := e.core.ImportGame(ctx, remoteGameA, "/saveA", "/gameA.exe"); err == nil {
				t.Fatal("ImportGame = nil, want error")
			}
			if _, err := e.store.GameInfo(ctx, remoteGameA); err == nil {
				t.Error("登録した")
			}
		})
	}

	t.Run("8-N12 登録の直前に読めなければ、登録しない", func(t *testing.T) {
		e := newBareEnv(t)
		e.putRemoteGame(t, remoteGameA, "A")
		e.failOn(memstore.OpGet, remoteGameKey(remoteGameA, "game.json"))

		if _, err := e.core.ImportGame(ctx, remoteGameA, "/saveA", "/gameA.exe"); !errors.Is(err, errInjected) {
			t.Errorf("ImportGame = %v, want %v", err, errInjected)
		}
		if _, err := e.store.GameInfo(ctx, remoteGameA); err == nil {
			t.Error("登録した")
		}
	})

	t.Run("8-N12 登録のときには、画像とプレイ履歴を取得しない", func(t *testing.T) {
		e := newBareEnv(t)
		e.putRemoteGame(t, remoteGameA, "A")
		e.putRemote(t, remoteGameKey(remoteGameA, "images/"+testImage), "image")
		e.putRemote(t, remoteGameKey(remoteGameA, "sessions/"+sessionA+".json"), sessionJSON(sessionA, remoteGameA, 60))

		if _, err := e.core.ImportGame(ctx, remoteGameA, "/saveA", "/gameA.exe"); err != nil {
			t.Fatalf("ImportGame: %v", err)
		}
		want := []string{remoteGameKey(remoteGameA, "deleted.json"), remoteGameKey(remoteGameA, "game.json")}
		if diff := cmp.Diff(want, e.getKeys()); diff != "" {
			t.Errorf("GET したキー (-want +got):\n%s", diff)
		}
		sessions, err := e.store.Sessions(ctx, remoteGameA)
		if err != nil {
			t.Fatalf("Sessions: %v", err)
		}
		if len(sessions) != 0 {
			t.Errorf("プレイ履歴を取り込んだ: %v", sessions)
		}
		if files := e.cacheFiles(t); len(files) != 0 {
			t.Errorf("画像をキャッシュに入れた: %v", files)
		}
	})

	t.Run("8章/取り込み/登録済みのゲームは、リモートを読まずにエラーにする", func(t *testing.T) {
		e := newBareEnv(t)
		e.putRemoteGame(t, testGameID, "Remote")

		if _, err := e.core.ImportGame(ctx, testGameID, "/saveA", "/gameA.exe"); err == nil {
			t.Fatal("ImportGame = nil, want error")
		}
		if gets := e.getsUnder(testGameID); len(gets) != 0 {
			t.Errorf("GET した: %v", gets)
		}
		if g := e.localGameInfo(t); g.Title != "Game" {
			t.Errorf("title = %q, want Game", g.Title)
		}
	})

	t.Run("8章/取り込み/gameId が UUIDv7 の形でなければ、リモートを読まずにエラーにする", func(t *testing.T) {
		e := newBareEnv(t)
		e.putRemoteGame(t, "not-a-uuid", "X")

		if _, err := e.core.ImportGame(ctx, "not-a-uuid", "/saveA", "/gameA.exe"); err == nil {
			t.Fatal("ImportGame = nil, want error")
		}
		if gets := e.getKeys(); len(gets) != 0 {
			t.Errorf("GET した: %v", gets)
		}
	})
}
