// 2 台目の PC での取り込み（仕様書 8 章「2 台目の PC での取り込み」）。リモートのゲームを一覧し、選んだゲームを同じ gameId で
// 登録する。
package metasync

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/sync/errgroup"
)

const gamesPrefix = "games/"

// gameIDPattern は、UUIDv7 の形（仕様書 2 章の HEAD と同じ）に当たる。
var gameIDPattern = regexp.MustCompile(`^` + uuidV7Expr + `$`)

var (
	errRemoteDeleted = errors.New("game is deleted on remote")
	// errUnimportable は、リモートのデータが不正で取り込めないことを表す（8-N8・8-N9）
	errUnimportable = errors.New("game cannot be imported")
)

type RemoteGames struct {
	// Games は gameId の順に並ぶ
	Games []GameInfo
	// Skipped は、8-N6・8-N8・8-N9 で除いたゲームの数（8-N10）
	Skipped int
}

// RemoteGames は、この PC に登録していないリモートのゲームを一覧する（8-N1・8-N5〜8-N11）。登録済みのゲームと、gameId が
// UUIDv7 の形でないゲームは、リモートを読まずに除く。読み込みが 1 つでも失敗したら、一覧の全体をエラーにする。
func (c *Core) RemoteGames(ctx context.Context) (RemoteGames, error) {
	ids, err := c.cfg.Local.GameIDs(ctx)
	if err != nil {
		return RemoteGames{}, fmt.Errorf("read registered games: %w", err)
	}
	prefixes, err := c.cfg.Remote.ListPrefixes(ctx, gamesPrefix)
	if err != nil {
		return RemoteGames{}, fmt.Errorf("list games: %w", err)
	}
	registered := make(map[string]bool, len(ids))
	for _, id := range ids {
		registered[id] = true
	}
	var res RemoteGames
	var candidates []string
	for _, p := range prefixes {
		id := strings.TrimSuffix(strings.TrimPrefix(p, gamesPrefix), "/")
		if registered[id] { // 8-N5
			continue
		}
		if !gameIDPattern.MatchString(id) { // 8-N6
			c.cfg.Logger.Warn("skip remote game with invalid id", "prefix", p)
			res.Skipped++
			continue
		}
		candidates = append(candidates, id)
	}

	// 並列に読むので、結果をゲームの位置に書き、最後に一覧の順（ListPrefixes の辞書順）で集める
	infos := make([]GameInfo, len(candidates))
	skipped := make([]error, len(candidates))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(transferLimit)
	for i, id := range candidates {
		g.Go(func() error {
			info, err := c.readRemoteGame(gctx, id)
			switch {
			case errors.Is(err, errRemoteDeleted), errors.Is(err, errUnimportable):
				skipped[i] = err
			case err != nil:
				return err
			default:
				infos[i] = info
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return RemoteGames{}, err
	}
	for i, id := range candidates {
		switch {
		case skipped[i] == nil:
			res.Games = append(res.Games, infos[i])
		case errors.Is(skipped[i], errUnimportable):
			c.cfg.Logger.Warn("skip remote game that cannot be imported", "gameId", id, "err", skipped[i])
			res.Skipped++
		}
	}
	return res, nil
}

// ImportGame は、ゲームの tombstone と game.json を読み直してから、同じ gameId で登録する（8-N3・8-N12）。画像とプレイ履歴は
// 取得せず、次の Sync に任せる。登録済みの ID と UUIDv7 の形でない ID は、リモートを読まずにエラーにする。
func (c *Core) ImportGame(ctx context.Context, gameID, saveFolderPath, exePath string) (GameInfo, error) {
	if !gameIDPattern.MatchString(gameID) {
		return GameInfo{}, fmt.Errorf("game id is not a UUIDv7: %q", gameID)
	}
	ids, err := c.cfg.Local.GameIDs(ctx)
	if err != nil {
		return GameInfo{}, fmt.Errorf("read registered games: %w", err)
	}
	if slices.Contains(ids, gameID) {
		return GameInfo{}, fmt.Errorf("game %s is already registered", gameID)
	}
	g, err := c.readRemoteGame(ctx, gameID)
	if err != nil {
		return GameInfo{}, fmt.Errorf("game %s: %w", gameID, err)
	}
	if err := c.cfg.Local.RegisterGame(ctx, g, saveFolderPath, exePath); err != nil {
		return GameInfo{}, fmt.Errorf("register game: %w", err)
	}
	return g, nil
}

// readRemoteGame は、ゲームの tombstone → game.json の順に読む（8-N7）。ゲームの tombstone があれば errRemoteDeleted を、
// 不正な tombstone（8-N8）と、ない・不正な game.json（8-N9）なら errUnimportable で包んだエラーを返す。それ以外のエラーは
// 読み込みの失敗（8-N11）である。
func (c *Core) readRemoteGame(ctx context.Context, gameID string) (GameInfo, error) {
	body, err := c.getLimited(ctx, tombstoneKey(gameID), maxTombstoneSize)
	switch {
	case err == nil:
		if _, err := validateTombstone(body); err != nil {
			return GameInfo{}, fmt.Errorf("%w: invalid deleted.json: %w", errUnimportable, err)
		}
		return GameInfo{}, errRemoteDeleted
	case !errors.Is(err, fs.ErrNotExist):
		return GameInfo{}, err
	}
	body, err = c.getGameJSON(ctx, gameID)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return GameInfo{}, fmt.Errorf("%w: %w", errUnimportable, err)
	case err != nil:
		return GameInfo{}, err
	}
	g, err := parseGameInfo(body, gameID)
	if err != nil {
		return GameInfo{}, fmt.Errorf("%w: invalid game.json: %w", errUnimportable, err)
	}
	return g, nil
}
