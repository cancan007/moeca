# デモ用ナレッジコーパス

Knowledge 画面と Settings → RAG を、実データ無しで動かして見るための架空のコーパスです。
決済基盤と在庫管理という 2 つのプロジェクトを持つ架空の会社の資料、という設定です。

`scripts/seed-demo.sh` がこのフォルダをインデクサに登録し、organization / project /
group / relation を作って再インデックスまで走らせます。埋め込みキーが無い環境では
`ORCHESTRA_EMBED_MODE=offline` で索引できます（詳細はリポジトリ直下の README）。

## 中身と、それぞれが確かめるもの

| パス | 形式 | このアプリの何を見せるか |
|---|---|---|
| `docs/*.md` | Markdown | 通常のテキスト索引 |
| `data/incidents.csv` | CSV | 列ヘッダ付きで 1 行 1 レコードに展開されること |
| `data/slo-targets.tsv` | TSV | タブ区切りも同じ扱いになること |
| `specs/settlement-spec.pdf` | PDF | テキスト層がページ単位で抽出されること（`--- p.1 ---`） |
| `media/demo-checkout.mp4` | 動画 | **中身は索引されない**（パスとファイル名のみ）こと |
| `media/demo-checkout.ja.vtt` | 字幕 | 動画の発話が、字幕ファイル側の索引として引けること |
| `design/*.svg` | 画像 | 画像もパスとファイル名のみになること |

## 断っておくこと

`media/demo-checkout.mp4` は**中身の無いプレースホルダ**です。動画のバイト列はこの
アプリでは一度も読まれない（読むには全ナレッジを持つコンテナに ffmpeg を入れることに
なる）ので、デモとしてはこれで十分であり、置いてあること自体がその設計の説明になります。
発話内容を検索させたければ隣に字幕ファイルを置く — というのがこのアプリの答えで、
`demo-checkout.ja.vtt` がその実例です。

`specs/settlement-spec.pdf` は非圧縮のテキスト層を持つ最小構成の PDF です。中身は本物の
文章ですが、体裁の整った PDF ではありません。
