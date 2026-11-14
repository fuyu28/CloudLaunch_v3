# tombstone-in-syncore

- 日時: 2026-11-14 23:29 (JST)

## 当初の要求 / 背景

ゲームの tombstone（`games/{gameId}/deleted.json`、仕様書 8 章）の扱いが、metasync・syncore・cmd に散っている。

- metasync: 検証（8-T4〜8-T6）、`CheckTombstone`・`HandleTombstone`（8-T1〜8-T8）、ゲームの削除での PUT（`DeleteRemoteGame`、8-X5・8-X6）、2 台目の PC での取り込みの確認（8-N7・8-N8・8-N12）、上げ直しの前提の確認（記録がなければ `ErrNotRemoteDeleted`）
- syncore: `Tombstone` 型の二重定義、`Config.ReadTombstone` の閉包と、閉包がないときの `errReadTombstoneNotSet`、`DeleteGame` の `deleteRemote` の閉包
- cmd: 閉包の組み立てと、`syncore.Tombstone(t)`・`metasync.Tombstone(...)` の型の詰め替え

この分け方は、2026-11-14_19-07_game-tombstone-check.md の「syncore と metasync の分担」で、tombstone の確認と掃除を metasync に置いたことから来ている。その後、ゲームの削除（2026-11-14_20-30_game-delete.md）はローカル側を syncore に、リモート側を metasync に置き、上げ直し（2026-11-14_22-38_game-reupload.md）もロックとジャーナルを syncore に、前提の確認と DB の更新を metasync に置いた。tombstone を使う手順のうち、ロック・ジャーナル・base・判定といった syncore の概念に触れるものが増え、そのたびに閉包が増えた。

syncore と metasync は互いに import しない（2026-10-23_00-30_package-structure.md）。この制約は保つ。

## 仕様

- tombstone の扱いをどこに置くか
- metasync が tombstone を知る方法
- 上げ直しの前提の確認の置き場所
- メタ情報の同期の 8-M4 の扱い
- API の形
- 文書の変更
- テスト
- PR の大きさ

## 決定事項

### tombstone の扱いの置き場所

- 事項: tombstone の型・キー・検証・確認・記録・配下の削除・PUT をどのパッケージに置くか
- 決定内容: すべて syncore に置く。型・キー・検証（8-T4〜8-T6）、確認（リモートと DB の記録、8-T1〜8-T8）、記録と配下の削除（8-T2・8-X10）、ゲームの削除での PUT と記録（8-X5・8-X6）を syncore に移す。配下の削除は、`games/{gameId}/` を一覧して `deleted.json` 以外を消す形のまま移す。metasync の tombstone.go はなくなる
- 判断理由: 配下の削除は一覧と削除だけで、metasync のキー（game.json・sessions・images）を知らなくてよい。キーの形（仕様書 2 章）はもともと syncore が持つ。syncore に置けば、閉包（`ReadTombstone`・`deleteRemote`）と型の二重定義が消え、確認がリモートと DB の両方を 1 か所で見る。却下した案は次の 2 つ
  - 新しいパッケージ（`internal/tombstone` など）に寄せる案: 移せるのは型・キー・検証だけで、記録と削除は syncore か metasync に残るので、扱いが 2 か所に分かれたままになる。syncore と metasync の両方が import する、仕様の概念のパッケージという新しい種類の層ができ、`domain` のような何でも入る置き場に育ちやすい
  - syncore と metasync を 1 つのパッケージにまとめる案: 仕様書 8 章冒頭の「メタ情報は確認なしで同期する」と、セーブの同期の「確認を取る」という性質の違いを、パッケージの境界が表している。まとめると 1 つの `Core` が両方の依存を持ち、どの操作がどの依存を要るかが型から読めなくなる

### metasync が tombstone を知る方法

