# store-and-game-add

- 日時: 2026-10-23 00:53 (JST)

## 当初の要求 / 背景

M1（walking skeleton）の最初の作業として、ゲームをローカルに登録できるようにする。M1 では、`game add` → `status` → `push` → 別のデータディレクトリで `pull` を一度通し、部品のつなぎ目の問題を先に出す（AGENTS.md「開発の進め方」）。

ローカルの状態は SQLite に保存し、テーブルの定義は最初の migration の SQL を正とする（仕様書 3 章）。ドライバは `modernc.org/sqlite`、migration は goose を使う（2026-10-23_00-30_db-migration-goose.md）。表の構成（7 つの表とその役割）と外部キーの扱いは、テーブル設計の段階で決めている。

v1 の CLI は、同期コアを動かして確かめるための道具であり、ゲームの管理は GUI で行う。ただし CLI だけで試すには、ゲームを登録する手段が要る。

## 仕様

- 最初の migration に入れる表の範囲
- 表の定義の細部（型の扱い、制約）
- DB の開き方と、接続ごとの設定
- store の公開 API と、値を決める責任の分担
- CLI のデータの置き場所
- `game add` の引数と検査
- UUIDv7 の生成方法

## 決定事項

### 最初の migration に入れる表

- 事項: M1 で使わない表も最初の migration に入れるか
- 決定内容: `games`・`game_locals`・`play_sessions`・`hash_cache`・`pull_operations`・`legacy_sessions`・`settings` の 7 つすべてを最初の migration に入れる
- 判断理由: M1 で使うのは `games`・`game_locals`・`settings` だけだが、表は互いに外部キーでつながっており、まとめて見たほうが設計の整合を確かめやすい。使う表だけを入れると、表を足す migration のたびに設計を確かめ直すことになる

### 表の定義の細部

- 事項: 列の型と制約
- 決定内容: すべての表を `STRICT` にする。空文字を許さない列には `<> ''`、sha256 の列には `length(...) = 64`、時間とサイズには `>= 0` の CHECK 制約を置く。`game_locals` の tombstone の記録（`remote_deleted_at`・`remote_deleted_by`）は、両方そろって入るか、どちらも入らないかの CHECK 制約を置く。`pull_operations.game_id` は `UNIQUE` にし、外部キーは `ON DELETE RESTRICT` にする。日時の列の形式（RFC3339 の固定長）は CHECK 制約で検査せず、書き込む前に Go の側で検査する
- 判断理由: SQLite は通常、列の型と違う値を黙って受け入れる。`STRICT` にすれば、それをエラーにできる。sha256 の長さの制約は、仕様書 2 章の表記（小文字の 16 進数 64 文字）に基づく。1 ゲームにつき実行中の Pull は 1 件なので、`UNIQUE` で 2 件目を DB の側で拒否できる。ジャーナルが消えると Pull の残骸を片付けられなくなるため、ジャーナルが残っている間はゲームを削除できないようにする（仕様書 3 章）。日時の形式を SQL の CHECK 制約で正確に書くと複雑になり、Go の `time` で検査するほうが確実である

### DB の開き方

- 事項: 接続ごとの設定をどこで行うか
- 決定内容: `foreign_keys` の有効化、WAL モード、`busy_timeout` の 5 秒を、DB を開くときの接続文字列（DSN）に書く。`Open` は DB を開いたあと、未適用の migration を最後まで適用する
- 判断理由: `database/sql` は内部で複数の接続を作って使い回すが、SQLite の `PRAGMA` は実行した 1 本の接続にしか効かない。DSN に書けば、新しく作るどの接続にも適用される。WAL にすると、GUI と CLI が同時に DB を開いても、読み込みが書き込みを待たずに済む（仕様書 3 章）。`busy_timeout` は、書き込みがぶつかったときにすぐ失敗せず待つ時間で、同期 1 回の DB の書き込みは短いため 5 秒で足りる見込みとした

### store の公開 API

- 事項: 最初の作業で store が公開する関数と、値を決める責任
- 決定内容: `Open(ctx, path) (*Store, error)`、`(*Store).Close() error`、`(*Store).CreateGame(ctx, NewGame) error` を公開する。`NewGame` には、ゲーム ID・タイトル・ブランド・セーブフォルダと実行ファイルのパス・作成日時を、呼び出し側が詰めて渡す。UUIDv7 の採番と現在時刻の取得は、store ではなく呼び出し側（CLI）が行う
- 判断理由: store の中で ID や時刻を決めると、呼ぶたびに値が変わり、テストで期待値と比べにくい。呼び出し側が決めて渡せば、テストで値を固定できる

### CLI のデータの置き場所

- 事項: CLI が DB などを置く場所
- 決定内容: `--data-dir` フラグを用意し、DB・バックアップ・ロックファイルをこのディレクトリにまとめる。既定は、Windows では `%LOCALAPPDATA%\CloudLaunch`、Linux では `$XDG_DATA_HOME/cloudlaunch`（未設定なら `~/.local/share/cloudlaunch`）とする
- 判断理由: Windows の既定は仕様書 3 章の保存先に合わせた。Linux の既定は、XDG Base Directory の慣例に合わせた。1 台の PC の中でデータディレクトリを 2 つ使い分ければ、2 台の PC を模して push と pull を試せる

### `game add` の引数

- 事項: ゲームを登録するコマンドの形
- 決定内容: `cloudlaunch-cli game add --title <タイトル> --save-dir <パス> --exe <パス> [--brand <ブランド>] [--id <gameId>] [--data-dir <dir>]` とする。`--id` を省くと新しい UUIDv7 を振り、標準出力に表示する。`--save-dir` と `--exe` は絶対パスであることだけを検査し、存在は検査しない
- 判断理由: 2 台目の PC を模すときは、同じゲーム ID で登録する必要があるため、`--id` で指定できるようにする。2 台目の PC では、まだセーブフォルダがないことがある（仕様書 4 章の「local なし」）ので、存在は検査しない。相対パスを許すと、CLI を実行した場所によって指す先が変わる

### UUIDv7 の生成

- 事項: UUIDv7 を生成するライブラリ
- 決定内容: `github.com/google/uuid` の `NewV7` を使う
- 判断理由: Go の標準ライブラリには UUID の生成がない。`github.com/google/uuid` は UUIDv7 に対応しており、広く使われている

## 結果 / 影響

（作業が終わったら書く）

## 更新履歴

- 2026-10-23 00:53 作成
