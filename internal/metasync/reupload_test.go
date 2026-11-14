package metasync_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/fuyu28/CloudLaunch_v3/internal/metasync"
	"github.com/fuyu28/CloudLaunch_v3/internal/store"
	"github.com/fuyu28/CloudLaunch_v3/internal/syncore"
)

const (
	reuploadedGameID = "0192d4e8-0000-7000-8000-0000000000b1"
	reuploadBase     = "4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945"
)

var reuploadedAt = time.Date(2026, 11, 10, 1, 2, 3, 4_000_000, time.UTC)

// newReuploadEnv は、ブランドと画像とプレイ履歴と base を持ち、ゲームの tombstone の記録があるゲームを用意する。
func newReuploadEnv(t *testing.T) *env {
	t.Helper()
	e := newBareEnv(t)
	brand := "Brand"
	if err := e.store.EditGame(context.Background(), testGameID, store.GameEdit{Brand: &brand}, testPlayedAt.Add(time.Hour)); err != nil {
		t.Fatalf("EditGame: %v", err)
	}
	e.setImage(t, testPNG, testPlayedAt.Add(2*time.Hour))
	e.addSession(t, sessionA, 60)
	e.addSession(t, sessionB, 120)
	if err := e.store.SetBase(context.Background(), testGameID, reuploadBase); err != nil {
		t.Fatalf("SetBase: %v", err)
	}
	markRemoteDeleted(t, e.dbPath)
	return e
}

func (e *env) reupload(t *testing.T) (metasync.ReuploadResult, error) {
	t.Helper()
	return e.core.ReuploadGame(context.Background(), testGameID, reuploadedGameID, reuploadedAt)
}

// assertNotReuploaded は、上げ直しが DB を何も変えずに止まったことを確かめる。
func assertNotReuploaded(t *testing.T, e *env) {
	t.Helper()
	ctx := context.Background()
	ids, err := e.store.GameIDs(ctx)
	if err != nil {
		t.Fatalf("GameIDs: %v", err)
	}
	if diff := cmp.Diff([]string{testGameID}, ids); diff != "" {
		t.Errorf("登録したゲーム (-want +got):\n%s", diff)
	}
	if got := e.localSessions(t); len(got) != 2 {
		t.Errorf("古い gameId のプレイ履歴 = %v, want 2 件", got)
	}
}

