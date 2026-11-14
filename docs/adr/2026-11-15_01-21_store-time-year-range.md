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
- 決定内容: `formatTime` を `(string, error)` にし、UTC の年が範囲を外れたらエラーを返す。日時を書くすべての操作は `formatTime` を通す。`validateTime` は `formatTime` の中だけで使い、`validateSession`・`validateGameInfo`・`EditGame`・`AddLegacySessions` にある個別の呼び出しは外す。ID・タイトル・sha256 の検証はそのまま残す
- 判断理由: 日時を DB の文字列にする場所は `formatTime` の 1 か所なので、そこで確かめれば、日時を書く操作を足しても確かめ忘れが起きない。操作ごとに `validateTime` を呼ぶ形のままにする案は、今の確かめ忘れと同じことが繰り返されうるので採らない

### 受け付ける年の範囲

- 事項: 受け付ける UTC の年
- 決定内容: 0〜9999 年にする（今の `validateTime` と同じ）
- 判断理由: `timeLayout` の `2006` で固定長の 4 桁に書け、`time.Parse` で読み戻せる範囲である

### エラーの形

- 事項: 範囲外のときに返すエラー
- 決定内容: センチネルエラーにせず、範囲外の日時を含むエラーを返す。呼び出す操作は、今のほかのエラーと同じく文脈を付けて返す
- 判断理由: 呼び出し側が振る舞いを分ける必要がない（AGENTS.md「構成」のエラーの方針）

### 仕様書の扱い

- 事項: 仕様書を変えるか、ID を振るか
- 決定内容: 変えない。ID も振らない
- 判断理由: 仕様書 3 章の日時の形を store が守るための内部の検査で、同期の振る舞いは変わらない

### テスト

- 事項: 何をどう検査するか
- 決定内容: store のテストに表を 1 つ作り、日時を書く操作（`CreateGame`・`RegisterGame`・`ReuploadGame`・`InsertPullOperation`・`RecordRemoteDeleted`・`CreateSession`・`ImportSessions`・`ReplaceGameInfo`・`EditGame`・`AddLegacySessions`）ごとに、UTC の年が 10000 と −1 の日時でエラーを返し、DB の行が変わらないことを確かめる。操作ごとにある年の検査のケースは、この表にまとめて消す
- 判断理由: 検査したいのは「日時を書くすべての操作が範囲外を書かない」ことなので、操作を 1 つの表に並べると、操作を足したときに行を足し忘れたことが読み取りやすい。年は上と下の両方の端の外を確かめる

## 結果 / 影響

（作業が終わったら書く）

## 更新履歴

- 2026-11-15 01:21 作成
