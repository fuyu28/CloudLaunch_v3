# db-migration-goose

- 日時: 2026-10-23 00:30 (JST)

## 当初の要求 / 背景

ローカルの状態は SQLite に保存する（仕様書 3 章）。テーブルの定義は最初の migration の SQL を正とする。DB は各利用者の PC にあり、アプリが起動時に自分で migration を適用する。

v1 の完成後には、スキーマを変えながら実験することが見込まれる。CloudLaunch_Go は、`schema_migrations` テーブルで適用済みのファイル名を記録する migration を自作していた。

## 仕様

- SQLite のドライバ
- migration の適用方法
- SQL の書き方

## 決定事項

### ドライバ

- 事項: SQLite のドライバ
- 決定内容: `modernc.org/sqlite` を使う
- 判断理由: cgo が要らないため、Windows 向けのビルドに C コンパイラを用意しなくて済む

### migration の適用方法

- 事項: migration を自作するか、ライブラリを使うか
- 決定内容: `github.com/pressly/goose/v3` をライブラリとして組み込む。SQL は `embed.FS` でバイナリに埋め込み、アプリは起動時に Up だけを呼ぶ。down migration は開発用として書くが、v1 では正しく戻せることを保証しない
- 判断理由: 自作（`PRAGMA user_version` で番号を管理する）なら依存は増えないが、down・redo・status のような実験向けの操作を自分で書くことになる。goose なら、開発機に CLI を入れて手元の DB に対して down / redo / status を使える。goose は純粋な Go のライブラリで、modernc の sqlite と組み合わせても cgo なしでビルドでき、配布物は exe 1 つのままである。golang-migrate は、ドライバとの組み合わせによってはつなぎのコードが要る。アプリが呼ぶのは Up だけなので、利用者の DB が巻き戻ることはない。DB には適用記録の表 `goose_db_version` が 1 つ増える

### SQL の書き方

- 事項: Go から SQL をどう扱うか
- 決定内容: `database/sql` に生の SQL を書く
- 判断理由: v1 の表は 7 つほどで、手で書いても量は少ない。sqlc を使えば SQL と Go のずれをコンパイル時に検出できるが、生成ツールの固定と生成物のコミットという運用が増える。表が増えて手で書くのが負担になったら、sqlc を検討する

## 結果 / 影響

- 実験の段階で down / redo を使える
- 依存が 1 つ増える。バイナリのサイズの増加は未計測
- down migration は保証しないので、利用者の DB を巻き戻す手段は v1 にない

## 更新履歴

- 2026-10-23 00:30 作成
