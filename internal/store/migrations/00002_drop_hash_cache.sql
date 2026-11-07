-- +goose Up
-- ハッシュキャッシュは持たないと決めた（仕様書 3 章）。この表は一度も読み書きしていないので、行を移さずに消す
DROP TABLE hash_cache;

-- +goose Down
CREATE TABLE hash_cache (
  game_id  TEXT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
  path     TEXT NOT NULL,
  size     INTEGER NOT NULL CHECK (size >= 0),
  mtime_ns INTEGER NOT NULL,
  sha256   TEXT NOT NULL CHECK (length(sha256) = 64),
  PRIMARY KEY (game_id, path)
) STRICT;
