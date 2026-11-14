# game-tombstone-check

- 日時: 2026-11-14 19:07 (JST)

## 当初の要求 / 背景

仕様書 8 章「ゲームの tombstone の確認」は、判定の前（4 章）に `games/{gameId}/deleted.json` を読み、あれば判定せずに「リモートで削除されました」の状態にするとしている（8-T1〜8-T8）。GUI の状態の確認と CLI の `push`・`pull` は、DB に記録してから、確認なしで配下の tombstone 以外を削除する。CLI の `status` は表示だけで、記録も削除もしない（8-T2）。Push・Pull は承認の後の読み直し（5-P5・6-L6）でも tombstone を確かめる（8-T7）。CLI の `push`・`pull` は、セーブとメタ情報を同期する前に tombstone を確かめ、あれば両方を行わない（8-M1）。

ゲームの削除（8-X2〜8-X10、#101）が tombstone を書いた後で止まると、配下のデータが残る。それを消すのはこの確認なので、#101 より先に作る。

AGENTS.md の構成では、tombstone の確認と掃除は metasync が持ち、syncore と metasync は互いに import しない。`deleted.json` の検証（8-T4〜8-T6）は、2 台目の PC での取り込み（2026-11-14_02-46_remote-game-import.md）で、値を返さない `validateTombstone` として metasync に置いた。DB の `game_locals` には、tombstone の記録の列（`remote_deleted_at`・`remote_deleted_by`）が初めの migration からある。メタ情報の同期は、記録のあるゲームでリモートに触れない（8-M4）。

## 仕様

- 扱う仕様書の ID の範囲
- syncore と metasync の分担
- Status が tombstone を伝える形
- Push・Pull の拒否と、承認の後の読み直し
- 掃除の手順と、記録済みのゲームの扱い
- API の形
- CLI の振る舞い
- テスト
- PR の大きさ

## 決定事項

### 扱う仕様書の ID の範囲

- 事項: この作業で、どの項目を満たすか
- 決定内容: 8-T1〜8-T8 と 8-M1 を扱う。8-X10 の「次の確認で消し残りを削除する」も、8-T2 の掃除として扱う。削除を始める操作（8-X2〜8-X9、#101）、登録の解除（8-U1、#102）、上げ直し（8-U2〜8-U5、#103）は扱わない。GUI の状態の確認での掃除は、M4 で GUI から同じ metasync の操作を呼ぶ
- 判断理由: Issue #100 の範囲に合わせる。8-X10 は、削除する PC で記録を書いた後に配下の削除が止まった状態で、消し残りを消す処理は 8-T2 の掃除と同じである

### syncore と metasync の分担

- 事項: syncore が判定の前と承認の後に tombstone を確かめるとき、確認の実体をどこに置き、どう渡すか
- 決定内容: 読み込み・検証・DB の記録・掃除はすべて metasync に置く。syncore の `Config` に `ReadTombstone func(ctx, gameID string) (Tombstone, bool, error)` を足し、cmd が metasync の読み取りだけの確認（後述の `CheckTombstone`）を包んで渡す。`Tombstone` は syncore と metasync にそれぞれ定義し、cmd の包む関数で詰め替える。`ReadTombstone` が nil なら、`Status`・`Push`・`Pull` は何も読まずにエラーを返す。`Recover` は使わない
- 判断理由: syncore が `deleted.json` を自分で読む案は、8-T4〜8-T6 の検証が二重になり、AGENTS.md の「tombstone の確認と掃除は metasync」から外れるので採らない。インターフェースにすると、metasync の型を返すメソッドを syncore の型で受けられず、cmd に型の変換だけの構造体が要る。func にすれば、既存の `Rename` と同じく、cmd の閉包 1 つで済む。nil で黙って判定する道を作らないのは、`Locker` を必須にしたのと同じ理由で、確認を飛ばすと削除済みのゲームに Push するためである

### Status が tombstone を伝える形

- 事項: tombstone があって判定しないとき、`Status` が呼び出し側に何を返すか
- 決定内容: `Plan` に `RemoteDeleted *Tombstone` を足し、tombstone があれば、それだけを埋めた `Plan` を返す。このとき local のハッシュも HEAD も読まず、`Decision` は使わない。`ReadTombstone` が失敗したら（8-T3〜8-T6）、`Status` はエラーを返す
- 判断理由: GUI は「リモートで削除されました（deviceName・deletedAt）」を、変更なし・Push などと並ぶ状態の 1 つとして表示する。センチネルエラーにして値を `errors.As` で取り出す案は、正常な状態をエラーの道で扱うことになり、呼び出し側が判定の結果とエラーの両方を見る必要があるので採らない。判定しないので、local と HEAD を読む必要はない

