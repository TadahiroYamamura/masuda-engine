# HANDOFF
## 作業項目
E13（masuda #69、契約変更・承認済み）: 役定義のfrontmatterに`model`・`effort`を書けるようにした。

- `077256e` `engine/api.go`の`Agent`に`Model`・`Effort`（省略は空文字列）。`engine/parse_agent.go`で読み込み: `model`は空でない文字列のみ検査（別名・フルID・`inherit`を通す）、`effort`は`low`・`medium`・`high`・`xhigh`・`max`以外を`effort: must be one of low, medium, high, xhigh, max (line N)`で拒否（リスト等スカラーでない値も同じメッセージ）。`AgentTask`には足していない（`AgentTask.Agent`で届く）。テスト`TestAgentModelAndEffort`（サブテスト名は日本語の文）を`engine/load_test.go`に追加
- `3bdda50` `docs/workflow-schema.md`: エージェント定義の例に`model: sonnet`・`effort: low`、箇条書きに指示書の文、「続き」に起動時の`model`・`effort`のまま動く旨を1行
- 同梱の役（`engine/defaults/agents/*.md`）・`bundled_test.go`・`continues`の検査は変えていない
## 完了した契約テスト
C-E1〜C-E9は無修正で緑（`go build ./... && go vet ./... && go test -count=1 ./...`）。
判定の分岐をわざと壊して`TestAgentModelAndEffort`が落ちることを確かめて戻した: effortの値検査を無効化／列挙から`xhigh`を外す／`model`の空検査を外す／`Model`・`Effort`をAgentに入れない、の5通り。
## 未完と理由
- なし
## 次の一手
- masuda側のM14b: `internal/guest.AgentFile`と`docs/guest-protocol.md`で`model`・`effort`をサブエージェント定義のfrontmatterへ書き出す。engineの版上げ（`go get ...@main`）が先に要る
## 注意点
- `model`の値は検査しないので、打ち間違い（例: `sonet`）はゲストのClaude Code側でしか分からない（意図どおり）
- `../masuda`は変更していない。pushしていない
## 契約への提案
- E13で`Agent.Model`・`Agent.Effort`を足した（承認済み）。masuda側は`internal/guest.AgentFile`と`docs/guest-protocol.md`で追従する（M14b）
