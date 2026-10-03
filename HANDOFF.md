# HANDOFF
## 作業項目
監督の承認を得て契約を変えた。agentの宣言した出力を`done`以外の終わり方でも受け付ける。`done`では従来どおり必須、`done`以外では書かれていれば検証して保存（不正なら差し戻し、書かれていなければ可）。`question`ノードの答えの保存は`done`のときだけ（従来どおり）。execは変えていない（exit 0のときだけ読む）。

- 契約（9a16b1e）: `engine/run.go`の`reportResult`で、`done`以外でも`ReadOutput`→`validateData`→`putOutputs`。`res.Outputs`には保存したものだけを入れるので、`records.latest`・累積の`mergeAccumulated`は既存のまま`done`以外の出現の書き込みを最新として扱う（直す必要は無かった）。差し戻す報告の出力は1つも保存しない。`engine/api.go`は`ReportResult`と`AgentTask.Outputs`のコメントだけ直した（名前・シグネチャは不変）。`docs/workflow-schema.md`に「出力の受け付け」の節を新設し、questionの節・`Set.Check`のデータ可用性（出力は`done`/`answered`の遷移でだけ用意済みとみなす。既存の挙動の明記）・累積データの読み出しを合わせた
- 同梱（66cd4f7）: recheckerはどちらの終わり方でも、認めた反論を同じ`id`の`withdrawn: true`で書く（`unresolved`でも保存される）。fixerは`cannot_fix`でも反論を`findings`に`disputed`・`response`で書き、対象は「維持する」と挙がったdisputedだけ。synthesizerは節3の見出しを「反論が退けられたか裁定されず、人間の判断待ちの指摘」にしただけ
## 完了した契約テスト
C-E1〜C-E9すべて緑（`go build ./... && go vet ./... && go test -count=1 ./...`）。C-E9は新設: `done`以外で書かれた出力の保存と次ノードでの解決（latestになる）、書かれていない出力は`done`以外なら有効、不正な出力は`done`以外でも差し戻し（報告の出力は何も保存されない）、`done`では従来どおり必須。旧実装でC-E9が落ちることを確認した。`engine/bundled_test.go`の反論の歩行は、recheckerがf1を取り下げつつf2を維持して`unresolved`で返し、次のfixerの台帳からf1が消えている経路に更新（旧実装で落ちることを確認）
## 未完と理由
- synthesizerの「反論して取り下げられた指摘」は依然として書けない（`withdrawn`の要素は保存時に除かれる）。下の「契約への提案」
- 実機での確認はしていない（masuda側の追随が要る）
## 次の一手
- masuda側の追随（下の「注意点」）を行い、実機でrecheckerの取り下げ＋`unresolved`が1回の報告で成立することを確かめる
- 取り下げた反論をレポートに載せる方法を決める（「契約への提案」）
## 注意点
- **契約が変わったので次のリリースはYを上げる（v0.2.0）**。前回の`continues`と合わせて同じv0.2.0でよい
- masuda側で要る追随（今回の分）:
  - `docs/guest-protocol.md`の`report_result`の備考「未出力のoutputがあれば拒否」を「`done`のとき未出力のoutputがあれば拒否」に限る語句修正（`done`以外では書かれた出力だけ検証・保存し、不正なら拒否、という意味が読めるように）
  - `go.mod`のengineの版上げ（`go get github.com/TadahiroYamamura/masuda-engine@<tagまたはmain> && go mod tidy`）
  - ゲスト向けの説明（タスクファイル・MCPのツール説明）に「宣言された出力をすべて書いてから終える」相当の記述があれば、`done`のときの決まりであることに合わせる
- masuda側で要る追随（前回の`continues`の分。未着手なら残っている）:
  - `docs/guest-protocol.md`の`next_task`応答に`continues: {occurrence, agent_id}`、ループ規約（`agent_id`があればSendMessageで続ける。失敗・不在なら新しく起動する）
  - 出現ごとにゲストが起動・使用したサブエージェントの`agent_id`を記録し、`AgentTask.Continues`の出現から引けるようにする
  - タスクファイルに「## 続き」の節、liveのテスト2つ、同梱エージェントの説明（fixerの反論、recheckerの裁定、findingsの`disputed`・`response`）を書いた文書があれば合わせる
- `done`以外で書かれた出力も検証されるので、途中まで書いた出力（例: plannerが`plan`を書きかけて`out_of_scope`で終える）が不正なら差し戻される。同梱の役で問題になる例は見つけていないが、実機で`invalid`のイベントが増えないか見ておく
- 同梱エージェントの共通の末尾「宣言された出力をすべて書いてから終えること」は全役に共通の文で、今回は触っていない（`done`の決まりとしては正しい）
- `cannot_fix`は修正の確認を経ずに人間の判断（develop/fixはreview-commit、build-stepはinterimゲート）へ回るので、そこで書いた反論は裁定されないままレポートに載る
- どの反論を裁定するかはfixerのfeedbackの`id`で示す。以前に裁定されて人間の判断待ちの`disputed`は後のステップで触らない（本文にそう書いた。エンジンは区別しない）
- recheckerは計画を読まない（入力は`[findings, step-diff]`のまま）。toolsの比較は名前の完全一致
## 契約への提案
- 取り下げられた指摘をレポートで見せるため、累積データの`withdrawn`の要素を保存時に捨てず、読み出し・foreachでだけ除き、synthesizerのような「全部を読む」入力を別に設けるか（例: `findings-all`のようなエンジンが用意するデータ）
- 前回からの持ち越し: 同梱スキーマの列挙から`selected-perspectives`を外すか、`workflow`ノードに`inputs`を書けるようにするか（reviewerへ計画を渡す手段）
