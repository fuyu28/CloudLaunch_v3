// Package legacy は、旧版（CloudLaunch_Go）のエクスポートからプレイ履歴を取り込む（仕様書 9 章）。
package legacy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"time"
)

// Session は未紐付けの旧履歴の 1 件。GameID と GameTitle は旧版のゲームのもの。
type Session struct {
	ID          string
	GameID      string
	GameTitle   string
	PlayedAt    time.Time
	DurationSec int64
}

type Store interface {
	// AddLegacySessions は、1 つのトランザクションで未紐付けの旧履歴に入れ、入れた件数を返す。未紐付けの旧履歴か
	// 通常のプレイ履歴に同じ ID があるものは入れない（仕様書 9-L3）
	AddLegacySessions(ctx context.Context, sessions []Session) (int, error)
}

type Result struct {
	Added int
	// Existing は、未紐付けの旧履歴か通常のプレイ履歴に同じ ID があって保持しなかった件数
	Existing int
	// Orphaned は、games にない gameId を持つので保持しなかった件数（仕様書 9-L2）
	Orphaned int
}

var legacyIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// ValidSessionID は、旧版のセッション ID の形（32 桁の小文字 16 進）に当たるかを返す。
func ValidSessionID(id string) bool {
	return legacyIDPattern.MatchString(id)
}

// Import は、r のエクスポートを読み、プレイ履歴を未紐付けの旧履歴として st に入れる。エクスポートが 1 か所でも
// 不正なら、何も入れずにエラーを返す。不正の範囲は docs/adr/2026-11-14_01-59_legacy-export-import.md による。
func Import(ctx context.Context, r io.Reader, st Store) (Result, error) {
	sessions, orphaned, err := parseExport(r)
	if err != nil {
		return Result{}, fmt.Errorf("invalid export: %w", err)
	}
	added, err := st.AddLegacySessions(ctx, sessions)
	if err != nil {
		return Result{}, fmt.Errorf("add legacy sessions: %w", err)
	}
	return Result{Added: added, Existing: len(sessions) - added, Orphaned: orphaned}, nil
}

// object は、項目名を大文字小文字まで含めて比べるために、JSON のオブジェクトを項目ごとに持つ。encoding/json は
// 構造体の項目名を大文字小文字を区別せずに当てるので、構造体に読むと `ID` を `id` として通してしまう。
type object map[string]json.RawMessage

func parseExport(r io.Reader) ([]Session, int, error) {
	// json.Decoder は 1 つ目の値の後ろを読まないので、全体を読んでから Unmarshal し、後ろに続くものも不正にする
	body, err := io.ReadAll(r)
	if err != nil {
		return nil, 0, fmt.Errorf("read: %w", err)
	}
	var top object
	if err := json.Unmarshal(body, &top); err != nil {
		return nil, 0, fmt.Errorf("decode: %w", err)
	}
	if top == nil {
		return nil, 0, errors.New("top level is null")
	}
	games, err := array(top, "games")
	if err != nil {
		return nil, 0, err
	}
	rawSessions, err := array(top, "sessions")
	if err != nil {
		return nil, 0, err
	}

	titles := map[string]string{}
	for i, g := range games {
		id, err := nonEmptyString(g, "id")
		if err != nil {
			return nil, 0, fmt.Errorf("games[%d]: %w", i, err)
		}
		title, err := nonEmptyString(g, "title")
		if err != nil {
			return nil, 0, fmt.Errorf("games[%d]: %w", i, err)
		}
		if _, ok := titles[id]; ok {
			return nil, 0, fmt.Errorf("games[%d]: duplicate id %q", i, id)
		}
		titles[id] = title
	}

	var sessions []Session
	seen := map[string]bool{}
	orphaned := 0
	for i, raw := range rawSessions {
		sess, err := parseSession(raw)
		if err != nil {
			return nil, 0, fmt.Errorf("sessions[%d]: %w", i, err)
		}
		if seen[sess.ID] {
			return nil, 0, fmt.Errorf("sessions[%d]: duplicate id %q", i, sess.ID)
		}
		seen[sess.ID] = true
		title, ok := titles[sess.GameID]
		if !ok {
			orphaned++
			continue
		}
		sess.GameTitle = title
		sessions = append(sessions, sess)
	}
	return sessions, orphaned, nil
}

// array は、key の配列の要素を返す。key がないときはエラーにし、null は空の配列とする。旧版は、DB にゲームが
// ないとき games を null で書くため。
func array(o object, key string) ([]object, error) {
	raw, ok := o[key]
	if !ok {
		return nil, fmt.Errorf("%s is missing", key)
	}
	var elems []object
	if err := json.Unmarshal(raw, &elems); err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	for i, e := range elems {
		if e == nil {
			return nil, fmt.Errorf("%s[%d] is not an object", key, i)
		}
	}
	return elems, nil
}

func field[T any](o object, key string) (T, error) {
	var v T
	raw, ok := o[key]
	if !ok || string(raw) == "null" {
		return v, fmt.Errorf("%s is missing", key)
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, fmt.Errorf("%s: %w", key, err)
	}
	return v, nil
}

func nonEmptyString(o object, key string) (string, error) {
	s, err := field[string](o, key)
	if err != nil {
		return "", err
	}
	if s == "" {
		return "", fmt.Errorf("%s is empty", key)
	}
	return s, nil
}

func parseSession(o object) (Session, error) {
	id, err := field[string](o, "id")
	if err != nil {
		return Session{}, err
	}
	if !ValidSessionID(id) {
		return Session{}, fmt.Errorf("invalid id %q", id)
	}
	gameID, err := field[string](o, "gameId")
	if err != nil {
		return Session{}, err
	}
	playedAtRaw, err := field[string](o, "playedAt")
	if err != nil {
		return Session{}, err
	}
	// 旧版は Go の time.Time を RFC3339Nano で、旧版を動かした PC の時差のまま書く。3 章の日時の形に合わせて
	// UTC のミリ秒に切り捨てる
	playedAt, err := time.Parse(time.RFC3339Nano, playedAtRaw)
	if err != nil {
		return Session{}, fmt.Errorf("playedAt: %w", err)
	}
	playedAt = playedAt.UTC().Truncate(time.Millisecond)
	if y := playedAt.Year(); y < 0 || y > 9999 {
		return Session{}, fmt.Errorf("playedAt out of range: %s", playedAtRaw)
	}
	duration, err := field[int64](o, "duration")
	if err != nil {
		return Session{}, err
	}
	if duration < 0 {
		return Session{}, fmt.Errorf("negative duration: %d", duration)
	}
	return Session{ID: id, GameID: gameID, PlayedAt: playedAt, DurationSec: duration}, nil
}
