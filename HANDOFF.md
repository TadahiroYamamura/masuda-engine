# HANDOFF
## 作業項目
同梱定義の変更を2つ行った。engineの実行ロジックと契約（`engine/api.go`・`docs/workflow-schema.md`）は変えていない。

- C: implementerの入力に`investigation`を足した（`inputs: [plan, investigation]`）。本文に「調査結果の流儀に合わせ、既にある機能を重複して作らない」を足した。investigatorの調査結果が実装者に届いていなかったことへの対処
- A: 小さな修正向けの`workflows/fix`を足した。新しい役`agents/quick-planner`（読み取り専用、`[instructions]`→`[investigation, plan]`、outcomeは`done`・`out_of_scope`・`needs_human`）が調査と計画を1セッションで書き、計画の承認の後に`workflows/fix/build-step`（implement→test→commit、途中レビューとinterimゲート無し）を各ステップに回す。最終レビューは`workflows/review/perspectives`（reviewer→checker）、`done`ならfixer→rechecker、`clean`なら`review-commit`へ直結。以後はdevelopと同じreview gate・rework。publishは`export`無し
- developとの違い（`workflows/fix.yaml`の先頭コメントにも書いた）: 調査と計画が1セッション、途中レビュー無し、横断チェック無し、レポート無し（人間はreview gateで差分とstagingのコメント（指摘）を見る）。計画を立てられないときの出口は`end:needs_human`
- 1セッションにまとめられるのは計画まで。`docs/workflow-schema.md`の「承認済み計画が無い状態で、書き込めるエージェントや`commit`に到達しない」により、実装の前に`approval`（target: plan）が要るため
## 完了した契約テスト
C-E1〜C-E7すべて緑（`go test -count=1 ./...`）。`go vet ./...`指摘なし。C-E7の同梱rootの検査に`workflows/fix`が入る（`workflows/fix/build-step`は呼ばれる側なのでrootにならない）。`engine/bundled_test.go`で、developの歩行テストにimplementer（build-stepとrework）が`investigation`を受け取ることの確認を足し、`workflows/fix`を1ステップで歩かせるテスト（rootであること、`Set.Check`、計画の承認、fix→recheck、review gateの差し戻し→rework、2回目のレビューの`clean`から`review-commit`への直結、publish、コミットのscopeが`step, plan, plan, plan`）を足した
## 未完と理由
- reviewer・synthesizerに`plan`を渡すことはしていない。reviewerはdevelopと`workflows/review`（計画が無い）で共有する`workflows/review/perspectives`の中のノードで、developの`review`は`workflow`ノード（任意キーは`with`・`max`のみで`inputs`を書けない）なので、developの経路だけに`plan`を足す手段が無い。perspectivesの`inputs`に`plan`を足すと`workflows/review`の検査で欠落になる。synthesizerはdevelopの`report`ノード（agentノード）に`inputs: [plan]`を書けば足せるが、渡した`plan`をどう使うかの本文が未合意なので、reviewerと合わせて後続の作業で扱う
## 次の一手
- reviewer・synthesizerへの`plan`の渡し方を決める。案: (a) synthesizerだけdevelopの`report`ノードの`inputs`で足す、(b) develop用に`workflows/review/perspectives`を分けて（計画ありの版）reviewerのノードに`inputs: [plan]`を書く、(c) 契約の変更（`workflow`ノードの`inputs`や任意入力）を提案する
- masuda側で`go.mod`のengineをこの版に上げ、実機で`workflows/fix`を1周させる（quick-plannerの調査結果と計画の質、所要時間）
## 注意点
- masuda側で追随が要るもの:
  - `docs/user/workflows.md`の同梱ワークフローの表と図に`workflows/fix`（と`workflows/fix/build-step`）を足す。developとの違い（上記）も書く
  - 同梱エージェントの個数（quick-plannerが増えて12個）を書いている箇所
  - `go.mod`のengineの版上げ（`go get github.com/TadahiroYamamura/masuda-engine@<tagまたはmain> && go mod tidy`）
  - `end:needs_human`はfixで新しく現れる終わり方。masudaが終わり方のラベルを表示・分類しているなら扱いを確かめる
- implementerは`investigation`を必須の入力に取るので、implementerを使うワークフローは`investigation`をそれより前に用意する必要がある（同梱ではdevelopのinvestigatorとfixのquick-planner）。対象リポジトリでimplementerを使う自前のワークフローがあれば`Set.Check`で拒否されるようになる
- fixのreworkも`investigation`を読むが、これは最初の計画時の調査結果のまま（reworkの前に調べ直さない）
- fixの`plan`の`max: 3`には`exhausted`の行き先を置いていない（developの`plan`と同じ）。上限に達すると実行はblockedで止まる
- contractのC-E7は`*/selected-perspectives`の出力を用意しているが、もう使われない（無害なので触っていない）
- `docs/work-orders.md`の旧記述はそのまま
## 契約への提案
- `docs/workflow-schema.md`の同梱スキーマの列挙から`selected-perspectives`を外すかどうか（前回からの持ち越し）。外すなら`schemas/selected-perspectives.json`を削除できる
- reviewerに計画を渡す手段として、`workflow`ノードにも`inputs`（呼び出し先の各ノードへ追加で渡す入力）を書けるようにするか、エージェントの`inputs`に任意入力を設けるかを検討する価値がある（上の「次の一手」の(c)）
