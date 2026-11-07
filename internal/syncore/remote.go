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

func manifestsPrefix(gameID string) string {
	return "games/" + gameID + "/manifests/"
}

func manifestKey(gameID, generation string) string {
	return manifestsPrefix(gameID) + generation + ".json"
}

func objectsPrefix(gameID string) string {
	return "games/" + gameID + "/objects/"
}

func objectKey(gameID, sha256 string) string {
	return objectsPrefix(gameID) + sha256
}

// readHead は HEAD が指す世代を返す。HEAD がなければ空を返す。
func (c *Core) readHead(ctx context.Context, gameID string) (string, error) {
	head, err := c.getAll(ctx, headKey(gameID))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read HEAD: %w", err)
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
	body, err := c.getAll(ctx, manifestKey(gameID, generation))
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest %s: %w", generation, err)
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

func (c *Core) getAll(ctx context.Context, key string) ([]byte, error) {
	r, err := c.cfg.Remote.Get(ctx, key)
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
