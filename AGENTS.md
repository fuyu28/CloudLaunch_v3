# Repository Guidelines

CloudLaunch v3 は、PC ゲームのセーブデータを複数 PC 間で R2 経由で安全に持ち運ぶ Windows 向けデスクトップアプリ。
Go（同期コア + CLI）と Wails（UI）で作る。

## 設計と実装の分担

- 同期の振る舞いの唯一の正は `docs/sync-spec.md`
- 設計判断（データ構造・状態遷移・保証範囲）はオーナーが行う。AI は仕様にない判断を勝手に入れず、必要なら `docs/sync-spec.md` の「未決事項」に挙げて止まる
- 振る舞いを変えるときは、仕様書の変更 PR を先に通してから実装を変える
- PR はオーナーが全行読める量（目安: 数百行）に収める

## 作る順番

1. `docs/sync-spec.md`
2. 同期コア + CLI（`status` / `push` / `pull`）
3. GUI（Wails）
   - フロントエンドの第一候補は Go のテンプレート + htmx、次点は Svelte。GUI に着手するときに、進捗の表示と確認ダイアログを含む 1 画面を試作して決める
   - 状態と画面の組み立ては Go に置き、Go の型を TS で書き直す層や、Go の API を包む層を作らない。Go 版ではこの橋渡しの層が、フロントエンドの肥大化の主な原因だったため

## 構成

```
cmd/cloudlaunch-cli/   CLI のエントリポイント。各パッケージを組み合わせる配線だけを書く
internal/syncore/      セーブ同期のコア（仕様書 2・4〜7 章）
internal/metasync/     メタ情報の同期（仕様書 8 章）。ゲームの tombstone の確認と掃除もここが持つ
internal/store/        SQLite（goose の migration を含む）
internal/objstore/     S3 互換オブジェクトストアのクライアント
internal/platform/     OS 固有の処理（ファイルロック、reparse point の検出、プロセス検出）
internal/synclock/     PC 全体の同期ロック（仕様書 3 章）
```

- パッケージは責務で分ける。`services`・`domain`・`util` のような、何でも入る名前のパッケージは作らない
- syncore の中は、仕様書の章に合わせてファイルを分ける。独立して意味を持つ部分（最初の候補は manifest）が育ったら、サブパッケージに切り出す
- 依存の向き: syncore と metasync が必要な操作だけのインターフェースを定義し、store・objstore・platform がそれを実装する。syncore と metasync は互いに import せず、cmd で組み合わせる
- リモートは、キー指定で `io.Reader` を受け渡す汎用のオブジェクトストア（Get / Put / List / Delete）として扱う。キーの形（仕様書 2 章）はコアが持つ。実装は、複数の goroutine から同時に呼んでも安全にする
- 並列実行は、呼び出す側のコアが `golang.org/x/sync/errgroup` の `SetLimit` で制御する。転送は 8 並列、ハッシュ計算は CPU の数まで。1 つが失敗したら残りをキャンセルし、再試行は SDK に任せる。進捗は直列化したコールバックで通知する
- syncore の公開 API は、`Status`（副作用なしで plan を返す）→ `Push` / `Pull` / `Adopt`（plan を受け取り、承認後に状況が変わっていればセンチネルエラーを返す）、起動時の `Recover` の形にする
- エラーは、呼び出し側が振る舞いを分ける必要のあるものだけをセンチネルエラーとして公開し、それ以外は `%w` で包んで返す。表示用の型（`ApiResult` など）はコアに置かず、Wails の境界で変換する
- SQLite には `database/sql` で生の SQL を書く
- ログは `log/slog` を使い、コアは `*slog.Logger` を受け取る。出力先は配線の側で決める
- OS 固有の処理は、同じパッケージの中でファイル名の接尾辞（`_windows.go` / `_linux.go`）で分ける。製品として保証するのは Windows で、Linux の実装は開発用の最小限にとどめる。macOS は扱わない
- CLI は標準の `flag` で書き、`status` / `push` / `pull` / `recover` / `game add` を持つ。v1 の CLI は同期コアを動かして確かめるための道具で、ゲームの管理は GUI で行う
- GUI（Wails）の配置は、GUI の段階で決める

## テスト

### 方針

- syncore と metasync は TDD で書く。UI・Wails の配線・CLI の配線にはテストを書かない
- syncore と metasync のテストは外部のテストパッケージ（`package syncore_test`）に置き、公開 API 経由で書く。golangci-lint の `testpackage` で検査する
- サブパッケージに切り出した部分は、それ自身の公開 API でテストしてよい
- テストはテーブル駆動で書き、`t.Run` のケース名を「仕様書の章/項目」の日本語にする（例: `4章/base なし・local あり・remote 異なる → 競合`）
- アサーションは標準の `testing` と `github.com/google/go-cmp` で書く
- カバレッジの数値目標は設けない。仕様書の行ごとにテストを書くことで網羅する

