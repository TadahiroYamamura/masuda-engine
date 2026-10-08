# HANDOFF
## 作業項目
2026-10-08: masuda#93。ワークフローのトップに`user_invocable`（省略時true）を書けるようにした（`6fff69f`、`main`にpush済み、未タグ）。masudaのセッション（masuda-d3）がユーザーの判断で契約を変えて実装した。

- 契約: `Workflow.UserInvocable`（`engine/api.go`）、`docs/workflow-schema.md`のワークフローファイルの例と`user_invocable`の説明
- 読み込みは真偽値だけを受け付ける（`boolValue`、`engine/yamlutil.go`）。エンジンは値を写すだけで、実行・検査には使わない。falseのワークフローも始められ、検査もされる
- 同梱: `fix/build-step`・`implement/build-step`・`review/cross-cutting`・`review/perspectives`・`smoke`にfalse。`develop`・`fix`・`review`は省略（true）
- masuda側: `WorkflowEntry.user_invocable`を足し、`masuda workflow list`は既定でtrueだけ、`--all`で全部を出す。masudaのgo.modは`main`の擬似バージョン

同日: #11。同梱の`fix`で、plan gateの`rejected`を新しい`replan`ノードへ向けた（`20fdcb8`、`main`にpush済み、未タグ。masudaのセッションが委ねたサブエージェントが実装し、監督が差分・テスト・壊す確認をした）。

- `replan`: role・continuesとも`agents/quick-planner`、`inputs: [plan, investigation]`、`max: 3`。`done`・`exhausted`は`approve-plan`、`out_of_scope`・`needs_human`は`plan`と同じend
- `quick-planner.md`に「人間の却下理由」の節（却下理由で指摘された箇所と、それで前提が変わる箇所だけを直す。指摘の無いステップの分け方・文面・`files`は変えない）。実装の`stuck`はゲートへ進むので、「行き詰まって戻ってきた場合」の文は消した
- masudaのgo.modは`v0.3.1-0.20261008084012-20fdcb8f73ec`（masuda `3300fe2`、`docs/user/workflows.md`のfixの説明も合わせた）

前回（2026-10-07）のmasuda#99（`type: privileged`ノード、v0.3.0）の要点は注意点に残した。
## 完了した契約テスト
- `go build ./... && go vet ./... && go test ./...`が緑（`20fdcb8`）
- 足したテスト: `TestUserInvocableは省略時trueで書いた値を写す`（load_test.go）、`TestBundledのuser_invocableは利用者が始めるものだけtrue`（bundled_test.go。同梱を足したら表にも足さないと落ちる）、`TestLoadRejectionReasons`の「user_invocableが真偽値でない」。既定値を変える・smokeのfalseを消すと落ちることを確かめた
- #11: fixの歩行テストを`Test同梱のfixは計画の却下を前回の計画の続きで直しpublishする`に広げた（replanの役・feedback・入力の出現・Continues）。`rejected`を`plan`に戻す・inputsを外す・continuesを外すと落ちる
## 未完と理由
- v0.4.0のタグ: masudaが契約を変えたので次はv0.4.0。masudaのリリース（Skill `release`）のときに、engineにも同じタグを打つ
- `replan`の`exhausted`→`approve-plan`の経路はテストしていない（developの`revise-rejected`も同じ）
- `docs/work-orders.md`のE14の「`workflows/fix`は変えない」は、当時の判断の記録として残した
- 同梱のdevelopで特権コマンドを強制する方法（宣言があるときだけprivilegedノードを通す等）は決めていない
## 次の一手
1. masudaのv0.4.0のリリースで、engineの`main`の先頭にv0.4.0を打つ（masudaのgo.modをタグに上げる）
2. privilegedノードの強制の方法を、masuda側の利用の様子を見て決める
## 注意点
- ワークフローのトップのキーを足すときは、`parseWorkflow`の`switch`（知らないキーは拒む）と`workflow-schema.md`の両方を直す
- 同梱のワークフローを足したら、`TestBundledのuser_invocableは利用者が始めるものだけtrue`の表に足す
- Runnerのインターフェースを変えると、masudaの`internal/runner`とフェイク（contract・engineのテスト）が追従する必要がある
- privilegedノードはmasudaの`advance()`が`c.mu`を握ったまま同期で呼ぶ。masuda側の実行の関数で`c.mu`を取ると止まる（masudaの契約テストC-M11が検出する）
- ワークフローの形への外部の指摘（ワークフローのoutputs・outcomesの宣言、revise系3ノードの統合、optionalなinput）は、masuda側で検討した上でユーザーが却下した
## 契約への提案
- なし
