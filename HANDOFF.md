# HANDOFF
## 作業項目
同梱のレビュー工程を「観点ごとにセッションを起動する」形から「役割ごとに1セッションで全観点・全指摘を回す」形に組み替えた（quickstart実走で観点レビューが9分16秒かかったことへの対処）。engineの実行ロジックは変えず、同梱の定義だけを変えた。

- `workflows/review/perspectives`: `review`（reviewer、max 3）→`check-review`（review-checker、`inaccurate`で`review`へ）。`clean`（指摘0件）は`end:clean`、reviewerの`exhausted`は`end`
- `workflows/implement/interim-review`（新規）: 同じ構造でノード名が`interim-review`・`interim-check`。build-stepから`with: {diff: step-diff}`で呼ぶ
- `workflows/develop`: `review`→`cross-cutting`→`fix`（fixer、max 3）→`recheck`（rechecker、`unresolved`で`fix`へ）→`review-commit`→`report`→… fixerの`cannot_fix`・`exhausted`とrecheckerの`exhausted`は`review-commit`へ
- `workflows/implement/build-step`: `implement`→`test`→`review`（interim-review）→`fix`→`recheck`→`commit`。`review`の`clean`は`commit`へ直結。fixerの`cannot_fix`・`exhausted`とrecheckerの`exhausted`は`approve-interim`へ
- `workflows/review`（review-only）: `review`（perspectives）→`cross-cutting`→`report`→`cleanup`。fix・recheckは置いていない（承認済み計画が無いので書き込めるエージェントに到達できない）
- エージェント: reviewer（入力`[diff]`、全観点を順に当てる、ノード名`interim-`で途中レビューと判別し`trigger`で観点を飛ばす、`clean`を追加）、review-checker（入力`[diff, findings]`、直前の出現の指摘を全観点まとめて検証、`clean`を追加）、fixer（入力`[findings]`、一括修正、直せない指摘を`id`付きで列挙して`cannot_fix`）、rechecker（入力`[findings, step-diff]`、未解決を`id`付きで列挙して`unresolved`）
- 削除: `workflows/review/perspective-review`、`workflows/fix-finding`、`agents/trigger-matcher`
## 完了した契約テスト
C-E1〜C-E7すべて緑（`go test -count=1 ./...`）。`go vet ./...`指摘なし。`engine/bundled_test.go`に、同梱のdevelopを2ステップで歩かせ、途中レビューの`clean`直結・fix→recheck経路・最終レビューの差し戻し・fixerの`cannot_fix`からreportへの経路と、途中レビューのreviewerが`diff`として`step-diff`を受け取ることを確かめるテストを足した
## 未完と理由
- `schemas/selected-perspectives.json`は削除しなかった。`docs/workflow-schema.md`（契約）が同梱スキーマとして列挙しており、`perspectives(from=<node>)`がそのデータを前提にしているため
## 次の一手
- masuda側で`go.mod`のengineをこの版に上げる
- 実機1周（quickstart）で、レビュー工程の所要時間と、1セッションで全観点を回したときの指摘の質（観点の取りこぼし、`id`の形）を確かめる
## 注意点
- masuda側で追随が要るもの:
  - `docs/user/workflows.md`のワークフロー図（観点ごとのforeach・trigger-matcher・fix-findingの記述を、reviewer→checker→fixer→recheckerに）
  - quickstartの工程説明（観点ごとのペアが14個並ぶ前提の説明があれば直す）
  - `go.mod`のengineの版上げ（`go get github.com/TadahiroYamamura/masuda-engine@<tagまたはmain> && go mod tidy`）
- reviewerはタスクの「実行位置」のノード名（`interim-`で始まるか）で途中レビューかを判別する。masudaの`internal/runner/task.go`が「実行位置: ワークフロー…のノード…」を出していることに依存する
- reviewerの`clean`はcheckerを経ずに終わる（指摘0件の典型経路を1セッションで抜けるため）。見落とし検査が要るなら`clean: check-review`に変えればよい
- developのfix・recheckは、指摘0件でも1回ずつ起動する（cross-cuttingは変更しない指示だったため）。飛ばすならfixerに「対象なし」の終わり方を足すか、cross-cuttingに`clean`相当の終わり方を足す
- fixer・recheckerの対象は「`autofix: true`で現在のコードに問題が残っているもの」。台帳に前のステップで直した指摘も残るため、現在のコードを読んで判断させている
- `max`はフレームの直前の人間の判断から数えるので、developの`fix`・`recheck`はreworkの周回ごとに数え直される
- contractのC-E7は`*/selected-perspectives`の出力を用意しているが、もう使われない（無害なので触っていない）
- `docs/work-orders.md`の旧記述（trigger-matcher・fix-finding）は経緯としてそのまま
## 契約への提案
- `docs/workflow-schema.md`の同梱スキーマの列挙から`selected-perspectives`を外すかどうか。外すなら`schemas/selected-perspectives.json`を削除でき、`perspectives(from=<node>)`を使うユーザー定義のワークフローは自前のスキーマを置く（無ければ空でないことだけ検証される）。残すなら現状のまま