### テストの種類

- コアのテスト: リモートはメモリ上の偽物（失敗を仕込める）、SQLite とファイルシステムは本物（一時ファイル・一時ディレクトリ）、時刻・ID 採番・プロセス検出は差し替える
  - 判定表は 1 行 1 ケースで全行を書く
  - 中止（エラーで止まる）は、偽のリモートに失敗を仕込んで確かめる
  - 中断（プロセスの異常終了）は、中断した直後の状態（ジャーナルの行、stage・old のディレクトリなど）を直接作り、新しいコアで `Recover` と `Status` を呼んで確かめる。仕様書 5 章「中断したときに残るもの」と 6 章「起動時の復旧」の各行に 1 つずつ書く
  - 複数 PC のシナリオは、別々の DB とフォルダを持つ 2 つのコアに、同じ偽のリモートを共有させて書く
- 契約テスト: objstore と偽物に同じテストを走らせる。本物の側は CI で MinIO を立てて行う。本物の R2 に対しては、ビルドタグ `r2` と環境変数があるときだけ手動で走らせる
- golden テスト: 既知の entries から計算した manifest ハッシュを固定値と比べる。計算方法が変わると全 PC で全ゲームが変化したと判定されるため
- fuzz テスト: manifest のパスの検証（仕様書 6 章）。CI では保存したコーパスだけを走らせ、ランダムな探索は手元で行う
- migration のテスト: 空の DB に最後まで適用できること
- platform のテスト: 実装したあとで書く。Windows の振る舞いは Windows の CI で確かめる
- CI のテストは Ubuntu・Windows とも `-race` で走らせる
- ここにない種類（CLI の E2E、プロパティベーステスト、ベンチマークなど）は、必要になったら PR で足す

## 開発環境

- ツールのバージョンは `mise.toml` で固定する。clone したら `mise install` と `lefthook install` を実行する
- コミット時に lefthook の pre-commit フックが走る
  - ステージした `.go` ファイルにフォーマッタ（gofmt・goimports）をかけ、直した結果をステージに戻す
  - モジュール全体に golangci-lint を `GOOS=windows` と `GOOS=linux` の両方でかける。golangci-lint は実行する OS 向けのファイルしか解析しないため、本番で動く Windows 向けのコードを検査するには `GOOS=windows` が要る
  - テストはフックでは走らせず、CI に任せる
- リンタの設定は `.golangci.yml` に明示する。リンタを追加するときは、1 つずつ PR で入れる
- CI は dev と main への PR と push で走る。Ubuntu でフォーマットの確認と両方の GOOS でのリント、Ubuntu と Windows でテストを行う。フックは `--no-verify` で飛ばせるので、ルールを最終的に守らせるのは CI である

## ブランチとマージ

- `main` はリリースした状態だけを置き、リリースのタグはここに打つ
- `dev` は開発の統合先とする
- 作業ブランチは `feat/…`・`fix/…`・`docs/…`・`refactor/…`・`chore/…` の形で、いつも dev から切る。作業ブランチからさらに作業ブランチを切らない（元のブランチが squash されると、派生したブランチで同じ変更が衝突するため）
- 作業ブランチ → dev は squash マージ、dev → main はマージコミットで取り込む。dev → main を squash すると main と dev の履歴が分かれ、次のリリースで同じ変更が衝突するため
- main と dev への取り込みは必ず PR 経由で行う。AI はオーナーの明示的な指示があるまでマージしない
- マージ後の作業ブランチは GitHub が自動で削除する。元のコミットは PR に残る

## コミット

- Conventional Commits に従う（`feat(scope):`・`fix(scope):`・`docs(scope):`・`refactor(scope):`・`chore(scope):` など）
- scope はパッケージ名か仕様書の章に合わせる（`sync`・`cli`・`gui`・`db`・`docs` など）
- サブジェクトは日本語で簡潔な命令形にする
- 本文には、なぜそう変えたか（動機・トレードオフ）を書く。コードから読める「何をしたか」は最小限にする
- AI が作ったコミットには、末尾に `Co-Authored-By:` を付ける

## コメント

- コード本体は how（実装そのもの）、テスト名は what（何を検証するか）、コミット本文は why（なぜ変えたか）を表す
- インラインコメントは why not（別案を採らない理由、やってはいけない制約）を基本にする
- 一目で分からない複雑な処理には、手順やデータの流れ、どこまで保証するかを書いてよい。次の行を言い換えるだけのコメントは書かない
- 本番の `.go` ファイルには、`package` の直前にファイルの役割を 1〜数文で書く。テストと生成物は除く
- exported な識別子すべてへの godoc は必須にしない。非自明な制約があるときだけ書く

## docs/

- docs/ には、仕様書のように「現在の正」である文書だけを置く
- 計画・タスク・バックログは GitHub Issues で管理し、終わったら閉じる
