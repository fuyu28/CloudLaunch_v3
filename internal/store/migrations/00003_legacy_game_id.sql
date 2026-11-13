-- +goose Up
-- 同じタイトルの旧ゲームを区別するため、旧版の gameId を持つ（仕様書 9-L1）。この表には一度も書き込んでいないので、
-- 行を移さずに作り直す
DROP TABLE legacy_sessions;
CREATE TABLE legacy_sessions (
  id                TEXT PRIMARY KEY,
  legacy_game_id    TEXT NOT NULL CHECK (legacy_game_id <> ''),
  legacy_game_title TEXT NOT NULL CHECK (legacy_game_title <> ''),
  played_at         TEXT NOT NULL,
  duration_sec      INTEGER NOT NULL CHECK (duration_sec >= 0)
) STRICT;

-- +goose Down
DROP TABLE legacy_sessions;
CREATE TABLE legacy_sessions (
  id                TEXT PRIMARY KEY,
  legacy_game_title TEXT NOT NULL,
  played_at         TEXT NOT NULL,
  duration_sec      INTEGER NOT NULL CHECK (duration_sec >= 0)
) STRICT;
