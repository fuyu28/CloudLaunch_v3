// ゲーム情報の同期（仕様書 8 章「ゲーム情報」）。game.json を文書全体の updatedAt の後勝ちで同期する。
package metasync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"time"
)

// maxGameInfoSize は、読む game.json の本文の上限。正しい本文は 1 KiB に満たないので、超えるものは壊れた object として
// 8-V7 で扱う。
const maxGameInfoSize = 64 << 10

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ValidSHA256 は、2 章の sha256 の表記（小文字の 16 進 64 文字）に当たる。
func ValidSHA256(s string) bool {
	return sha256Pattern.MatchString(s)
}

func gameKey(gameID string) string {
	return "games/" + gameID + "/game.json"
}

// gameInfoJSON の項目の順が、PUT する本文の項目の順になる。未設定の brand・imageSha256 は項目ごと省く。
type gameInfoJSON struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Brand       string `json:"brand,omitempty"`
	ImageSha256 string `json:"imageSha256,omitempty"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

// syncGameInfo は、game.json と、それが指す画像を同期する。GET・PUT・DB やキャッシュの書き込みの失敗だけをエラーにする。
// 不正な game.json（8-V7〜8-V11・8-V15）は、ログに残して GameInfoInvalid を返し、DB もリモートも書き換えず、画像も扱わない。
func (c *Core) syncGameInfo(ctx context.Context, gameID string) (GameInfoOutcome, ImageOutcome, error) {
	local, err := c.cfg.Local.GameInfo(ctx, gameID)
	if err != nil {
		return 0, 0, fmt.Errorf("read local game info: %w", err)
	}
	// current は、game.json の同期の後に DB にあるゲーム情報。その imageSha256 の画像がキャッシュになければ、最後に取得する
	// （8-I3）。8-G3・8-G4 に限らず確かめるので、前の同期で取得できなかった画像も取り直す
	var outcome GameInfoOutcome
	current := local
	upload := false
	body, err := c.getGameJSON(ctx, gameID)
	switch {
	case errors.Is(err, fs.ErrNotExist): // 8-G1
		upload = true
	case err != nil:
		return 0, 0, err
	default:
		remote, err := parseGameInfo(body, gameID)
		if err != nil {
			c.cfg.Logger.Warn("skip invalid remote game.json", "gameId", gameID, "err", err)
			return GameInfoInvalid, ImageNone, nil
		}
		switch {
		case local.UpdatedAt.After(remote.UpdatedAt): // 8-G2
			upload = true
		case remote.UpdatedAt.After(local.UpdatedAt): // 8-G3
			// GET の後にこの PC で編集されていれば書き換わらない。そのときはローカルが新しいので、次の同期で 8-G2 になる
			replaced, err := c.cfg.Local.ReplaceGameInfo(ctx, remote)
			if err != nil {
				return 0, 0, fmt.Errorf("replace game info: %w", err)
			}
			if replaced {
				outcome, current = GameInfoDownloaded, remote
			}
		default: // 8-G4
			if !sameGameInfo(remote, local) {
				c.cfg.Logger.Info("game.json differs but has the same updatedAt", "gameId", gameID, "updatedAt", local.UpdatedAt)
			}
		}
	}
	var uploaded ImageOutcome
	if upload {
		if uploaded, err = c.uploadGameInfo(ctx, local); err != nil {
			return 0, 0, err
		}
		outcome = GameInfoUploaded
		if uploaded == ImageMissing { // 8-I4: 画像はどこにもないので、取得も試さない
			return outcome, uploaded, nil
		}
	}
	img, err := c.cacheRemoteImage(ctx, gameID, current.ImageSha256)
	// PUT した画像はキャッシュから送ったものなので、取得の確かめは ImageUnchanged を返す。結果には PUT したことを出す
	if uploaded == ImageUploaded {
		img = ImageUploaded
	}
	return outcome, img, err
}

// uploadGameInfo は、指す画像をリモートに揃えてから game.json を PUT する（8-I1）。画像の PUT に失敗したら、game.json を
// PUT しない。画像がキャッシュにもリモートにもなければ、imageSha256 を入れたまま game.json を PUT し、ImageMissing を返す
// （8-I4）。画像は必須でないので、画像がないことでゲーム情報の編集の同期を止めない。
func (c *Core) uploadGameInfo(ctx context.Context, g GameInfo) (ImageOutcome, error) {
	img, err := c.uploadImage(ctx, g)
	if err != nil {
		return 0, err
	}
	if err := c.putGameInfo(ctx, g); err != nil {
		return 0, err
	}
	return img, nil
}

// getGameJSON は、game.json がなければ fs.ErrNotExist で包んだエラーを返す。
func (c *Core) getGameJSON(ctx context.Context, gameID string) ([]byte, error) {
	return c.getLimited(ctx, gameKey(gameID), maxGameInfoSize)
}

// getLimited は、キーがなければ fs.ErrNotExist で包んだエラーを返す。limit を超える本文は、limit の 1 バイト先までしか
// 読まないので、呼び出し側は長さで超えたことを判定できる。
func (c *Core) getLimited(ctx context.Context, key string, limit int64) ([]byte, error) {
	r, err := c.cfg.Remote.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", key, err)
	}
	defer func() { _ = r.Close() }()
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", key, err)
	}
	return body, nil
}

func (c *Core) putGameInfo(ctx context.Context, g GameInfo) error {
	body, err := json.Marshal(gameInfoJSON{
		ID:          g.ID,
		Title:       g.Title,
		Brand:       g.Brand,
		ImageSha256: g.ImageSha256,
		CreatedAt:   g.CreatedAt.UTC().Format(timeLayout),
		UpdatedAt:   g.UpdatedAt.UTC().Format(timeLayout),
	})
	if err != nil {
		return fmt.Errorf("encode game.json: %w", err)
	}
	if err := c.cfg.Remote.Put(ctx, gameKey(g.ID), bytes.NewReader(body), int64(len(body))); err != nil {
		return fmt.Errorf("put game.json: %w", err)
	}
	return nil
}

// parseGameInfo は、仕様書 8-V7〜8-V11・8-V15 で検証する。知らない項目は無視する。parseSession と同じ理由で、構造体に
// Unmarshal せず項目ごとに読む。brand・imageSha256 は、ないか null なら未設定とし、brand の空文字も未設定とする。
func parseGameInfo(body []byte, gameID string) (GameInfo, error) {
	if len(body) > maxGameInfoSize {
		return GameInfo{}, fmt.Errorf("8-V7: body exceeds %d bytes", maxGameInfoSize)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return GameInfo{}, fmt.Errorf("8-V7: %w", err)
	}
	var g GameInfo
	var err error
	if g.Brand, err = optionalString(fields["brand"]); err != nil {
		return GameInfo{}, fmt.Errorf("8-V15: brand %s is not a string", fields["brand"])
	}
	if err := unmarshalString(fields, "id", &g.ID); err != nil || g.ID != gameID {
		return GameInfo{}, fmt.Errorf("8-V8: id %s does not match key", fields["id"])
	}
	if err := unmarshalString(fields, "title", &g.Title); err != nil || g.Title == "" {
		return GameInfo{}, fmt.Errorf("8-V9: title %s is missing or empty", fields["title"])
	}
	for _, f := range []struct {
		name string
		dst  *time.Time
	}{{"createdAt", &g.CreatedAt}, {"updatedAt", &g.UpdatedAt}} {
		t, ok := parseTimestamp(fields[f.name])
		if !ok {
			return GameInfo{}, fmt.Errorf("8-V10: %s %s is not in the spec format", f.name, fields[f.name])
		}
		*f.dst = t
	}
	// 空文字を未設定とするのは brand だけで、imageSha256 は値があれば空文字も sha256 の形でないとして拒む
	raw := fields["imageSha256"]
	g.ImageSha256, err = optionalString(raw)
	if err != nil || (raw != nil && string(raw) != "null" && !ValidSHA256(g.ImageSha256)) {
		return GameInfo{}, fmt.Errorf("8-V11: imageSha256 %s is not a sha256", fields["imageSha256"])
	}
	return g, nil
}

// optionalString は、項目がないか null なら空文字を返し、文字列でなければエラーにする。
func optionalString(v json.RawMessage) (string, error) {
	if v == nil {
		return "", nil
	}
	var p *string
	if err := json.Unmarshal(v, &p); err != nil {
		return "", err
	}
	if p == nil {
		return "", nil
	}
	return *p, nil
}

func sameGameInfo(a, b GameInfo) bool {
	return a.ID == b.ID && a.Title == b.Title && a.Brand == b.Brand && a.ImageSha256 == b.ImageSha256 &&
		a.CreatedAt.Equal(b.CreatedAt) && a.UpdatedAt.Equal(b.UpdatedAt)
}

// unmarshalString は、項目がないか null か、文字列でなければエラーにする。
func unmarshalString(fields map[string]json.RawMessage, name string, dst *string) error {
	v, ok := fields[name]
	if !ok || string(v) == "null" {
		return fmt.Errorf("%s is missing or null", name)
	}
	return json.Unmarshal(v, dst)
}