func TestReuploadGame(t *testing.T) {
	ctx := context.Background()

	t.Run("8-U4 新しい gameId にゲーム情報・ローカル情報・プレイ履歴を移し、base とゲームの tombstone の記録を消す", func(t *testing.T) {
		e := newReuploadEnv(t)

		res, err := e.reupload(t)
		if err != nil {
			t.Fatalf("ReuploadGame: %v", err)
		}
		if res.ImageDropped {
			t.Error("ImageDropped = true, want false")
		}

		info, err := e.store.GameInfo(ctx, reuploadedGameID)
		if err != nil {
			t.Fatalf("GameInfo: %v", err)
		}
		wantInfo := metasync.GameInfo{
			ID: reuploadedGameID, Title: "Game", Brand: "Brand", ImageSha256: testImage,
			CreatedAt: reuploadedAt, UpdatedAt: reuploadedAt,
		}
		if diff := cmp.Diff(wantInfo, info); diff != "" {
			t.Errorf("新しいゲーム情報 (-want +got):\n%s", diff)
		}

		sessions, err := e.store.Sessions(ctx, reuploadedGameID)
		if err != nil {
			t.Fatalf("Sessions: %v", err)
		}
		wantSessions := []metasync.Session{
			{ID: sessionA, GameID: reuploadedGameID, PlayedAt: testPlayedAt, DurationSec: 60},
			{ID: sessionB, GameID: reuploadedGameID, PlayedAt: testPlayedAt, DurationSec: 120},
		}
		if diff := cmp.Diff(wantSessions, sessions); diff != "" {
			t.Errorf("新しい gameId のプレイ履歴 (-want +got):\n%s", diff)
		}

		// base が空なので、上げ直した後は base なしから判定する（4 章）
		local, err := e.store.GameLocal(ctx, reuploadedGameID)
		if err != nil {
			t.Fatalf("GameLocal: %v", err)
		}
		if diff := cmp.Diff(syncore.GameLocal{SaveFolderPath: "/save", ExePath: "/game.exe"}, local); diff != "" {
			t.Errorf("新しいローカル情報 (-want +got):\n%s", diff)
		}
		if _, deleted, err := e.store.RemoteDeleted(ctx, reuploadedGameID); err != nil || deleted {
			t.Errorf("新しい gameId の tombstone の記録 = %v（err %v）, want なし", deleted, err)
		}

		ids, err := e.store.GameIDs(ctx)
		if err != nil {
			t.Fatalf("GameIDs: %v", err)
		}
		if diff := cmp.Diff([]string{reuploadedGameID}, ids); diff != "" {
			t.Errorf("登録したゲーム (-want +got):\n%s", diff)
		}
		if _, err := e.store.GameInfo(ctx, testGameID); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("古い gameId の GameInfo error = %v, want sql.ErrNoRows", err)
		}
	})

	t.Run("8-U4 リモートに触れない", func(t *testing.T) {
		e := newReuploadEnv(t)

		if _, err := e.reupload(t); err != nil {
			t.Fatalf("ReuploadGame: %v", err)
		}
		if puts, gets := e.putKeys(), e.getKeys(); len(puts) != 0 || len(gets) != 0 {
			t.Errorf("PUT %v・GET %v, want なし", puts, gets)
		}
	})

	t.Run("8-U5 画像がキャッシュになければ、imageSha256 を引き継がない", func(t *testing.T) {
		e := newReuploadEnv(t)
		if err := os.Remove(filepath.Join(e.imageDir, testImage)); err != nil {
			t.Fatalf("remove cache: %v", err)
		}

		res, err := e.reupload(t)
		if err != nil {
			t.Fatalf("ReuploadGame: %v", err)
		}
		assertImageDropped(t, e, res)
	})

	t.Run("8-U5 キャッシュの画像の中身が imageSha256 と違えば、引き継がず、ファイルは消さない", func(t *testing.T) {
		e := newReuploadEnv(t)
		path := filepath.Join(e.imageDir, testImage)
		if err := os.WriteFile(path, imageBody(pngMagic, "broken"), 0o600); err != nil {
			t.Fatalf("write cache: %v", err)
		}

		res, err := e.reupload(t)
		if err != nil {
			t.Fatalf("ReuploadGame: %v", err)
		}
		assertImageDropped(t, e, res)
		if _, ok := e.cachedImage(t, testImage); !ok {
			t.Error("壊れたキャッシュのファイルを消した")
		}
	})

	t.Run("8章/上げ直し/ゲームの tombstone の記録がなければ、何も変えずに拒否する", func(t *testing.T) {
		e := newBareEnv(t)
		e.addSession(t, sessionA, 60)
		e.addSession(t, sessionB, 120)

		if _, err := e.reupload(t); !errors.Is(err, metasync.ErrNotRemoteDeleted) {
			t.Fatalf("ReuploadGame error = %v, want ErrNotRemoteDeleted", err)
		}
		assertNotReuploaded(t, e)
	})

	t.Run("8章/上げ直し/新しい gameId が UUIDv7 でなければ、何も変えずに拒否する", func(t *testing.T) {
		e := newReuploadEnv(t)

		for _, id := range []string{"", "not-a-uuid", "0192d4e8-0000-4000-8000-0000000000b1"} {
			if _, err := e.core.ReuploadGame(ctx, testGameID, id, reuploadedAt); err == nil {
				t.Errorf("ReuploadGame(%q) error = nil, want error", id)
			}
		}
		assertNotReuploaded(t, e)
	})

	t.Run("8章/上げ直し/上げ直した後のメタ情報の同期で、画像・game.json・プレイ履歴を新しい gameId の下に PUT する", func(t *testing.T) {
		e := newReuploadEnv(t)
		if _, err := e.reupload(t); err != nil {
			t.Fatalf("ReuploadGame: %v", err)
		}

		got, err := e.core.Sync(ctx, reuploadedGameID)
		if err != nil {
			t.Fatalf("Sync: %v", err)
		}
		want := metasync.Result{GameInfo: metasync.GameInfoUploaded, Image: metasync.ImageUploaded, UploadedSessions: 2}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("Result (-want +got):\n%s", diff)
		}
		wantKeys := []string{
			remoteGameKey(reuploadedGameID, "game.json"),
			remoteGameKey(reuploadedGameID, "images/"+testImage),
			remoteGameKey(reuploadedGameID, "sessions/"+sessionA+".json"),
			remoteGameKey(reuploadedGameID, "sessions/"+sessionB+".json"),
		}
		if diff := cmp.Diff(wantKeys, e.putKeys()); diff != "" {
			t.Errorf("PUT したキー (-want +got):\n%s", diff)
		}
	})
}

func assertImageDropped(t *testing.T, e *env, res metasync.ReuploadResult) {
	t.Helper()
	if !res.ImageDropped {
		t.Error("ImageDropped = false, want true")
	}
	info, err := e.store.GameInfo(context.Background(), reuploadedGameID)
	if err != nil {
		t.Fatalf("GameInfo: %v", err)
	}
	if info.ImageSha256 != "" {
		t.Errorf("新しいゲーム情報の imageSha256 = %q, want 空", info.ImageSha256)
	}
}
