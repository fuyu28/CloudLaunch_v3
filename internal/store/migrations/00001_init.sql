-- +goose Up
CREATE TABLE games (
  id           TEXT PRIMARY KEY,
  title        TEXT NOT NULL CHECK (title <> ''),
  brand        TEXT,
  image_sha256 TEXT CHECK (image_sha256 IS NULL OR length(image_sha256) = 64),
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL
) STRICT;

CREATE TABLE game_locals (
  game_id            TEXT PRIMARY KEY REFERENCES games(id) ON DELETE CASCADE,
  save_folder_path   TEXT NOT NULL CHECK (save_folder_path <> ''),
  exe_path           TEXT NOT NULL CHECK (exe_path <> ''),
  base_manifest_hash TEXT CHECK (base_manifest_hash IS NULL OR length(base_manifest_hash) = 64),
  remote_deleted_at  TEXT,
  remote_deleted_by  TEXT,
  CHECK ((remote_deleted_at IS NULL) = (remote_deleted_by IS NULL))
) STRICT;

CREATE TABLE play_sessions (
  id           TEXT PRIMARY KEY,
  game_id      TEXT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
  played_at    TEXT NOT NULL,
  duration_sec INTEGER NOT NULL CHECK (duration_sec >= 0)
) STRICT;
CREATE INDEX play_sessions_game_id ON play_sessions(game_id);

CREATE TABLE hash_cache (
  game_id  TEXT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
  path     TEXT NOT NULL,
  size     INTEGER NOT NULL CHECK (size >= 0),
  mtime_ns INTEGER NOT NULL,
  sha256   TEXT NOT NULL CHECK (length(sha256) = 64),
  PRIMARY KEY (game_id, path)
) STRICT;

-- ジャーナルが消えると Pull の残骸を片付けられなくなるため、行がある間はゲームを削除させない
CREATE TABLE pull_operations (
  op_id      TEXT PRIMARY KEY,
  game_id    TEXT NOT NULL UNIQUE REFERENCES games(id) ON DELETE RESTRICT,
  live_path  TEXT NOT NULL,
  stage_path TEXT NOT NULL,
  old_path   TEXT NOT NULL,
  created_at TEXT NOT NULL
) STRICT;

CREATE TABLE legacy_sessions (
  id                TEXT PRIMARY KEY,
  legacy_game_title TEXT NOT NULL,
  played_at         TEXT NOT NULL,
  duration_sec      INTEGER NOT NULL CHECK (duration_sec >= 0)
) STRICT;

CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
) STRICT;

-- +goose Down
DROP TABLE settings;
DROP TABLE legacy_sessions;
DROP TABLE pull_operations;
DROP TABLE hash_cache;
DROP TABLE play_sessions;
DROP TABLE game_locals;
DROP TABLE games;