- 事項: 取り込み（8-N7・8-N8・8-N12）が tombstone を確かめる方法
- 決定内容: metasync が、副作用なしで「ない・ある・不正」を区別できるインターフェース `TombstoneReader`（`RemoteTombstoneExists(ctx, gameID) (bool, error)`）を定義し、`Config.Tombstones` で受け取る。「不正」は metasync のセンチネルエラー `metasync.ErrInvalidTombstone` で包んだエラーで表す。cmd の小さなアダプタが `*syncore.Core` の `ReadRemoteTombstone` を包んで実装し、`syncore.ErrInvalidTombstone` を `metasync.ErrInvalidTombstone` で包み直す。`Config.Tombstones` が nil なら、取り込みはリモートを読まずにエラーにする。取り込むゲームは未登録で DB に記録がないので、確認は DB を見ずにリモートだけを読む
- 判断理由: 依存の向き（コアが必要な操作のインターフェースを定義し、外が実装する）の形をそのまま使える。metasync は syncore を import できないので、エラーの型は metasync が持ち、詰め替えは配線の側に置く。8-N8 は不正な tombstone を「除いてログに残す」ので、読めないとき（8-N11）と区別できる必要がある。状態の列挙型を別に作るより、センチネルエラーで包めば検証の詳細をログに残せる。nil のときに確認を飛ばすと、削除済みのゲームを取り込む道ができる

### 上げ直しの前提の確認

- 事項: 「tombstone の記録がなければ上げ直さない」確認と `ErrNotRemoteDeleted` の置き場所
- 決定内容: syncore の `ReuploadGame` に移す。ロックを取り、Pull ジャーナルを確かめた（8-U3）後、閉包を呼ぶ前に確かめる。センチネルエラーは `syncore.ErrNotRemoteDeleted` にする。記録の削除そのものは、8-U4 のトランザクション（store の `ReuploadGame`）に残し、store もトランザクションの中で記録を確かめ続ける
- 判断理由: tombstone の DB の記録を見るのは syncore、という分担がそろう。確かめる順（ジャーナル → 記録）は今と変わらない。記録の削除は、ゲーム情報とプレイ履歴の移動と同じトランザクションで行う要求（8-U4）があるので動かさない

### メタ情報の同期の 8-M4

- 事項: `metasync.Sync` が DB の記録を見て同期しない処理（8-M4）を、この作業で移すか
- 決定内容: 移さない。metasync の store のインターフェースの `RemoteDeleted`・`RecordRemoteDeleted` を、有無だけを返す `IsRemoteDeleted(ctx, gameID) (bool, error)` に置き換え、store に足す。8-M5 の作業で外す
- 判断理由: この作業は振る舞いを変えない。8-M4 を syncore に尋ねる形にするには、リモートの tombstone も読む「扱いまで」の操作が要り、それは仕様書 8-M5（#137）の振る舞いの変更になる。store の `RemoteDeleted` は syncore の `Tombstone` を返すようになり、metasync は syncore の型を書けないので、同じ名前のメソッドを両方のインターフェースで使えない

### API の形

- 事項: 移した後の公開 API
- 決定内容:
  - syncore: `CheckTombstone(ctx, gameID string) (Tombstone, bool, error)` と `HandleTombstone(ctx, gameID string) (Tombstone, bool, error)` を `*Core` に置く。振る舞いは今の metasync のものと同じにする
  - syncore: DB を見ずに deleted.json だけを読む `ReadRemoteTombstone(ctx, gameID string) (Tombstone, bool, error)` を置く。不正なら `ErrInvalidTombstone` で包んで返す。`CheckTombstone` と `HandleTombstone` も、不正なら同じく包む
  - syncore: `DeleteGame(ctx, gameID string) (DeleteResult, error)` にし、`deleteRemote` の引数をなくす。tombstone の `deletedAt` は `Config.Now()`、`deviceName` は `Config.DeviceName` から作る。`Config.DeviceName` が空なら、ロックを取る前にエラーにする。metasync の `ErrDeleteIncomplete` は `syncore.ErrDeleteIncomplete` にする
  - syncore: `Config.ReadTombstone` と `errReadTombstoneNotSet` をなくす。`LocalStore` に `RemoteDeleted` と `RecordRemoteDeleted` を足す
  - metasync: `CheckTombstone`・`HandleTombstone`・`DeleteRemoteGame`・`Tombstone`・`ErrDeleteIncomplete`・`ErrNotRemoteDeleted` をなくす
- 判断理由: 公開する名前と振る舞いは今の metasync のものを引き継ぎ、cmd と GUI から見た使い方を変えない。削除の時刻と deviceName は、今は cmd が `time.Now()` と設定から作っているが、syncore は既に両方を `Config` で受け取っている。deviceName が空のまま tombstone を書くと 8-T5 に当たり、どの PC もそのゲームを確かめられなくなる。今は cmd が必ずホスト名を渡すが、`Config` で受け取る以上、空を拒む場所が syncore に要る

### 文書の変更

