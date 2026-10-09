# HANDOFF
## 作業項目
2026-10-08〜09。masudaのセッション（masuda-d3）が、masudaのv0.4.0に向けて次を入れた。どれも`main`にpush済みで未タグ（先頭`042dcd5`）。masudaのgo.modは`v0.3.1-0.20261009033421-042dcd526de2`。

| Issue | コミット | 中身 |
|---|---|---|
| masuda#93 | `6fff69f` | **契約の変更**（ユーザー判断）: ワークフローのトップに`user_invocable`（省略時true）。`Workflow.UserInvocable`（`engine/api.go`）、`docs/workflow-schema.md`。真偽値だけを受け付ける（`boolValue`、`engine/yamlutil.go`）。エンジンは写すだけで、実行・検査には使わない。同梱の`fix/build-step`・`implement/build-step`・`review/cross-cutting`・`review/perspectives`・`smoke`はfalse |
| #11 | `20fdcb8` | 同梱の`fix`で、plan gateの`rejected`を`replan`（role・continuesとも`agents/quick-planner`、`inputs: [plan, investigation]`、`max: 3`、`exhausted`は`approve-plan`）へ。`quick-planner.md`に「人間の却下理由」の節。サブエージェントが実装し、監督が確かめた |
| #3 | `ff0145e` | コミット前の計画外の変更の判定で、`Byproducts`を「完全一致、またはdoublestarのglob」で照合する（`matchesByproduct`、非公開）。依存`github.com/bmatcuk/doublestar/v4`を足した（ユーザー承認）。`api.go`の`CommitRequest.Byproducts`の注記に規則を書いた。ホスト（masudaの`staging.matchPath`、masuda#66）も同じ規則にそろえた |
| #9 | `042dcd5` | fixerの出力に`commit-message`を足し、レビュー後の修正コミットのメッセージを書かせる（`commitMessage()`が空を返し、計画のsummaryに落ちていた）。fix→recheck→fixと回るときは前回までの分も含めて書き直させる |

**Issueの棚卸し（2026-10-09、ユーザー判断）**: 閉じたもの #8・#10・#6・#4・#1・#2（#2は既知の制約として。privilegedノードの強制を決めるときに見直す）。#5は依頼の粒度だけのIssueに書き直した。マイルストーンは`v0.4.0`・`v0.4.1`・`v0.5`を作り、`v0.2`は閉じた。今開いているのは#7（v0.5、masuda#67と一緒に）と#5（v0.5）。
## 完了した契約テスト
- `go build ./... && go vet ./... && go test ./...`が緑（`042dcd5`、契約テストを含む）
- #93: `TestUserInvocableは省略時trueで書いた値を写す`、`TestBundledのuser_invocableは利用者が始めるものだけtrue`（同梱を足したら表にも足さないと落ちる）、`TestLoadRejectionReasons`の「user_invocableが真偽値でない」
- #11: fixの歩行テストを`Test同梱のfixは計画の却下を前回の計画の続きで直しpublishする`に広げた
- #3: `TestMatchesByproductは完全一致とdoublestarのglobで照合する`（masudaの`TestMatchPathは…`と同じ入力と期待値）と`TestCommitはexpected_byproductsのglobに当たる変更を計画外の変更にしない`（`e5EngineWithPlan`で計画を差し替える）。照合を完全一致に戻すと両方落ちる
- #9: developとfixの歩行テストの最後で、review-commitのメッセージがfixerのものであることを確かめる。fixerの出力から外すとsummary（"s"）に落ちて落ちる
- 壊して落ちることは、どの項目も確かめた。実機（VM）では未確認（下）
## 未完と理由
- v0.4.0のタグ: masudaのリリース（Skill `release`、週末）のときに、engineの`main`の先頭にも打つ
- 実機での確認（v0.4.0のリリース前の実機1周で兼ねる）: fixerが実際に`commit-message`を書くか
- `replan`の`exhausted`→`approve-plan`の経路はテストしていない（developの`revise-rejected`も同じ）
- 同梱のdevelopで特権コマンドを強制する方法（宣言があるときだけprivilegedノードを通す等）は決めていない
## 次の一手
1. masudaのv0.4.0のリリースで、engineの`main`の先頭にv0.4.0を打つ（masudaのgo.modをタグに上げる）
2. privilegedノードの強制の方法を、masuda側の利用の様子を見て決める（#2もそのとき見直す）
## 注意点
- 副産物の照合の規則を変えるときは、masudaの`internal/staging/commit.go`の`matchPath`と一緒に直す。片方だけ変えると、片方が通した副産物をもう片方が逸脱にする。両方のテストの表は同じ入力と期待値にしてある
- 同梱の役に出力を足すと、その出力は`done`で必須になる（書かないと差し戻し）。歩行テストのフェイクは出力を名前で一律に返すので、足しても既存の歩行テストは通る。足した出力が使われることは、使う側（commitなど）を見る検査を足して確かめる
- ワークフローのトップのキーを足すときは、`parseWorkflow`の`switch`（知らないキーは拒む）と`workflow-schema.md`の両方を直す
- 同梱のワークフローを足したら、`TestBundledのuser_invocableは利用者が始めるものだけtrue`の表に足す
- Runnerのインターフェースを変えると、masudaの`internal/runner`とフェイク（contract・engineのテスト）が追従する必要がある
- privilegedノードはmasudaの`advance()`が`c.mu`を握ったまま同期で呼ぶ。masuda側の実行の関数で`c.mu`を取ると止まる（masudaの契約テストC-M11が検出する）
- 依存は、jsonschema・yamlに加えてdoublestar（照合のため、ユーザー承認）。ネットワーク・git・プロセス起動のライブラリは入れない（CLAUDE.md）
- ワークフローの形への外部の指摘（ワークフローのoutputs・outcomesの宣言、revise系3ノードの統合、optionalなinput）は、masuda側で検討した上でユーザーが却下した
## 契約への提案
- なし
