// プレイ履歴の同期（仕様書 8 章「プレイ履歴」）。
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
	"sort"
	"strconv"
	"strings"

	"golang.org/x/sync/errgroup"
)

const transferLimit = 8

// uuidV7Expr は、UUIDv7 の形（仕様書 2 章の HEAD と同じ）。セッション ID（8-S6）とゲーム ID（8-N6）の検証で同じ形を使う。
const uuidV7Expr = `[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}`

// sessionIDPattern は、UUIDv7 の形か、旧版の 32 桁の小文字 16 進に当たる（8-S6）。
var sessionIDPattern = regexp.MustCompile(`^(?:` + uuidV7Expr + `|[0-9a-f]{32})$`)

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

// syncSessions は、リモートの一覧を 1 回だけ取り、アップロード → 取り込みの順に行う。どちらかが失敗したら、
// そこでエラーを返す。両方にある ID のセッションは、中身を比べずにどちらも触らない（8-S4）。
func (c *Core) syncSessions(ctx context.Context, gameID string) (Result, error) {
	local, err := c.cfg.Local.Sessions(ctx, gameID)
	if err != nil {
		return Result{}, fmt.Errorf("read local sessions: %w", err)
	}
	remote, err := c.remoteSessionIDs(ctx, gameID)
	if err != nil {
		return Result{}, err
	}
	up, err := c.uploadSessions(ctx, gameID, local, remote)
	if err != nil {
		return Result{}, err
	}
	down, err := c.downloadSessions(ctx, gameID, local, remote)
	if err != nil {
		return Result{}, err
	}
	return Result{UploadedSessions: up, DownloadedSessions: down}, nil
}

// uploadSessions は、ローカルにだけあるセッションを PUT し（8-S1）、PUT した件数を返す。1 件が失敗したら残りを止める。
// 先に PUT できた分は残るが、同期済みのフラグを持たず毎回リモートの一覧と突き合わせるので、次の同期で残りが PUT される。
// DB に入れるときにも ID を検証するが（store.CreateSession）、DB を直接書き換えられたときのために、ここでも飛ばす。
func (c *Core) uploadSessions(ctx context.Context, gameID string, local []Session, remote map[string]bool) (int, error) {
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

// downloadSessions は、リモートにだけあるセッションを GET し、すべて揃ってから 1 つのトランザクションで DB に入れる（8-S2）。
// 入れた件数を返す。GET が 1 件でも失敗したら残りを止め、何も入れない。次の同期で一覧と突き合わせ直すので、取り込み直せる。
// 不正なもの（8-V1〜8-V6）・一覧の後に消えていたもの・別のゲームに同じ ID があるもの（8-S5）は、その 1 件だけを飛ばす。
func (c *Core) downloadSessions(ctx context.Context, gameID string, local []Session, remote map[string]bool) (int, error) {
	have := make(map[string]bool, len(local))
	for _, s := range local {
		have[s.ID] = true
	}
	var ids []string
	for id := range remote {
		if !have[id] {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return 0, nil
	}
	sort.Strings(ids)

	// 失敗した後に回る残りの Get は、キャンセル済みの ctx を受けて GET せずに返る（objstoretest の契約）
	fetched := make([]*Session, len(ids))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(transferLimit)
	for i, id := range ids {
		g.Go(func() error {
			s, err := c.getSession(gctx, gameID, id)
			fetched[i] = s
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return 0, err
	}

	sessions := make([]Session, 0, len(fetched))
	for _, s := range fetched {
		if s != nil {
			sessions = append(sessions, *s)
		}
	}
	if len(sessions) == 0 {
		return 0, nil
	}
	res, err := c.cfg.Local.ImportSessions(ctx, gameID, sessions)
	if err != nil {
		return 0, fmt.Errorf("import sessions: %w", err)
	}
	for _, id := range res.OtherGame {
		c.cfg.Logger.Warn("skip remote session that belongs to another game", "gameId", gameID, "sessionId", id)
	}
	return res.Inserted, nil
}

// getSession は、取り込めるセッションを返す。不正なものと、一覧の後に消えていたものは、ログに残して nil を返す。
func (c *Core) getSession(ctx context.Context, gameID, id string) (*Session, error) {
	r, err := c.cfg.Remote.Get(ctx, sessionKey(gameID, id))
	if errors.Is(err, fs.ErrNotExist) {
		c.cfg.Logger.Info("skip remote session removed after listing", "gameId", gameID, "sessionId", id)
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get session %s: %w", id, err)
	}
	defer func() { _ = r.Close() }()
	body, err := io.ReadAll(io.LimitReader(r, maxSessionSize+1))
	if err != nil {
		return nil, fmt.Errorf("read session %s: %w", id, err)
	}
	s, err := parseSession(body, gameID, id)
	if len(body) > maxSessionSize {
		err = fmt.Errorf("8-V1: body exceeds %d bytes", maxSessionSize)
	}
	if err != nil {
		c.cfg.Logger.Warn("skip invalid remote session", "gameId", gameID, "sessionId", id, "err", err)
		return nil, nil
	}
	return &s, nil
}

// maxSessionSize は、読むセッションの本文の上限。正しい本文は 200 バイトに満たないので、超えるものは壊れた
// object として 8-V1 で扱い、同期のたびに大きな object を並列でメモリに読まないようにする。
const maxSessionSize = 64 << 10

// durationPattern は、0 以上の整数の JSON の表記に当たる。小数（1.5）・指数（1e3）・負の数は当たらない。
var durationPattern = regexp.MustCompile(`^(?:0|[1-9][0-9]*)$`)

// parseSession は、仕様書 8-V1〜8-V6 で検証する。知らない項目は無視する。
// 構造体に Unmarshal しないのは、encoding/json が項目名を大文字小文字を区別せずに当て、仕様書の項目名を持たない本文が
// 8-V2 を通るため。項目ごとに読むので、型の違う値も、その項目の ID（8-V3〜8-V6）で不正になる。
func parseSession(body []byte, gameID, id string) (Session, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return Session{}, fmt.Errorf("8-V1: %w", err)
	}
	for _, name := range []string{"id", "gameId", "playedAt", "durationSec"} {
		if v, ok := fields[name]; !ok || string(v) == "null" {
			return Session{}, fmt.Errorf("8-V2: %s is missing or null", name)
		}
	}
	var sid, sgame string
	if err := json.Unmarshal(fields["id"], &sid); err != nil || sid != id {
		return Session{}, fmt.Errorf("8-V3: id %s does not match key", fields["id"])
	}
	if err := json.Unmarshal(fields["gameId"], &sgame); err != nil || sgame != gameID {
		return Session{}, fmt.Errorf("8-V4: gameId %s does not match key", fields["gameId"])
	}
	playedAt, ok := parseTimestamp(fields["playedAt"])
	if !ok {
		return Session{}, fmt.Errorf("8-V5: playedAt %s is not in the spec format", fields["playedAt"])
	}
	// json.Number で受けないのは、文字列の "60" も数として通してしまうため
	raw := string(fields["durationSec"])
	if !durationPattern.MatchString(raw) {
		return Session{}, fmt.Errorf("8-V6: durationSec %s is not a non-negative integer", raw)
	}
	duration, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return Session{}, fmt.Errorf("8-V6: %w", err)
	}
	return Session{ID: id, GameID: gameID, PlayedAt: playedAt, DurationSec: duration}, nil
}
