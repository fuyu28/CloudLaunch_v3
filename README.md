# CloudLaunch v3

PC ゲームのセーブデータを、複数の PC 間で Cloudflare R2 経由で持ち運ぶ Windows 向けのデスクトップアプリ。

- ユーザーが明示的に上書きを選ばない限り、どの PC のセーブも失われない
- 同期が途中で中断しても、ローカルのセーブフォルダとリモートは同期前か同期後のどちらかの状態に保たれる
- 両方の PC でセーブが変わっていたら、勝手にどちらかを選ばず、競合として止まる

保証する範囲と、保証しないことの詳細は [`docs/sync-spec.md`](docs/sync-spec.md) の 1 章にある。

## 状態

開発中。同期コアと、それを動かして確かめるための CLI がある。ゲームの管理は GUI（Wails）で行う予定で、まだ作っていない。

## これまでの版

このリポジトリは 3 世代目にあたる。

| 世代 | リポジトリ | 技術 | 開発期間 |
|---|---|---|---|
| 1 | [fuyu28/CloudLaunch](https://github.com/fuyu28/CloudLaunch) | Electron（TypeScript） | 2025-06〜2025-08 |
| 2 | [fuyu28/CloudLaunch_Go](https://github.com/fuyu28/CloudLaunch_Go) | Go + Wails（TypeScript） | 2026-01〜2026-10 |
| 3 | このリポジトリ | Go + Wails | 2026-10〜 |

2 世代目からは、プレイ履歴だけを取り込める（[`docs/sync-spec.md`](docs/sync-spec.md) 9 章）。ゲーム一覧と、2 世代目のリモートのデータは引き継がない。

## CLI

### ビルド

```sh
mise install
go build ./cmd/cloudlaunch-cli
```

### 接続先の設定

`.env.example` を `.env` に写し、R2 のエンドポイント・バケット・アクセスキーを書く。mise が `.env` を環境変数として読み込む。

```sh
cp .env.example .env
```

### 使い方

```sh
# ゲームを登録する。表示された gameId を以降のコマンドに渡す
cloudlaunch-cli game add --title <タイトル> --save-dir <セーブフォルダ> --exe <実行ファイル>

# ローカルとリモートを比べて、次に行う操作（変更なし / Push / Pull / 競合）を表示する
cloudlaunch-cli status --game <gameId>

# セーブをアップロード / ダウンロードする。判定と違う向きに上書きするときは --overwrite を付ける
cloudlaunch-cli push --game <gameId>
cloudlaunch-cli pull --game <gameId>

# 中断した Pull を復旧する
cloudlaunch-cli recover
```

`--save-dir` と `--exe` には絶対パスを渡す。別の PC では、同じ `--id <gameId>` を付けて `game add` してから `pull` する。

DB は `%LOCALAPPDATA%\CloudLaunch\cloudlaunch.db` に置く。`--data-dir` で置き場所を変えられる。

## 開発

開発の進め方・構成・テスト・ブランチとコミットのルールは [`AGENTS.md`](AGENTS.md) にまとめてある。

- [`docs/sync-spec.md`](docs/sync-spec.md): 同期の振る舞いの仕様。振る舞いの唯一の正
- [`docs/adr/`](docs/adr/): 技術と構造の決定とその理由

clone したら次を実行する。

```sh
mise install
lefthook install
```

テストは次で走らせる。objstore の契約テストを本物の S3 互換サーバーに対して走らせるときは、先に `docker compose up --wait` で versitygw を立てる（接続先は `.env.example` にある）。

```sh
go test -race ./...
```
