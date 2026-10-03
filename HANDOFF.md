# HANDOFF
## 作業項目
実装者が書くコードコメントの品質を安定させるため、追加・変更したコメントを基準つきで列挙させる出力`comment-manifest`を同梱定義に足した。契約（`engine/api.go`）は変えていない。`docs/workflow-schema.md`は同梱スキーマの列挙の1行だけ（`comment-manifest`を追記）。

- スキーマ: 新規`schemas/comment-manifest.json`（累積`x-masuda-accumulate: true`）。配列で、要素は`file`（findingsと同じrelpathのパターン）・`line`（≥1。コメントの先頭行、新ファイル側）・`kind`（`summary`/`choice`/`background`/`tradeoff`/`convention`/`nolint`）・`note`（`\S`）。すべて必須、`additionalProperties: false`。`id`を持たないので書き込みは常に追加になる
- implementer: `outputs: [commit-message, comment-manifest]`。「コメントの書き方」を4基準＋決まり（テストヘルパー・テスト内変数に書かない、全関数にgodocがあるファイルでは揃えてよい、`//nolint:xxx // 理由`、経緯を書き残さない）で書き直し、「コメントの一覧（comment-manifest）」の節を新設
- fixer: `outputs: [findings, comment-manifest]`。基準の要約と列挙の決まりを1段落で
- reviewer: `inputs: [diff, comment-manifest]`。観点がコメントの照合を求めるときに読んで差分と突き合わせる、の1文
## 完了した契約テスト
C-E1〜C-E9すべて緑（`go build ./... && go vet ./... && go test -count=1 ./...`）。C-E7の歩行に`*/comment-manifest`の出力（`[]`）を足した。同梱rootの検査（`workflows/review`を含む）は累積データが常に用意済み扱いなので通る。`engine/bundled_test.go`: develop・fixの歩行のfakeの出力に`comment-manifest: []`を足し、implementer・fixerのタスクの`Outputs`に`comment-manifest`があること、reviewerのタスクの`Inputs`に`comment-manifest`があることを各ステップで確かめる（`checkCommentManifest`）。新設`TestBundledCommentManifestSchema`（累積であること、正常例、`kind`の外の値・`note`空/空白のみ・`line` 0・絶対パス・必須欠落・余分なキー・配列でないものの拒否）
## 未完と理由
- 実機での確認はしていない（masuda側の観点`comment-criteria`が無いと照合されない。下の「注意点」）
## 次の一手
- masuda側の追随を行い、実機でdevelopを1周させて、implementerが実際に一覧を書くか、基準を言えないコメントを消すかを出力で見る
## 注意点
- **implementerとfixerの`done`に`comment-manifest`が必須になった**。次のリリースは同梱定義の変更として扱う。対象リポジトリで`agents/implementer.md`・`agents/fixer.md`を差し替えている場合は影響しない（差し替えた定義の`outputs`に従う）が、同梱の`reviewer`だけを使い続けると一覧は常に`[]`で届く
- masuda側で要る追随:
  - 観点`comment-criteria.md`の新設（`internal/perspectives/builtin/`。観点の数を14と固定している`internal/perspectives/perspectives_test.go`も15へ）。trigger「コードコメントを新規追加・変更する変更」。`comment-manifest`と差分のコメントを照合し、(a) 一覧に載っていないコメント、(b) 載っているが`kind`の基準が成り立たないコメント、(c) テストのヘルパー関数・テスト内の変数へのコメントは`autofix: true`で「削除」の指摘、`nolint`の理由が別行にあるものは`//nolint:xxx // 理由`への書式の指摘。同じファイルの全関数に揃えたgodoc（`convention`）は問題にしない
  - **implementerのいない`workflows/review`（PRレビュー）では一覧は`[]`で届く**。観点の本文で「一覧が空で差分にコメントがある」場合を(a)として一律に削除させないこと（たとえば一覧が無い前提のときは基準だけで判定する、と書き分ける）。また一覧は累積で`id`が無いので、最終レビューでは全ステップ・rework・fixの書き込みが連結されて届き、後のステップの編集で`line`がずれている可能性がある。観点では`file`と近傍の内容で突き合わせるよう書くのがよい
  - 既存の観点`comment-history-leakage`との分担: 経緯・差し戻しへの応答・却下した代替案の書き残しは`comment-history-leakage`が見る。`comment-criteria`は基準（4つ＋例外）と一覧との突き合わせだけを見て、経緯の書き残しには触れない（両方から同じ箇所に指摘が出ないよう、各観点の本文で相手の範囲を除外する）
  - review-checkerは`comment-manifest`を入力に取らない。`comment-criteria`の指摘を検証するとき一覧を読めないので、誤検知の判定がぶれるようなら、masuda側で`agents/review-checker.md`の`inputs`に足すか、エンジン側への追加を依頼する
  - `docs/user/`のワークフロー・観点の説明に`comment-manifest`と`comment-criteria`を書く
  - `go.mod`のengineの版上げ（`go get github.com/TadahiroYamamura/masuda-engine@<tagまたはmain> && go mod tidy`）
  - masudaの契約テスト（`contract/contract_test.go`）は独自のsmoke役で歩かせており、`commit-message`だけを書くので影響しない見込み。同梱のdevelop・fixを歩かせるテストがあれば、implementer・fixerの出力に`comment-manifest`（`[]`で可）が要る
## 契約への提案
- 前回からの持ち越し: 取り下げられた指摘をレポートで見せるため、累積データの`withdrawn`の要素を保存時に捨てず、読み出し・foreachでだけ除き、synthesizerのような「全部を読む」入力を別に設けるか
- 前回からの持ち越し: 同梱スキーマの列挙から`selected-perspectives`を外すか、`workflow`ノードに`inputs`を書けるようにするか（reviewerへ計画を渡す手段）
