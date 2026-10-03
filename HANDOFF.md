# HANDOFF
## 作業項目
同梱定義に「計画の問い立てと回答」の工程を足した（28a5356）。契約（`engine/api.go`）は変えていない。`docs/workflow-schema.md`は同梱スキーマの列挙の1行だけ（`plan`の`checks[]`と`plan-checklist`を追記）。

- スキーマ: `plan`に必須の`checks[]`（`id`は`<大文字の分類ID>-<番号>`、`category`は8種、`answer`は`addressed`/`out_of_scope`なら空不可、`status`は`addressed`/`out_of_scope`/`open`）。新規`plan-checklist`（`claims`・`sets`・`items`・`not_covered`、非累積）
- 役: `plan-questions`（問いを立てる。Read, Grep, Glob, LSP）、`plan-reviser`（答えて計画を直す。plannerと同じtools。plannerの「計画の組み立て方」以下を写した）、`plan-interviewer`（`question`ノード用。openの問いだけ`ask_human`で聞く）。planner・quick-plannerは`checks`に`[]`を書く
- develop: `plan → questions → revise`。`revise`（max 3）は done/exhausted→approve-plan、needs_human→ask、needs_more_investigation→investigate。`ask`（question、role: plan-interviewer、outputs: [answers]）→`revise-answered`（plan-reviser、ノードの`inputs: [answers]`、max 3、行き先はreviseと同じ）
- fixは変えていない
## 完了した契約テスト
C-E1〜C-E9すべて緑（`go build ./... && go vet ./... && go test -count=1 ./...`）。C-E7の歩行に`*/plan-checklist`の出力を足し、`validPlan`ほか既存の計画のフィクスチャ（engineのテスト含む）に`"checks":[]`を足した。`engine/bundled_test.go`: 既存のdevelopの歩行2つにquestions→revise(done)を挿入、新設`TestBundledDevelopAsksOpenChecksBeforeThePlanGate`（revise needs_human→ask→revise-answered→planゲート。answersがrevise-answeredにだけ届き、planゲートの計画がanswer反映済み）、`TestBundledPlanChecksSchemas`（checksとplan-checklistの正常例・不正例。itemsに`answer`を入れた例の拒否を含む）
## 未完と理由
- 実機での確認はしていない（masuda側の追随が要る。下の「注意点」）
## 次の一手
- masuda側の追随を行い、実機でdevelopの計画段階（問い→回答→人間への質問→planゲート）を1周させる
- 問いの質（判定に寄らないか、集合の数え上げが効くか）を実機の出力で見て、plan-questionsの本文を調整する
## 注意点
- **`plan`のスキーマに必須の`checks`が増えた**。次のリリースは同梱定義の変更として扱う。対象リポジトリで`agents/planner.md`等を差し替えている場合、`checks`を書かないと計画が差し戻され続ける
- masuda側で要る追随:
  - `.masuda/pitfalls.jsonl`の読み込み（1行ずつ`{id, category, trigger, question, background}`を検証）と、ゲストの`/masuda/pitfalls.jsonl`への配置。無ければ置かない（plan-questionsは無ければ飛ばす）
  - `gate show`（planゲート）で`plan.checks`を描画する（id・分類・問い・答え・status。openは目立たせる）
  - `question`の表示: develop で`masuda question`に複数の問い（idは`SPEC-1`等、textは問いと判断できなかった理由）が出る。1回の`ask_human`に複数の質問が入る
  - `docs/user/workflows.md`・quickstartのdevelopの工程説明（questions・revise・ask・revise-answered）
  - `go.mod`のengineの版上げ（`go get github.com/TadahiroYamamura/masuda-engine@<tagまたはmain> && go mod tidy`）
  - 同梱エージェントの個数は15（数や一覧を持つ箇所・テストがあれば合わせる）
  - masudaの契約テスト・クライアントのテスト（`contract/contract_test.go`・`cmd/masuda/client_test.go`）で計画のJSONを書いていれば`"checks":[]`が要る。developを歩かせるなら`plan-checklist`の出力も要る
- plan-interviewerはエージェント定義に`outputs`を持たない（fix(defaults)で外した）。答えの保存先は`ask`ノードの`outputs: [answers]`で、エンジンが`ask_human`で溜めた答えを保存する。役は聞いて`done`で終えるだけ
- reviseが`needs_human`で終えるとき、openを含む`plan`を書くよう本文で求めている（done以外で書いた出力も保存される）。書かずに終えるとplan-interviewerはplannerの`checks: []`の計画を読み、聞く問いが無くなる
- 進入回数は人間の判断（ゲート）ごとに数え直されるので、approve-planの差し戻しを繰り返してもquestions・reviseは上限に掛からない。questionsの`exhausted`には行き先が無い（調査のやり直しが重なるとinvestigateが先に上限に達するので、既存のinvestigateと同じ扱いにした）
## 契約への提案
- 前回からの持ち越し: 取り下げられた指摘をレポートで見せるため、累積データの`withdrawn`の要素を保存時に捨てず、読み出し・foreachでだけ除き、synthesizerのような「全部を読む」入力を別に設けるか
- 前回からの持ち越し: 同梱スキーマの列挙から`selected-perspectives`を外すか、`workflow`ノードに`inputs`を書けるようにするか（reviewerへ計画を渡す手段）
