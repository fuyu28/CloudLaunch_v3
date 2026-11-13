package metasync_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/fuyu28/CloudLaunch_v3/internal/metasync"
	"github.com/fuyu28/CloudLaunch_v3/internal/objstore/memstore"
	"github.com/fuyu28/CloudLaunch_v3/internal/store"
)

const (
	gameKey     = "games/" + testGameID + "/game.json"
	testImage   = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	localStamp  = "2026-11-03T01:02:03.004Z"
	olderStamp  = "2026-11-01T00:00:00.000Z"
	newerStamp  = "2026-11-05T00:00:00.000Z"
	remoteTitle = "Remote"
)

// localGameJSON は、newEnvOn が登録するゲームを PUT したときの本文。
const localGameJSON = `{"id":"` + testGameID + `","title":"Game","createdAt":"` + localStamp + `","updatedAt":"` + localStamp + `"}`

// localGameInfo は、newEnvOn が登録するゲームのゲーム情報。
var localGameInfo = metasync.GameInfo{ID: testGameID, Title: "Game", CreatedAt: testPlayedAt, UpdatedAt: testPlayedAt}

// remoteGameJSON は、title を remoteTitle にし、updatedAt を updatedAt にした game.json を返す。
func remoteGameJSON(updatedAt string) string {
	return fmt.Sprintf(`{"id":%q,"title":%q,"createdAt":%q,"updatedAt":%q}`, testGameID, remoteTitle, olderStamp, updatedAt)
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("parse %s: %v", s, err)
	}
	return v
}

func (e *env) localGameInfo(t *testing.T) metasync.GameInfo {
	t.Helper()
	g, err := e.store.GameInfo(context.Background(), testGameID)
	if err != nil {
		t.Fatalf("GameInfo: %v", err)
	}
	return g
}

func (e *env) gamePuts() int {
	n := 0
	for _, key := range e.putKeys() {
		if key == gameKey {
			n++
		}
	}
	return n
}