### Push・Pull の拒否と、承認の後の読み直し

- 事項: 8-T7・8-T8 を、Push・Pull のどこで確かめるか
- 決定内容:
  - `ErrRemoteDeleted` をセンチネルエラーとして公開する
  - `Push`・`Pull` は、`plan.RemoteDeleted` があれば、上書きでも、ロックを取る前に `ErrRemoteDeleted` を返す
  - 承認の後に HEAD を読み直す 3 か所（5-P5・6-L6・上書きで同じとき 7-O5）で、HEAD に続けて `ReadTombstone` を呼び、あれば `ErrRemoteDeleted` を返す。Push は HEAD を書かずに止まり、PUT 済みの object と manifest は残る（5-I1）。Pull は 6-A1 のとおり stage とジャーナルを削除して止まる
  - DB の記録とリモートの掃除は syncore では行わず、`ErrRemoteDeleted` を受けた呼び出し側が metasync の掃除を呼ぶ
- 判断理由: CLI と GUI は、tombstone で止まったときに掃除と「リモートで削除されました」の表示に分かれるので、センチネルエラーにする。7-O5 は 8-T7 に挙がっていないが、HEAD を読み直す同じ確認の中にあり、削除済みのゲームに base だけを記録して成功と伝えるのを防ぐので、揃えて読み直す。`ReadTombstone` は DB の記録を先に見る（後述）ので、承認の後に記録されたゲーム（8-T8）も同じ読み直しで止まる。掃除を syncore に持たせると、syncore が metasync を呼ぶことになり、依存の向きに反する

### 掃除の手順と、記録済みのゲームの扱い

- 事項: 8-T2 の記録と掃除を、どの順で、何を確かめて行うか。DB に記録があるゲームで、リモートの `deleted.json` をどう扱うか
- 決定内容: metasync に、読み取りだけの `CheckTombstone` と、記録と掃除をする `HandleTombstone` を置く
  - `CheckTombstone`（`status` と syncore の `ReadTombstone` が使う）: DB に記録があれば、リモートを読まずに記録を返す（8-T8）。なければ `deleted.json` を読み、ない（8-T1）・ある・読めない（8-T3）・不正（8-T4〜8-T6）を返す。何も書かない
  - `HandleTombstone`（CLI の `push`・`pull`、GUI の状態の確認が使う）: DB の記録の有無によらず、`deleted.json` を読む
    1. 読めない・不正なら、エラーを返し、記録も削除もしない
    2. あれば、DB に記録がなければ記録し、`games/{gameId}/` を一覧して、`deleted.json` 以外を `DeleteMany` で削除する。DB に記録があれば、記録は書き換えない
    3. なく、DB に記録もなければ、削除されていないと返す
    4. ないのに DB に記録があれば、何も削除せず、Warn のログを残して、記録の tombstone を返す
  - 削除に失敗したら、エラーを返す。記録は残るので、次の `HandleTombstone` で同じ手順をやり直し、消し残りを削除する
- 判断理由: 8-X10 は、記録のあるゲームでも、tombstone を確かめたときに消し残りを削除するとしているので、`HandleTombstone` は記録があっても `deleted.json` を読む。記録だけを信じて掃除する案は、リモートで tombstone を見ないまま配下を消す道を作るので採らない。記録と `deleted.json` が食い違っても、配下を消すのは tombstone が見えるときだけになる。増えるのは、削除済みのゲームでの GET 1 回だけである。記録があり `deleted.json` がないときは、仕様書のどの手順でも起きない（8-X5 は PUT に成功してから記録する）。そのときに配下を消さず、8-T8 のとおり Push・Pull を止めたままにするのは、どちらに倒しても取り返しのつく側だからである。記録を先に書くのは 8-T2 の順のとおりで、掃除の途中で止まっても、この PC の Push・Pull とメタ情報の同期が止まったままになる。一覧の結果が 1000 件を超えても、objstore の `List` と `DeleteMany` が全件を扱う

### API の形

