// リモートのキーのレイアウトと書式、HEAD が指す世代の読み込み（仕様書 2 章）。
package syncore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"regexp"
)

// store と metasync にも同じ書式の定数がある。どれか 1 つだけ変えると DB とリモートの JSON で日時の形がずれるので、
// それぞれのテストが、書式どおりの値で確かめている（#47）
const timeLayout = "2006-01-02T15:04:05.000Z"

// maxHeadSize は UUIDv7 の文字列の長さ。超える HEAD は generationPattern に当たらないので、続きを読まない
const maxHeadSize = 36

var generationPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range []byte(s) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func gamePrefix(gameID string) string {
	return "games/" + gameID + "/"
}

func headKey(gameID string) string {
	return gamePrefix(gameID) + "HEAD"
}

func tombstoneKey(gameID string) string {
	return gamePrefix(gameID) + "deleted.json"
}

func manifestsPrefix(gameID string) string {
	return gamePrefix(gameID) + "manifests/"
}

func manifestKey(gameID, generation string) string {
	return manifestsPrefix(gameID) + generation + ".json"
}

func objectsPrefix(gameID string) string {
	return gamePrefix(gameID) + "objects/"
}

func objectKey(gameID, sha256 string) string {
	return objectsPrefix(gameID) + sha256
}

// readHead は HEAD が指す世代を返す。HEAD がなければ空を返す。
func (c *Core) readHead(ctx context.Context, gameID string) (string, error) {
	head, found, err := c.getLimited(ctx, headKey(gameID), maxHeadSize)
	if err != nil {
		return "", fmt.Errorf("read HEAD: %w", err)
	}
	if !found {
		return "", nil
	}
	generation := string(head)
	if !generationPattern.MatchString(generation) {
		return "", fmt.Errorf("malformed HEAD: %q", generation)
	}
	return generation, nil
}

// readRemote は HEAD がなければゼロ値を返す。
func (c *Core) readRemote(ctx context.Context, gameID string) (RemoteState, error) {
	generation, err := c.readHead(ctx, gameID)
	if err != nil || generation == "" {
		return RemoteState{}, err
	}

	m, err := c.readManifest(ctx, gameID, generation)
	if err != nil {
		return RemoteState{}, fmt.Errorf("manifest pointed by HEAD: %w", err)
	}
	return RemoteState{Generation: generation, Manifest: m, Hash: ManifestHash(m.Entries)}, nil
}

func (c *Core) readManifest(ctx context.Context, gameID, generation string) (Manifest, error) {
	key := manifestKey(gameID, generation)
	// manifest の大きさの上限は、仕様書に定めていない
	body, found, err := c.getLimited(ctx, key, math.MaxInt64)
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest %s: %w", generation, err)
	}
	if !found {
		return Manifest{}, fmt.Errorf("read manifest %s: %s: %w", generation, key, fs.ErrNotExist)
	}
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return Manifest{}, fmt.Errorf("parse manifest %s: %w", generation, err)
	}
	if m.Version != 1 {
		return Manifest{}, fmt.Errorf("manifest %s has unsupported version %d", generation, m.Version)
	}
	return m, nil
}

// getLimited は、キーがなければ found を false にして返す。「ない」を戻り値で分けるのは、本文の読み込みの失敗が
// fs.ErrNotExist を包んでいても、ないものとして扱わないため。limit を超える本文は、limit の 1 バイト先までしか読まない
// ので、呼び出し側は長さで超えたことを判定できる。Get のエラーはキーを含むので、包まずに返す。
func (c *Core) getLimited(ctx context.Context, key string, limit int64) (body []byte, found bool, err error) {
	r, err := c.cfg.Remote.Get(ctx, key)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = r.Close() }()
	n := limit
	if n < math.MaxInt64 {
		n++
	}
	body, err = io.ReadAll(io.LimitReader(r, n))
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", key, err)
	}
	return body, true, nil
}
