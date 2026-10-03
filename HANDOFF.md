# HANDOFF
## 作業項目
E14（engine #10、masuda #68の計測）: 同梱`develop`の途中レビューを外し、ゲートの却下を直前の役へ戻した。契約（`engine/api.go`・`docs/workflow-schema.md`）は変えていない。

- `a77af38` `workflows/implement/build-step`を`implement`→`test`→`commit`に（`workflows/fix/build-step`と同一内容）。`workflows/implement/interim-review`を削除。ゲート名`interim`は予約のまま
- `e0bd292` `approve-plan.rejected`を新ノード`revise-rejected`（`agents/plan-reviser`、`max: 3`、遷移は`revise-answered`と同じ）へ。`plan-reviser.md`に「## 人間の却下理由」の節を追加
- `1c281ae` `rework`に`continues: agents/implementer`、`rework-commit.done`を`approve-review`へ（最終レビューの段は通さない）。`review-commit.rejected: rework`も結果として`approve-review`へ戻る
- テスト（`engine/bundled_test.go`）:
  - `TestBundledDevelopReviewsInOneSessionPerRole`を`Test同梱のdevelopは役ごとに1セッションで進み却下を直前の役へ戻す`に改名・改修。3ステップがimplement→test→commit、planゲート却下→`revise-rejected`（feedbackに却下理由、直前のreviseの`plan`とquestionsの`plan-checklist`を読む）、reviewゲート却下→`rework`（最後のimplementerの続き、feedbackに却下理由）→`approve-review`。コミットは step×3・plan×2
  - `TestBundledBuildStepDisputeEndsAtInterimGate`（build-stepのfix/recheckで反論の台帳を確かめていた）を、同じ仕組みが残る最終レビューの`fix`→`recheck`へ移し`Test同梱のdevelopの最終の修正で反論が続くと3回でレポートへ進む`に改名
  - `checkContinues`はdevelopの`rework`も直前のimplementerに続くことを確かめる。同梱から消えたワークフローの列挙に`workflows/implement/interim-review`を追加
## 完了した契約テスト
C-E1〜C-E9は無修正で緑（`go build ./... && go vet ./... && go test -count=1 ./...`）。
定義をわざと壊して歩行テストが落ちることを確かめて戻した: `approve-plan.rejected`を`plan`に戻す／`revise-rejected`の役を`planner`にする／`rework-commit.done`を`review`に戻す／`rework`の`continues`を外す／`rework-test.done`を`approve-review`にしてコミットを飛ばす、の5通り。
## 未完と理由
- なし
## 次の一手
- masuda側: engineの版上げと、`docs/user/workflows.md`の同梱ワークフローの説明（途中レビュー・却下の戻り先）を直す
- 下の「注意点」の残った記述をどうするか監督が決める
## 注意点
- 指示書の範囲外として次の記述を残した（途中レビューを前提にした文で、同梱では当たらなくなった）
  - `engine/defaults/workflows/fix.yaml`冒頭コメント「developとの違い: ステップごとの途中レビューとinterimゲートを置かない」（`fix`は変えない指示のため）。今はdevelopとの違いではなくなった
  - `agents/reviewer.md`・`review-checker.md`（ノード名が`interim-`で始まるときの途中レビュー）、`rechecker.md`（「ステップの途中レビューの後では」）、`synthesizer.md`（「ステップごとの途中レビュー」）。利用者の定義が`interim-`のノードを使えば今も当たる
- `docs/workflow-schema.md`の`step-diff`の説明にある「interim gateのように」はゲート名の例なので残した
- `../masuda`は変更していない。pushしていない
## 契約への提案
- なし
