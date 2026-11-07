-- +goose Up
-- 計算を始めた時刻の列を足す（仕様書 3 章）。キャッシュは消えても計算し直すだけなので、行を移さずに作り直す。
-- 列を足す形にしないのは、NOT NULL の列に意味のない既定値が要るため
DROP TABLE hash_cache;
CREATE TABLE hash_cache (
  game_id   TEXT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
  path      TEXT NOT NULL,
  size      INTEGER NOT NULL CHECK (size >= 0),
  mtime_ns  INTEGER NOT NULL,
  sha256    TEXT NOT NULL CHECK (length(sha256) = 64),
  hashed_at TEXT NOT NULL,
  PRIMARY KEY (game_id, path)
) STRICT;

-- +goose Down
DROP TABLE hash_cache;
CREATE TABLE hash_cache (
  game_id  TEXT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
  path     TEXT NOT NULL,
  size     INTEGER NOT NULL CHECK (size >= 0),
  mtime_ns INTEGER NOT NULL,
  sha256   TEXT NOT NULL CHECK (length(sha256) = 64),
  PRIMARY KEY (game_id, path)
) STRICT;