func TestSyncGameInfo(t *testing.T) {
	ctx := context.Background()

	t.Run("8-G1 リモートに game.json がなければ、ローカルのものを PUT する", func(t *testing.T) {
		e := newBareEnv(t)

		got, err := e.core.Sync(ctx, testGameID)
		if err != nil {
			t.Fatalf("Sync: %v", err)
		}
		if diff := cmp.Diff(metasync.Result{GameInfo: metasync.GameInfoUploaded}, got); diff != "" {
			t.Errorf("Result (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(localGameJSON, e.remoteBody(t, gameKey)); diff != "" {
			t.Errorf("本文 (-want +got):\n%s", diff)
		}
	})

	t.Run("8-G2 ローカルの updatedAt が大きければ、ローカルのものを PUT する", func(t *testing.T) {
		e := newBareEnv(t)
		e.putRemote(t, gameKey, remoteGameJSON(olderStamp))

		got, err := e.core.Sync(ctx, testGameID)
		if err != nil {
			t.Fatalf("Sync: %v", err)
		}
		if diff := cmp.Diff(metasync.Result{GameInfo: metasync.GameInfoUploaded}, got); diff != "" {
			t.Errorf("Result (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(localGameJSON, e.remoteBody(t, gameKey)); diff != "" {
			t.Errorf("本文 (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(localGameInfo, e.localGameInfo(t)); diff != "" {
			t.Errorf("DB のゲーム情報 (-want +got):\n%s", diff)
		}
	})

	t.Run("8-G3 リモートの updatedAt が大きければ、createdAt・updatedAt も含めて DB をリモートのもので置き換える", func(t *testing.T) {
		e := newBareEnv(t)
		remote := fmt.Sprintf(`{"id":%q,"title":"Remote","brand":"Brand","imageSha256":%q,"createdAt":%q,"updatedAt":%q}`,
			testGameID, testImage, olderStamp, newerStamp)
		e.putRemote(t, gameKey, remote)

		got, err := e.core.Sync(ctx, testGameID)
		if err != nil {
			t.Fatalf("Sync: %v", err)
		}
		if diff := cmp.Diff(metasync.Result{GameInfo: metasync.GameInfoDownloaded}, got); diff != "" {
			t.Errorf("Result (-want +got):\n%s", diff)
		}
		want := metasync.GameInfo{
			ID: testGameID, Title: "Remote", Brand: "Brand", ImageSha256: testImage,
			CreatedAt: mustTime(t, olderStamp), UpdatedAt: mustTime(t, newerStamp),
		}
		if diff := cmp.Diff(want, e.localGameInfo(t)); diff != "" {
			t.Errorf("DB のゲーム情報 (-want +got):\n%s", diff)
		}
		if n := e.gamePuts(); n != 0 {
			t.Errorf("game.json を %d 回 PUT した", n)
		}
		if body := e.remoteBody(t, gameKey); body != remote {
			t.Errorf("リモートが書き換わった: %s", body)
		}
	})

	t.Run("8-G4 updatedAt が等しければ、中身が違っても何もしない", func(t *testing.T) {
		e := newBareEnv(t)
		remote := remoteGameJSON(localStamp)
		e.putRemote(t, gameKey, remote)

		got, err := e.core.Sync(ctx, testGameID)
		if err != nil {
			t.Fatalf("Sync: %v", err)
		}
		if diff := cmp.Diff(metasync.Result{GameInfo: metasync.GameInfoUnchanged}, got); diff != "" {
			t.Errorf("Result (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(localGameInfo, e.localGameInfo(t)); diff != "" {
			t.Errorf("DB のゲーム情報 (-want +got):\n%s", diff)
		}
		if n := e.gamePuts(); n != 0 {
			t.Errorf("game.json を %d 回 PUT した", n)
		}
		if body := e.remoteBody(t, gameKey); body != remote {
			t.Errorf("リモートが書き換わった: %s", body)
		}
	})

	// 不正な game.json は、updatedAt をローカルより新しくして置く。検証を通してしまえば 8-G3 で DB が書き換わる
	field := func(name, value string) string { return fmt.Sprintf("%q:%s", name, value) }
	q := func(s string) string { return fmt.Sprintf("%q", s) }
	gameJSONWith := func(fields ...string) string { return "{" + strings.Join(fields, ",") + "}" }
	id, title := field("id", q(testGameID)), field("title", q(remoteTitle))
	created, updated := field("createdAt", q(olderStamp)), field("updatedAt", q(newerStamp))
	invalid := []struct {
		name string
		body string
	}{
		{"8-V7 JSON として読めない game.json は同期しない", `{"id":`},
		{"8-V7 64 KiB を超える game.json は同期しない", remoteGameJSON(newerStamp) + strings.Repeat(" ", 64<<10)},
		{"8-V7 brand が文字列でない game.json は同期しない", gameJSONWith(id, title, field("brand", "5"), created, updated)},
		{"8-V8 id がキーのゲームと違う game.json は同期しない", gameJSONWith(field("id", q("0192d4e8-0000-7000-8000-000000000002")), title, created, updated)},
		{"8-V8 id がない game.json は同期しない", gameJSONWith(title, created, updated)},
		{"8-V8 id が文字列でない game.json は同期しない", gameJSONWith(field("id", "5"), title, created, updated)},
		{"8-V8 項目名の大文字小文字が違う game.json は同期しない", gameJSONWith(field("ID", q(testGameID)), title, created, updated)},
		{"8-V9 title がない game.json は同期しない", gameJSONWith(id, created, updated)},
		{"8-V9 title が null の game.json は同期しない", gameJSONWith(id, field("title", "null"), created, updated)},
		{"8-V9 title が空の game.json は同期しない", gameJSONWith(id, field("title", q("")), created, updated)},
		{"8-V9 title が文字列でない game.json は同期しない", gameJSONWith(id, field("title", "5"), created, updated)},
		{"8-V10 createdAt がない game.json は同期しない", gameJSONWith(id, title, updated)},
		{"8-V10 updatedAt が null の game.json は同期しない", gameJSONWith(id, title, created, field("updatedAt", "null"))},
		{"8-V10 updatedAt が 3 章の日時の形でない game.json は同期しない（時差つき）", gameJSONWith(id, title, created, field("updatedAt", q("2026-11-05T09:00:00.000+09:00")))},
		{"8-V10 updatedAt が 3 章の日時の形でない game.json は同期しない（ミリ秒なし）", gameJSONWith(id, title, created, field("updatedAt", q("2026-11-05T00:00:00Z")))},
		{"8-V10 createdAt が 3 章の日時の形でない game.json は同期しない", gameJSONWith(id, title, field("createdAt", q("2026-11-01")), updated)},
		{"8-V11 imageSha256 が大文字の game.json は同期しない", gameJSONWith(id, title, field("imageSha256", q(strings.ToUpper(testImage))), created, updated)},
		{"8-V11 imageSha256 の長さが違う game.json は同期しない", gameJSONWith(id, title, field("imageSha256", q(testImage[:63])), created, updated)},
		{"8-V11 imageSha256 が空文字の game.json は同期しない", gameJSONWith(id, title, field("imageSha256", q("")), created, updated)},
		{"8-V11 imageSha256 が文字列でない game.json は同期しない", gameJSONWith(id, title, field("imageSha256", "5"), created, updated)},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			e := newBareEnv(t)
			e.putRemote(t, gameKey, tt.body)

			got, err := e.core.Sync(ctx, testGameID)
			if err != nil {
				t.Fatalf("Sync: %v", err)
			}
			if diff := cmp.Diff(metasync.Result{GameInfo: metasync.GameInfoInvalid}, got); diff != "" {
				t.Errorf("Result (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(localGameInfo, e.localGameInfo(t)); diff != "" {
				t.Errorf("DB のゲーム情報 (-want +got):\n%s", diff)
			}
			if n := e.gamePuts(); n != 0 {
				t.Errorf("game.json を %d 回 PUT した", n)
			}
		})
	}

	t.Run("8章/ゲーム情報/PUT する JSON の形", func(t *testing.T) {
		e := newBareEnv(t)
		execDB(t, e.dbPath, `UPDATE games SET brand = ?, image_sha256 = ? WHERE id = ?`, "Brand", testImage, testGameID)

		if _, err := e.core.Sync(ctx, testGameID); err != nil {
			t.Fatalf("Sync: %v", err)
		}
		want := `{"id":"` + testGameID + `","title":"Game","brand":"Brand","imageSha256":"` + testImage +
			`","createdAt":"` + localStamp + `","updatedAt":"` + localStamp + `"}`
		if diff := cmp.Diff(want, e.remoteBody(t, gameKey)); diff != "" {
			t.Errorf("本文 (-want +got):\n%s", diff)
		}
	})

	t.Run("8章/ゲーム情報/brand と imageSha256 が未設定なら、項目を省いて PUT する", func(t *testing.T) {
		e := newBareEnv(t)

		if _, err := e.core.Sync(ctx, testGameID); err != nil {
			t.Fatalf("Sync: %v", err)
		}
		if diff := cmp.Diff(localGameJSON, e.remoteBody(t, gameKey)); diff != "" {
			t.Errorf("本文 (-want +got):\n%s", diff)
		}
	})

	t.Run("8章/ゲーム情報/brand が空文字、imageSha256 が null なら、未設定として取り込む", func(t *testing.T) {
		e := newBareEnv(t)
		execDB(t, e.dbPath, `UPDATE games SET brand = ?, image_sha256 = ? WHERE id = ?`, "Brand", testImage, testGameID)
		e.putRemote(t, gameKey, gameJSONWith(id, title, field("brand", q("")), field("imageSha256", "null"), created, updated))

		got, err := e.core.Sync(ctx, testGameID)
		if err != nil {
			t.Fatalf("Sync: %v", err)
		}
		if diff := cmp.Diff(metasync.Result{GameInfo: metasync.GameInfoDownloaded}, got); diff != "" {
			t.Errorf("Result (-want +got):\n%s", diff)
		}
		want := metasync.GameInfo{ID: testGameID, Title: remoteTitle, CreatedAt: mustTime(t, olderStamp), UpdatedAt: mustTime(t, newerStamp)}
		if diff := cmp.Diff(want, e.localGameInfo(t)); diff != "" {
			t.Errorf("DB のゲーム情報 (-want +got):\n%s", diff)
		}
	})

	t.Run("8章/ゲーム情報/知らない項目があっても取り込む", func(t *testing.T) {
		e := newBareEnv(t)
		e.putRemote(t, gameKey, gameJSONWith(id, title, created, updated, field("note", q("x"))))

		got, err := e.core.Sync(ctx, testGameID)
		if err != nil {
			t.Fatalf("Sync: %v", err)
		}
		if diff := cmp.Diff(metasync.Result{GameInfo: metasync.GameInfoDownloaded}, got); diff != "" {
			t.Errorf("Result (-want +got):\n%s", diff)
		}
		if got := e.localGameInfo(t).Title; got != remoteTitle {
			t.Errorf("title = %q, want %q", got, remoteTitle)
		}
	})

	t.Run("8章/ゲーム情報/取り込んだ後にもう一度同期しても PUT しない", func(t *testing.T) {
		e := newBareEnv(t)
		e.putRemote(t, gameKey, fmt.Sprintf(`{"id":%q,"title":"Remote","brand":"Brand","imageSha256":%q,"createdAt":%q,"updatedAt":%q}`,
			testGameID, testImage, olderStamp, newerStamp))

		if _, err := e.core.Sync(ctx, testGameID); err != nil {
			t.Fatalf("Sync 1: %v", err)
		}
		got, err := e.core.Sync(ctx, testGameID)
		if err != nil {
			t.Fatalf("Sync 2: %v", err)
		}
		if diff := cmp.Diff(metasync.Result{GameInfo: metasync.GameInfoUnchanged}, got); diff != "" {
			t.Errorf("Result (-want +got):\n%s", diff)
		}
		if n := e.gamePuts(); n != 0 {
			t.Errorf("game.json を %d 回 PUT した", n)
		}
	})

	t.Run("8章/ゲーム情報/不正な game.json があっても、プレイ履歴は同期する", func(t *testing.T) {
		e := newBareEnv(t)
		e.putRemote(t, gameKey, `{"id":`)
		e.addSession(t, sessionA, 60)
		e.putRemoteSession(t, sessionB, 0)

		got, err := e.core.Sync(ctx, testGameID)
		if err != nil {
			t.Fatalf("Sync: %v", err)
		}
		want := metasync.Result{GameInfo: metasync.GameInfoInvalid, UploadedSessions: 1, DownloadedSessions: 1}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("Result (-want +got):\n%s", diff)
		}
	})

	for _, op := range []struct {
		name string
		op   memstore.Op
	}{{"GET", memstore.OpGet}, {"PUT", memstore.OpPut}} {
		t.Run("8章/ゲーム情報/game.json の "+op.name+" に失敗したら、プレイ履歴を同期せずにエラーを返す", func(t *testing.T) {
			e := newBareEnv(t)
			e.addSession(t, sessionA, 60)
			errInjected := errors.New("injected")
			e.remote.Before = func(o memstore.Op, key string) error {
				if o == op.op && key == gameKey {
					return errInjected
				}
				if strings.Contains(key, "/sessions/") {
					t.Errorf("プレイ履歴を同期した: op %d, key %s", o, key)
				}
				return nil
			}

			if _, err := e.core.Sync(ctx, testGameID); !errors.Is(err, errInjected) {
				t.Errorf("Sync = %v, want %v", err, errInjected)
			}
		})
	}

	t.Run("8章/ゲーム情報/2 台の PC で、後に編集した方の内容が両方に残る", func(t *testing.T) {
		remote := memstore.New()
		pcA, pcB := newEnvOn(t, remote), newEnvOn(t, remote)
		syncPC := func(pc *env) {
			t.Helper()
			if _, err := pc.core.Sync(ctx, testGameID); err != nil {
				t.Fatalf("Sync: %v", err)
			}
		}
		edit := func(pc *env, title string, now time.Time) {
			t.Helper()
			if err := pc.store.EditGame(ctx, testGameID, store.GameEdit{Title: &title}, now); err != nil {
				t.Fatalf("EditGame: %v", err)
			}
		}
		syncPC(pcA)
		syncPC(pcB)
		edit(pcB, "B", testPlayedAt.Add(time.Hour))
		edit(pcA, "A", testPlayedAt.Add(2*time.Hour))
		syncPC(pcB)
		syncPC(pcA)
		syncPC(pcB)

		for name, pc := range map[string]*env{"A": pcA, "B": pcB} {
			if got := pc.localGameInfo(t).Title; got != "A" {
				t.Errorf("PC %s の title = %q, want %q", name, got, "A")
			}
		}
	})
}
