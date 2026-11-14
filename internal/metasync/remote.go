// リモートのキーの形（仕様書 2 章）と、リモートの object の読み方。メタ情報の object は、どれも games/{gameId}/ の下に置く。
package metasync

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"time"
)

const (
	gamesPrefix = "games/"
	timeLayout  = "2006-01-02T15:04:05.000Z"

	transferLimit = 8

	// maxMetaJSONSize は、読む game.json・セッションの本文の上限。正しい本文は 1 KiB に満たないので、超えるものは壊れた
	// object として扱い（8-V1・8-V7）、同期のたびに大きな object を並列でメモリに読まないようにする。
	maxMetaJSONSize = 64 << 10
)

// uuidV7Expr は、UUIDv7 の形（仕様書 2 章の HEAD と同じ）。セッション ID（8-S6）とゲーム ID（8-N6）の検証で同じ形を使う。
const uuidV7Expr = `[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}`

var gameIDPattern = regexp.MustCompile(`^` + uuidV7Expr + `$`)

func gameKey(gameID string) string {
	return gamesPrefix + gameID + "/game.json"
}

func imagesPrefix(gameID string) string {
	return gamesPrefix + gameID + "/images/"
}

func sessionsPrefix(gameID string) string {
	return gamesPrefix + gameID + "/sessions/"
}

func sessionKey(gameID, sessionID string) string {
	return sessionsPrefix(gameID) + sessionID + ".json"
}

// getLimited は、キーがなければ fs.ErrNotExist で包んだエラーを返す。limit を超える本文は、limit の 1 バイト先までしか
// 読まないので、呼び出し側は長さで超えたことを判定できる。Get のエラーはキーを含むので、包まずに返す。
func (c *Core) getLimited(ctx context.Context, key string, limit int64) ([]byte, error) {
	r, err := c.cfg.Remote.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", key, err)
	}
	return body, nil
}

// parseTimestamp は、3 章の日時の形の JSON 文字列だけを受け入れる。ない項目・null・文字列でない値は通さない。
// time.Parse は表記の揺れ（ミリ秒の桁数など）を受け入れうるので、書き戻して元の表記と一致するものだけを通す。
func parseTimestamp(raw json.RawMessage) (time.Time, bool) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return time.Time{}, false
	}
	t, err := time.Parse(timeLayout, s)
	if err != nil || t.UTC().Format(timeLayout) != s {
		return time.Time{}, false
	}
	return t, true
}

// unmarshalString は、項目がないか null か、文字列でなければエラーにする。
func unmarshalString(fields map[string]json.RawMessage, name string, dst *string) error {
	v, ok := fields[name]
	if !ok || string(v) == "null" {
		return fmt.Errorf("%s is missing or null", name)
	}
	return json.Unmarshal(v, dst)
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