- 事項: AGENTS.md と過去の ADR
- 決定内容: AGENTS.md の「構成」の syncore の行に「ゲームの tombstone とゲームの削除（8 章）」を足し、metasync の行から「ゲームの tombstone の確認と掃除もここが持つ」を消す。次の部分を、この ADR で置き換える。それぞれの更新履歴に追記し、本文は書き換えない
  - 2026-10-23_00-30_package-structure.md の「依存の向き」のうち、tombstone の問い合わせの部分
  - 2026-11-14_19-07_game-tombstone-check.md の「syncore と metasync の分担」と、「API の形」のうち tombstone の確認と扱いを metasync に置く部分
  - 2026-11-14_20-30_game-delete.md の「syncore と metasync の分担と、ロックを握る範囲」のうち `deleteRemote` の閉包の部分と、「metasync の tombstone の PUT と、止まった場所の伝え方」
  - 2026-11-14_22-38_game-reupload.md の「上げ直せるゲームの条件」のうち、記録を metasync で確かめる部分
- 判断理由: docs/adr/README.md の「過去の決定を覆すとき」に従う

### テスト

- 事項: 移すテストと書き直すテスト
- 決定内容: metasync の tombstone_test.go の 8-T・8-X のケースを、syncore の外部テストパッケージに移す。ケース名は変えない。metasync の取り込みのテスト（8-N7・8-N8・8-N12）は、状態を返す偽物のインターフェースで書き直す。上げ直しの前提の確認のテストは、syncore の reupload のテストに移す
- 判断理由: 検査する仕様書の行は変わらず、ケースの置き場所だけが変わる。metasync の側は、tombstone の読み方ではなく、状態ごとの取り込みの振る舞いを検査すればよい

### PR の大きさ

- 事項: PR の分け方
- 決定内容: 1 つの PR にする。見込みは約 900 行で、うち約 550 行はテストとコードの移動（テスト約 450、syncore 約 200、metasync 約 50、cmd 約 60、store 約 10、ADR・AGENTS.md 約 130）
- 判断理由: 確認・記録・削除・PUT は同じ関数（記録と配下の削除）を共有しているので、途中で分けると、両方のパッケージに同じ処理が一時的に並ぶ。移動の部分は、移す前と後を並べて読める

## 結果 / 影響

- tombstone の型・検証・確認・記録・配下の削除・PUT が syncore の 1 か所にそろった。`Config.ReadTombstone` と `DeleteGame` の `deleteRemote` の閉包、`Tombstone` 型の二重定義と cmd の詰め替えがなくなった
- metasync から syncore への問い合わせは、取り込みのための `TombstoneReader` 1 つだけになった。代わりに、不正な tombstone のエラーを包み直す cmd のアダプタが残る。cmd にはテストを書かないので、本物の検証からアダプタを通って 8-N8 になる道は、metasync のテスト（偽物を使う）では検査されない
- syncore のテストは閉包の偽物をやめ、メモリ上のリモートに本物の deleted.json を置くようになった。承認の後に tombstone が書かれる場面は、n 回目の GET の前に置くフックで作る
- 8-M4 のために store に `IsRemoteDeleted` が一時的に残る。8-M5 の作業で外す
- 「syncore = 2・4〜7 章」という章との対応は崩れ、syncore が 8 章の tombstone とゲームの削除も持つ
- 見込みとの差: 差分は約 1,900 行（テスト +651 −559、本番 +301 −270、ADR・AGENTS.md など）で、見込みの約 900 行を超えた。syncore のテストを閉包の偽物から本物の deleted.json に書き換えたため、移すだけでなく既存のケースにも手が入った

## 更新履歴

- 2026-11-14 23:29 作成
- 2026-11-14 23:48 取り込みの確認は未登録のゲームを相手にし、DB の記録を読めないので、syncore にリモートだけを読む `ReadRemoteTombstone` を置くことにした。「不正」の状態は列挙型ではなく `metasync.ErrInvalidTombstone` で包んだエラーで表すことにした。検証の詳細をログに残せるため。store の `RemoteDeleted` が syncore の型を返すので、8-M4 のために `IsRemoteDeleted` を足すことにした。syncore が tombstone を書くので、`Config.DeviceName` が空なら削除を拒むことにした。置き換える過去の ADR に、game-delete と game-reupload の該当部分を足した
- 2026-11-14 23:58 「結果 / 影響」を書いた。レビュー指摘の反映として、`Config.Remote` がなければ削除をロックの前に拒むことにした。移す前は閉包の nil で止まっていたが、移した後はバックアップの後で nil 参照になるため
