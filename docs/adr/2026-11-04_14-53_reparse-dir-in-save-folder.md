# reparse-dir-in-save-folder

- 日時: 2026-11-04 14:53 (JST)

## 当初の要求 / 背景

仕様書 2 章は、セーブフォルダの中に通常のファイル以外（シンボリックリンク、ジャンクション、OneDrive のファイルのような Windows の reparse point など）があれば、manifest を計算せずにエラーにすると決めている。セーブフォルダそのものがリンクのときも同じである。

dev のコード全体のレビューで、この規則が一部のディレクトリに効いていないと分かった。Go 1.27 の `os/types_windows.go` では、reparse point のモードは次のようになる。

- シンボリックリンクとジャンクション（name surrogate の reparse point）: `ModeDir` を付けず、`ModeSymlink` か `ModeIrregular` にする
- それ以外のディレクトリの reparse point（OneDrive のクラウドのフォルダなど）: `ModeDir` を付けたうえで、`ModeIrregular` も付ける

`localEntries`（`internal/syncore/manifest.go`）は、`d.IsDir()` が真なら通常のファイルかどうかを確かめずに中へ進む。セーブフォルダそのものも `info.IsDir()` だけで確かめている。そのため、後者のディレクトリはエラーにならず、中まで読まれる。前者は今の実装でエラーになり、#33 で確かめた内容と一致する。

## 仕様

- ディレクトリをどう見分けるか
- テストを書くか

## 決定事項

### ディレクトリの見分け方

- 事項: 中へ進んでよいディレクトリを、モードのどのビットで見分けるか
- 決定内容: 種類のビット（`Mode().Type()`、`DirEntry.Type()`）が `ModeDir` だけのものを、通常のディレクトリとして中へ進む。`ModeDir` にほかの種類のビットが付いていれば、通常のファイル以外としてエラーにする。セーブフォルダそのものも同じ規則で確かめる。Pull の手順 9 でバックアップへコピーするとき（`copyTree`）も、同じ規則で中へ進むかを決める
- 判断理由: `copyTree` は、通常のファイル以外をエラーにする規則を 2 章のセーブフォルダの規則と揃えている（2026-11-04_11-27_pull-backup-and-recover.md）ので、ディレクトリの見分け方も揃える。Go は、name surrogate でないディレクトリの reparse point に `ModeDir|ModeIrregular` を付ける。`IsDir()` は `ModeDir` だけを見るので、`ModeIrregular` を見落とす。`platform` パッケージで reparse point の属性を直接読む案は、Go が付けるモードで見分けられるので採らない

### テスト

- 事項: この修正をテストで確かめるか
- 決定内容: テストは書かない
- 判断理由: `ModeDir|ModeIrregular` になるディレクトリ（OneDrive のクラウドのフォルダなど）は、CI の Windows で作る手段がない。Linux ではこのモードにならない。#33 で「Windows でジャンクションを使うテストは足さない」としたのと同じ扱いにする

## 結果 / 影響

（作業の後に書く）

## 更新履歴

- 2026-11-04 14:53 作成
