// プレイ履歴の同期（仕様書 8 章「プレイ履歴」）。
package metasync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/sync/errgroup"
)

const (
	transferLimit = 8
	timeLayout    = "2006-01-02T15:04:05.000Z"
)

// sessionIDPattern は、UUIDv7 の形（仕様書 2 章の HEAD と同じ）か、旧版の 32 桁の小文字 16 進に当たる（8-S6）。
var sessionIDPattern = regexp.MustCompile(`^(?:[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}|[0-9a-f]{32})$`)

func ValidSessionID(id string) bool {
	return sessionIDPattern.MatchString(id)
}

type sessionJSON struct {
	ID          string `json:"id"`
	GameID      string `json:"gameId"`
	PlayedAt    string `json:"playedAt"`
	DurationSec int64  `json:"durationSec"`
}

func sessionsPrefix(gameID string) string {
	return "games/" + gameID + "/sessions/"
}

func sessionKey(gameID, sessionID string) string {
	return sessionsPrefix(gameID) + sessionID + ".json"
}

// remoteSessionIDs は、`{ID}.json` の形で ID が 8-S6 に合うキーだけを数える。`{ID}.deleted.json` は、
// 末尾の .json を外した `{ID}.deleted` が 8-S6 に合わないので外れる（8-S3）。
func (c *Core) remoteSessionIDs(ctx context.Context, gameID string) (map[string]bool, error) {
	prefix := sessionsPrefix(gameID)
	keys, err := c.cfg.Remote.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	ids := map[string]bool{}
	for _, key := range keys {
		id, ok := strings.CutSuffix(strings.TrimPrefix(key, prefix), ".json")
		if ok && ValidSessionID(id) {
			ids[id] = true
		}
	}
	return ids, nil
}

// uploadSessions は、ローカルにだけあるセッションを PUT し（8-S1）、PUT した件数を返す。1 件が失敗したら残りを止める。
// 先に PUT できた分は残るが、同期済みのフラグを持たず毎回リモートの一覧と突き合わせるので、次の同期で残りが PUT される。
// DB に入れるときにも ID を検証するが（store.CreateSession）、DB を直接書き換えられたときのために、ここでも飛ばす。
func (c *Core) uploadSessions(ctx context.Context, gameID string) (int, error) {
	local, err := c.cfg.Local.Sessions(ctx, gameID)
	if err != nil {
		return 0, fmt.Errorf("read local sessions: %w", err)
	}
	remote, err := c.remoteSessionIDs(ctx, gameID)
	if err != nil {
		return 0, err
	}

	// 失敗した後に回る残りの Put は、キャンセル済みの ctx を受けて PUT せずに返る（objstoretest の契約）
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(transferLimit)
	n := 0
	for _, s := range local {
		if !ValidSessionID(s.ID) {
			c.cfg.Logger.Warn("skip session with invalid id", "gameId", gameID, "sessionId", s.ID)
			continue
		}
		if remote[s.ID] {
			continue
		}
		n++
		g.Go(func() error { return c.putSession(gctx, s) })
	}
	if err := g.Wait(); err != nil {
		return 0, err
	}
	return n, nil
}

func (c *Core) putSession(ctx context.Context, s Session) error {
	body, err := json.Marshal(sessionJSON{
		ID:          s.ID,
		GameID:      s.GameID,
		PlayedAt:    s.PlayedAt.UTC().Format(timeLayout),
		DurationSec: s.DurationSec,
	})
	if err != nil {
		return fmt.Errorf("encode session %s: %w", s.ID, err)
	}
	if err := c.cfg.Remote.Put(ctx, sessionKey(s.GameID, s.ID), bytes.NewReader(body), int64(len(body))); err != nil {
		return fmt.Errorf("put session %s: %w", s.ID, err)
	}
	return nil
}
