# store-time-year-range

- 日時: 2026-11-15 01:21 (JST)

## 当初の要求 / 背景

store は日時を `timeLayout`（`2006-01-02T15:04:05.000Z`、仕様書 3 章の日時の形）で書き、`time.Parse` で読み戻す。UTC の年が 4 桁に収まらない日時を書くと、その行は読み戻せず、読むたびに失敗する。

この制約は日時を書く store のすべての操作に当てはまるが、年を確かめているのは一部だけである。

- 確かめている: `CreateSession`・`ImportSessions`（`validateSession`）、`ReplaceGameInfo`・`RegisterGame`・`ReuploadGame`（`validateGameInfo`）、`EditGame`、`AddLegacySessions`
- 確かめていない: `CreateGame`、`InsertPullOperation`、`RecordRemoteDeleted`

確かめていない操作に渡る日時は、今は PC の時計か、リモートから読んで仕様書 3 章の形に一致することを確かめた値なので、年は 4 桁に収まる。ただ、確かめる場所が操作ごとに散っていると、日時を書く操作を足したときに確かめ忘れる。

## 仕様

- 年を確かめる場所
- 受け付ける年の範囲
- エラーの形
- 仕様書の扱い
- テスト

## 決定事項

### 年を確かめる場所

- 事項: どこで年を確かめるか
- 決定内容: `formatTime` を `(string, error)` にし、UTC の年が範囲を外れたらエラーを返す。日時を書くすべての操作は `formatTime` を通す。`validateTime` は `formatTime` に入れてなくし、`validateSession`・`validateGameInfo`・`EditGame`・`AddLegacySessions` にある個別の呼び出しは外す。ID・タイトル・sha256 の検証はそのまま残す
- 判断理由: Go の日時を DB の文字列にする場所は `formatTime` の 1 か所なので、そこで確かめれば、日時を書く操作を足しても確かめ忘れが起きない。ただし、SQL の中で日時を計算して書く処理は `formatTime` を通らない。今は `EditGame` の strftime（今の updated_at に 1 ms を足す）だけで、上限の 9999 年の最後では strftime が NULL を返すので、範囲外を書かない。SQL で日時を計算する処理を足すときは、範囲を別に確かめる必要がある。操作ごとに `validateTime` を呼ぶ形のままにする案は、今の確かめ忘れと同じことが繰り返されうるので採らない

### 受け付ける年の範囲

- 事項: 受け付ける UTC の年
- 決定内容: 0〜9999 年にする（今の `validateTime` と同じ）
- 判断理由: `timeLayout` の `2006` で固定長の 4 桁に書け、`time.Parse` で読み戻せる範囲である

### エラーの形

- 事項: 範囲外のときに返すエラー
- 決定内容: センチネルエラーにせず、範囲外の日時を含むエラーを返す。呼び出す操作は、行の ID と列の名前（例: `session {id}: played_at: …`）を付けて返す。多くのセッションをまとめて書くときに、どの行のどの日時が範囲外かを探せるようにするため
- 判断理由: 呼び出し側が振る舞いを分ける必要がない（AGENTS.md「構成」のエラーの方針）

### 仕様書の扱い

- 事項: 仕様書を変えるか、ID を振るか
- 決定内容: 変えない。ID も振らない
- 判断理由: 仕様書 3 章の日時の形を store が守るための内部の検査で、同期の振る舞いは変わらない

### テスト

- 事項: 何をどう検査するか
- 決定内容: store のテストに表を 1 つ作り、日時を書く操作（`CreateGame`・`RegisterGame`・`ReuploadGame`・`InsertPullOperation`・`RecordRemoteDeleted`・`CreateSession`・`ImportSessions`・`ReplaceGameInfo`・`EditGame`・`AddLegacySessions`）ごとに、範囲外の日時でエラーを返し、DB の行が変わらないことを確かめる。範囲外の日時は、UTC の年が 10000 と −1 のものと、手元の時刻では 4 桁に収まり UTC に直すと外れるもの（9999-12-31T23:00−05:00 と 0000-01-01T05:00+09:00）の 4 つにする。ゲーム情報の 2 つの日時（createdAt・updatedAt）は、片方だけを範囲外にした行を別々に置く。境界の 0 年と 9999 年の日時は、書いて読み戻せることを別のケースで確かめる。操作ごとにある年の検査のケースは、この表にまとめて消す
- 判断理由: 検査したいのは「日時を書くすべての操作が範囲外を書かない」ことなので、操作を 1 つの表に並べると、操作を足したときに行を足し忘れたことが読み取りやすい。年を UTC に直してから確かめていること、2 つの日時の両方を確かめていること、範囲の境界がずれていないことは、UTC の日時を 2 つの項目に同時に入れるだけでは検査できない

## 結果 / 影響

- 日時を DB の文字列にする場所と、年を確かめる場所が `formatTime` の 1 か所になった。`CreateGame`・`InsertPullOperation`・`RecordRemoteDeleted` も年を確かめるようになった
- `ImportSessions` と `AddLegacySessions` は、トランザクションを始める前に全件を `formatTime` に通し、1 件でも範囲外なら何も書かない
- ゲーム情報の 2 つの日時をまとめて文字列にする `formatGameTimes` を足した。`RegisterGame`・`ReuploadGame`・`ReplaceGameInfo` が使う
- 呼び出し側から見た振る舞いは、範囲外の日時を渡した 3 つの操作がエラーを返すようになったことだけである。今の呼び出し元が渡す日時は PC の時計か、仕様書 3 章の形を確かめたリモートの値なので、実際に変わる場面はない

## 更新履歴

- 2026-11-15 01:21 作成
- 2026-11-15 01:26 確かめていない操作の一覧を直した。`RegisterGame`・`ReuploadGame` は `validateGameInfo` で年を確かめていた。呼び出し元が `formatTime` だけになる `validateTime` は、`formatTime` に入れてなくすことにした。1 か所からしか呼ばれない関数を分けておく理由がないため。「結果 / 影響」を書いた
- 2026-11-15 01:30 レビュー指摘の反映。テストの範囲外の日時に UTC に直すと外れるものを足し、ゲーム情報の 2 つの日時を別々に検査し、境界の 0 年と 9999 年を読み戻せることを確かめることにした。UTC の日時を 2 つの項目に同時に入れるだけでは、UTC に直さずに年を見る誤りや、片方の項目の確かめ忘れ、境界のずれを検出できないため。エラーに行の ID と列の名前を付けることにした。SQL の中で計算して書く日時（`EditGame` の strftime）は `formatTime` を通らないことを、判断理由に書いた
