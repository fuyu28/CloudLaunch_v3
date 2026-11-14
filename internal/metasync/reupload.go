// リモートで削除されたゲームを、新しい gameId のゲームとして上げ直す（仕様書 8 章「リモートで削除されたゲーム」の 8-U4・8-U5）。
// 同期ロックと Pull ジャーナルとゲームの tombstone の記録の確認（8-U2・8-U3）は、呼び出し側（syncore の ReuploadGame）が行う。
package metasync

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

type ReuploadResult struct {
	// ImageDropped は、画像がキャッシュになく、新しいゲーム情報に imageSha256 を引き継がなかったことを表す（8-U5）
	ImageDropped bool
}

// ReuploadGame は、gameID のゲーム情報から newGameID のゲーム情報を作り、プレイ履歴とローカル情報を移し、base とゲームの
// tombstone の記録を消し、古い gameId のゲーム情報を消す。DB の 1 つのトランザクションで行い、失敗したら何も変えない（8-U4）。
// createdAt・updatedAt は now にする。リモートには触れず、次の Sync が新しい gameId の下に PUT する。
func (c *Core) ReuploadGame(ctx context.Context, gameID, newGameID string, now time.Time) (ReuploadResult, error) {
	if !gameIDPattern.MatchString(newGameID) {
		return ReuploadResult{}, fmt.Errorf("new game id is not a UUIDv7: %q", newGameID)
	}
	old, err := c.cfg.Local.GameInfo(ctx, gameID)
	if err != nil {
		return ReuploadResult{}, fmt.Errorf("read game info: %w", err)
	}
	cached, err := c.hasCachedImage(old.ImageSha256)
	if err != nil {
		return ReuploadResult{}, err
	}
	g := GameInfo{ID: newGameID, Title: old.Title, Brand: old.Brand, CreatedAt: now, UpdatedAt: now}
	if cached {
		g.ImageSha256 = old.ImageSha256
	}
	if err := c.cfg.Local.ReuploadGame(ctx, gameID, g); err != nil {
		return ReuploadResult{}, fmt.Errorf("new game %s: %w", newGameID, err)
	}
	return ReuploadResult{ImageDropped: old.ImageSha256 != "" && !cached}, nil
}

// hasCachedImage は、中身の sha256 が sha と一致するファイルがキャッシュにあるときだけ true を返す。一致しないファイルを
// 引き継ぐと、次の同期の uploadImage が壊れたものとして消し、どの PC も取得できない imageSha256 が残るため。一致しない
// ファイルの削除は uploadImage に任せる。
func (c *Core) hasCachedImage(sha string) (bool, error) {
	if sha == "" {
		return false, nil
	}
	body, err := os.ReadFile(c.imagePath(sha))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read cached image: %w", err)
	}
	return sha256Hex(body) == sha, nil
}