- 事項: syncore・metasync・store に何を足すか
- 決定内容:
  - syncore: `Tombstone{DeletedAt time.Time; DeviceName string}`、`Config.ReadTombstone`、`Plan.RemoteDeleted *Tombstone`、`ErrRemoteDeleted`
  - metasync: `Tombstone`（同じ項目）、`Core.CheckTombstone(ctx, gameID) (Tombstone, bool, error)`、`Core.HandleTombstone(ctx, gameID) (Tombstone, bool, error)`。`validateTombstone` を、検証した値を返す形に広げる。`ObjectStore` に `DeleteMany` を足す
  - metasync の `LocalStore`: `RemoteDeleted(ctx, gameID) (bool, error)` を `RemoteDeleted(ctx, gameID) (Tombstone, bool, error)` に変え、`RecordRemoteDeleted(ctx, gameID string, t Tombstone) error` を足す。store はどちらも `game_locals` の既存の列で実装し、migration は足さない
  - 不正な tombstone と読めない tombstone は、センチネルエラーにせず `%w` で包んで返す
- 判断理由: 記録の値（deviceName・deletedAt）を `status` で表示するには、`RemoteDeleted` が値を返す必要がある。真偽だけの関数と値を返す関数を並べると、同じ列を読む関数が 2 つになるので、既存の関数の形を変える。不正と読めないは、どちらも「止めてエラーにする」（8-T3〜8-T6）で、呼び出し側が分ける振る舞いがない

### CLI の振る舞い

- 事項: `status`・`push`・`pull` が tombstone をどう扱い、何を出すか
- 決定内容:
  - `status`: `plan.RemoteDeleted` があれば「リモートで削除されました（deviceName: …, deletedAt: …）」と出し、正常に終わる。base の記録はしない
  - `push`・`pull`: 最初に `HandleTombstone` を呼ぶ（8-M1）。tombstone があれば、同じ表示を出し、セーブとメタ情報を同期せずに、エラー（終了コード 1）で終わる。`HandleTombstone` が失敗したら、どちらも同期せずにエラーで終わる
  - `push`・`pull` のセーブの同期が `ErrRemoteDeleted` で止まったとき（8-T7）と、`Status` が `RemoteDeleted` の plan を返したときは、`HandleTombstone` を呼んでから同じ表示とエラーで終わり、メタ情報は同期しない
- 判断理由: `push`・`pull` は頼まれた同期をしていないので、スクリプトから失敗として見分けられるよう、エラーで終わる。判定が合わずに見送ったとき（終了コード 0）とは違い、もう一度実行しても同期できる見込みがない。8-T7 の後にメタ情報を同期しないのは、`HandleTombstone` の記録が失敗していると、`Sync` が 8-M4 の確認を通ってしまい、削除したデータをリモートに戻すためである。`status` は表示だけを行う（8-T2）ので、tombstone があることは失敗ではない

### テスト

- 事項: どう確かめるか
- 決定内容:
  - metasync は `package metasync_test` で、本物の SQLite と memstore を使い、8-T1〜8-T6・8-T8 を `CheckTombstone` と `HandleTombstone` の両方で書く。8-T2 は、記録・削除するキーと残すキー（`deleted.json`、ほかのゲームのキー）を確かめる。消し残りの削除は、`DeleteMany` に失敗を仕込んだ後に、もう一度 `HandleTombstone` を呼んで確かめる。記録があり `deleted.json` がないときは、何も削除しないことを確かめる
  - syncore は `package syncore_test` で、`ReadTombstone` を偽物（指定した回目の呼び出しから tombstone を返す）にして、8-T2（判定しない）・8-T3（エラー）・8-T7（Push・Pull それぞれ）・8-T8（上書きでも拒否）と、nil のときのエラーを書く
  - store の `RecordRemoteDeleted` と `RemoteDeleted` は、metasync のテストで本物の SQLite を通して確かめる。cmd の配線にはテストを書かない
- 判断理由: AGENTS.md のテストの方針に従う。syncore のテストで metasync を組み立てないのは、syncore が受け取るのは `ReadTombstone` の結果だけで、確認の中身は metasync のテストが確かめるためである

### PR の大きさ

- 事項: この作業の PR をどう切るか
- 決定内容: 1 つの PR にする。見込みは約 700 行（テスト約 350、metasync 約 100、syncore 約 60、store 約 40、CLI 約 60、ADR 約 100）
- 判断理由: Issue #100 の見込み（約 450 行）は、syncore の 8-T7 を Push・Pull で別々に確かめるケースと、metasync の 2 つの入口それぞれのケースを数えていない。直近の ADR（2026-11-14_02-46_remote-game-import.md）で、条件ごとのケースが見込みを押し上げたことを踏まえ、テストを多めに見積もった

## 結果 / 影響

（作業の後に書く）

## 更新履歴

- 2026-11-14 19:07 作成
