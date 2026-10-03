# HANDOFF
## 作業項目
監督の承認を得て契約を変えた。agentノードに任意の`continues: agents/<役>`を足し、エンジンは「このrunでその役が最後に担当した出現」を`AgentTask.Continues`で渡す。同梱の修正（fixer）を直前のimplementerのサブエージェントへ続けて渡し、fixerが指摘に反論できるようにした。

- 契約（d1c3a39）: `engine/api.go`に`Node.Continues`・`AgentTask.Continues`。`docs/workflow-schema.md`に`continues`のキー、「続き」の節（意味と「記憶はあれば使う。無くても成立する入力を常に渡す」）、`Set.Check`の規則、「エンジンが用意するもの」の節、`findings`の`disputed`・`response`
- 宛先の決め方: 進入時（`prepareAgent`）に記録の全出現から「`Exhausted`でなく、resultがあり、出現のworkflow・nodeから引いたノードが`type: agent`で`role`が一致する」もののうち出現IDが最大のものを選び、出現の記録`continues`に残す。`task()`はそれを写す。resumeで再発行されるタスクも同じ宛先を指す。記録の形は`occurrence.continues`（omitempty）を足しただけ
- `Set.Check`: 続ける側のtoolsが続けられる側のtoolsの部分集合（nil＝全ツールは続けられる側もnilのときだけ）、続けられる役のagentノードがrootから到達可能なワークフローにあること、`continues`の役が存在すること
- 同梱（06ae81f）: build-step・develop・fixの`fix`に`continues: agents/implementer`。fixerは`inputs: [findings, plan]`・`outputs: [findings]`で、誤りと判断した指摘を同じ`id`で`disputed: true`・`response`付きに書き直す。recheckerは`outputs: [findings]`で、反論を認めれば`withdrawn: true`、退ければ`unresolved`。implementerの末尾に続きのタスクの一文、synthesizerに「反論が退けられ人間の判断待ちの指摘」
## 完了した契約テスト
C-E1〜C-E8すべて緑（`go build ./... && go vet ./... && go test -count=1 ./...`）。C-E8は新設: 宛先が別フレームの最新の終了済み出現になること、該当が無ければ空、`continues`無しは空、Advanceの繰り返しで同じ宛先、toolsが部分集合でない／全ツールで続ける／続けられる役が到達範囲に無い／役が無い定義の拒否、execノードの`continues`の読み込み拒否。`engine/bundled_test.go`はdevelop・fixの歩行で全fixerの`Continues`が直前のimplementer（build-stepはそのステップ、developの最終の修正は最後のステップかrework）を指すことを確かめ、反論→unresolved×3→exhausted→interimゲートの経路を足した
## 未完と理由
- synthesizerの「反論して取り下げられた指摘」は書けない。`withdrawn: true`の要素は累積データの保存時に除かれる（`mergeAccumulated`）ので、synthesizerは台帳から読めない。下の「契約への提案」
- 実機での確認はしていない（masuda側の追随が要る）
## 次の一手
- masuda側の追随（下の「注意点」）を行い、実機で続きが成立すること・成立しないとき新しく起動することを確かめる
- 取り下げた反論をレポートに載せる方法を決める（「契約への提案」）
## 注意点
- **契約が変わったので次のリリースはYを上げる（v0.2.0）**
- masuda側で要る追随:
  - `docs/guest-protocol.md`の`next_task`応答に`continues: {occurrence, agent_id}`を足し、ループ規約（`agent_id`があればSendMessageで続ける。失敗・不在なら新しく起動する）を書く
  - 出現ごとにゲストが起動・使用したサブエージェントの`agent_id`を記録し、`AgentTask.Continues`の出現から引けるようにする（続けた出現は続けられた側と同じ`agent_id`になる）
  - タスクファイルに「## 続き」の節（続きとして渡されたこと、役の指示が切り替わること）
  - liveのテスト2つ（続きが成立する場合、VM再開後などで続けられず新しく起動する場合）
  - `go.mod`のengineの版上げ（`go get github.com/TadahiroYamamura/masuda-engine@<tagまたはmain> && go mod tidy`）
  - 同梱エージェントの説明（fixerの反論、recheckerの裁定、findingsの`disputed`・`response`）を書いている文書があれば合わせる
- 出力は`done`の報告でしか受け付けられない。そのためfixerは反論を`done`で確認へ回し、`cannot_fix`は「直すべきだが直せない」だけ。recheckerは`unresolved`のとき取り下げを書けないので、認めた反論はfeedbackで伝え、fixerが次の報告で再び挙げて次の確認で取り下げる。上限に達すると認めた反論も`disputed`のまま残る
- どの反論を裁定するかはfixerのfeedbackの`id`で示す。以前に裁定されて人間の判断待ちになった`disputed`の指摘は、後のステップの修正・確認では触らない（本文にそう書いた。エンジンは区別しない）
- recheckerは計画を読まない（入力は`[findings, step-diff]`のまま）。fixerには`plan`を足した
- toolsの比較は名前の完全一致
## 契約への提案
- 取り下げられた指摘をレポートで見せるため、累積データの`withdrawn`の要素を保存時に捨てず、読み出し・foreachでだけ除き、synthesizerのような「全部を読む」入力を別に設けるか（例: `findings-all`のようなエンジンが用意するデータ）
- エージェントの出力を`done`以外の終わり方でも受け付けるか（recheckerの`unresolved`で取り下げを同時に書けるように）
- 前回からの持ち越し: 同梱スキーマの列挙から`selected-perspectives`を外すか、`workflow`ノードに`inputs`を書けるようにするか（reviewerへ計画を渡す手段）
