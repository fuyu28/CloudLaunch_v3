// リモートのキーのレイアウトと、HEAD が指す世代の読み込み（仕様書 2 章）。
package syncore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"regexp"
)

var generationPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func headKey(gameID string) string {
	return "games/" + gameID + "/HEAD"
}

func manifestKey(gameID, generation string) string {
	return "games/" + gameID + "/manifests/" + generation + ".json"
}

// readRemote は HEAD がなければ nil を返す。
func (c *Core) readRemote(ctx context.Context, gameID string) (*RemoteState, error) {
	head, err := c.getAll(ctx, headKey(gameID))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read HEAD: %w", err)
	}
	generation := string(head)
	if !generationPattern.MatchString(generation) {
		return nil, fmt.Errorf("malformed HEAD: %q", generation)
	}

	body, err := c.getAll(ctx, manifestKey(gameID, generation))
	if err != nil {
		return nil, fmt.Errorf("read manifest %s pointed by HEAD: %w", generation, err)
	}
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("parse manifest %s: %w", generation, err)
	}
	if m.Version != 1 {
		return nil, fmt.Errorf("manifest %s has unsupported version %d", generation, m.Version)
	}
	return &RemoteState{Generation: generation, Manifest: m, Hash: ManifestHash(m.Entries)}, nil
}

func (c *Core) getAll(ctx context.Context, key string) ([]byte, error) {
	r, err := c.remote.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", key, err)
	}
	return b, nil
}
