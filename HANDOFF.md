# HANDOFF
## 作業項目
E9（指摘の取り下げwithdrawn・egressのポート拒否・fixerのcannot_fix・観点の置き場所）。完了
## 完了した契約テスト
C-E1〜C-E7すべて緑（`go test -count=1 ./contract/`）。`go vet ./...`指摘なし、`go test -count=1 ./...`緑
## 未完と理由
なし
## 次の一手
- `docs/work-orders.md`の次の項目へ（E9まででE系は一巡）
- masuda側で、サンドボックスイメージを再ビルドしてからM8の実機1周をやり直し、review-checkerの取り下げ・fixerの`cannot_fix`・trigger-matcherの`/masuda/reviews/`読み込みが実機で働くか確かめる
## 注意点
- 実装の置き場所（E8からの差分）
  - `engine/accumulate.go`: `withdrawn`の判定と、`mergeAccumulated`の最後での除去。withdrawnな要素は**保存値から落とす**（読むたびに除くのではない）。inputs・`GetData`・foreachの項目はどれも保存値を読むので、これだけで契約の「読み出しとforeachの項目から除く」を満たす
  - `engine/parse_workflow.go`の`egress`: `:`を含む要素は「ポートは書けない」として先に拒否し、その後で形（`host`／`*.host`）を検査する
- 保存値から落とす方式の帰結: 取り下げた`id`を後で（withdrawn無しで）書き直すと復活するが、元の位置ではなく末尾に付く。後勝ちのマージ自体は従来どおり
- 同梱の変更
  - `findings`スキーマ: 要素に任意の`withdrawn`（boolean）。取り下げも通常の要素として検証されるので、元の要素の全必須項目＋`"withdrawn": true`で書く
  - review-checker: `outputs: [findings]`を持つ。誤検知は自分で同じ`id`に`withdrawn: true`を足して書いて取り下げ、無ければ`[]`を書く。`inaccurate`は残したが意味を「見落とし・説明の不足」に絞った（誤検知だけなら取り下げて`done`）。perspective-reviewの経路（`done: end`・`inaccurate: review`）は変えていない
  - reviewer: feedbackで取り下げたと書かれた`id`を書き直さない（書くと後勝ちで取り下げが取り消される）
  - fixer: `cannot_fix`（理由をfeedbackに、変更を加えずに終える）。`workflows/fix-finding`で`cannot_fix: end:unresolved`
  - trigger-matcher: 観点の一覧を`/masuda/reviews/*.md`から読む
- masuda側（M5〜）への取り決め（E8からの追加）
  - `GetData`で`findings`を返すときは、従来どおりengineが保存した値をそのまま返せばよい。withdrawnな要素はengineが既に除いている
  - `/masuda/reviews/*.md`にホストの観点のスナップショットを置くこと（`docs/guest-protocol.md`の表のとおり）。trigger-matcherはゲストのcloneの`.masuda/reviews/`を読まない
  - ノードの`egress`はホスト名のみ（ポート付きは`Load`が拒否する）
- 既知の制限: review-checkerの`id`の選び方（「観点名で始まり、出現IDが最も新しいもの」）はプロンプトの案内だけで、engineは検査しない。他の観点の指摘を取り下げてしまう誤りは防げない
## 契約への提案
なし（任意: `workflow-schema.md`「累積データ」の`withdrawn`の行に、「取り下げた`id`を後で書き直すと末尾に付く」と添えてもよい。今の文面でも実装と矛盾はしない）
