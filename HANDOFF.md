# HANDOFF
## 作業項目
E7（同梱ワークフローとエージェントの移植）。定義は全部置いたが、C-E7は`findings`の用意済み検査で赤のまま止めた（「契約への提案」参照）
## 完了した契約テスト
C-E1〜C-E6（緑）。C-E7は赤: `Check(workflows/develop)`と`Check(workflows/review)`が`findings`の用意済みで落ちる。`go vet ./...`指摘なし、`go test ./engine/`緑
## 未完と理由
- C-E7。`findings`を書くのはforeachの本体の中（reviewer）と条件付きの分岐（cross-cutting-verifier。explorerが`none_found`なら走らない）だけで、Checkはforeachの本体が書いたものを「その後どの経路でも用意済み」とは見ない（0回のことがある）。そのため次の4か所で「findingsが用意済みでない」になる
  - `workflows/develop`の`fix`（`over: findings`）と`report`（synthesizerの入力`findings`）
  - `workflows/implement/build-step`の`interim-fix`（`over: findings`）
  - `workflows/review`の`report`
- 実行時も同じで、C-E7のスタブ（観点0件・explorerは`none_found`）では`findings`が一度も書かれず、`fix`の`resolve`が「data "findings" is not available」で止まる
- 11エージェントだけで契約内に収める方法は無い（C-E7の経路でpublishまでに`findings`を書きうるのはreviewerとverifierだけで、どちらもその経路では走らない）。synthesizerに`findings`を読ませないか、`findings`を書くだけのノードを足すか、契約を変えるかのどれかで、いずれも指示された範囲（11本の移植・旧どおりの構成）を外れるので、判断を仰ぐために止めた
- 確認済み: 試しにdevelopの先頭に「`findings`を書くだけのエージェント」を置いて（コミットしていない）C-E7と同じ手順で歩かせると、seed → investigator → planner → plan gate → implementer → `/masuda/checks/test` → trigger-matcher → step commit → cross-cutting-explorer(none_found) → review-commit → synthesizer → review gate（diff） → publish 1回、で完了した。つまり残りの構成（plan gate、step commit、承認済みコミットのpublish、questionが無いこと）は問題ない
## 次の一手
「契約への提案」の案を監督が選んだら、それに合わせて`workflows/develop`・`workflows/review`・`workflows/implement/build-step`（必要ならエンジンと`docs/workflow-schema.md`）を直し、`go test -count=1 ./contract/`を全部緑にする
## 注意点
- 置いたもの（`engine/defaults/`）
  - `workflows/develop`: investigate(investigator) ⇄ plan(planner, max 4) → approve-plan(gate plan, target plan) → implement(foreach steps → `workflows/implement/build-step`, stuck → approve-plan) → review(workflow `review/perspectives`) → cross-cutting(workflow `review/cross-cutting`) → fix(foreach findings → `fix-finding`, continue) → review-commit(commit plan) → report(synthesizer) → approve-review(gate review, target diff) → publish(export report)。却下・commit拒否はrework(implementer) → rework-test(exec `/masuda/checks/test`) → rework-commit(commit plan) → reviewへ戻る（旧どおり）。triageの行き先は書いていない
  - `workflows/review`: review → cross-cutting → report → discard(export report, findings)
  - `workflows/implement/build-step`: implement(max 3) → test(exec) → pick-perspectives(trigger-matcher) → interim-find(foreach `perspectives(from=pick-perspectives)`, with diff: step-diff) → interim-fix(foreach findings) → commit(step)。incompleteはapprove-interim(gate interim, target diff)へ
  - `workflows/review/perspectives`・`perspective-review`・`cross-cutting`・`workflows/fix-finding`: 旧と同じ形
  - 旧`investigate/plan/implement/review/default`の包みは無くした
- エージェントの`inputs`はfrontmatterに明示した（旧版は本文で触れるだけ）。investigator `[instructions]`、planner `[instructions, investigation]`、implementer `[plan]`（buildでは`step`もフレームから入る）、trigger-matcher `[step-diff]`、reviewer `[perspective, diff]`、review-checker `[perspective, diff, findings]`、explorer `[diff]`、verifier `[cross-cutting-candidates]`、fixer `[finding]`、rechecker `[finding, fix-diff]`、synthesizer `[findings, diff]`
- 旧reviewerの`resume: true`はスキーマに無いキーなので落とした。旧synthesizerが前提にしていた指摘の`status`（resolved/unresolved/open）は新しい`findings`スキーマに無いので、「autofixの指摘は差分と現在のコードを見て解消済みか確かめる」と書き換えた
- 内部テスト`TestCheckAcceptsRunnableWorkflows`の「同梱ワークフローを全部rootとしてCheck」は、部品を単独で検査しない契約と食い違うので削った（rootの検査はC-E7が持つ）
- masuda側（M5〜M7）への取り決め
  - `/masuda/checks/test`: develop・build-stepのテスト実行は`exec`の`["/masuda/checks/test"]`。cwd `/workspace`で動き、0で通過。`docs/work-orders.md`のE7には「`/masuda/in/<occ>/run-check`」とあるが、監督の指示どおり`/masuda/checks/<名前>`に従った
  - レビュー観点: `over: perspectives`の項目は`.masuda/reviews/*.md`（ファイル名が観点名、本文が定義）をmasudaが`Items`で返す前提。`perspectives(from=...)`は`selected-perspectives`（観点名の配列）で絞る。trigger-matcherは`/workspace/.masuda/reviews/*.md`のfrontmatter `trigger`を直接読む
  - 特権コマンド: implementerのプロンプトは`run_privileged_command(name)`と`masuda privileged-command approve <name>`を案内している
  - `over: steps`の`Item.Done`: stuck → approve-plan → implementで戻ったとき、コミット済みのステップは`Done`で返してもらう前提（旧どおり）
  - `findings`の集約（下の提案2）は、契約で決まらなければmasudaの`Items(findings)`とデータの実体化で肩代わりすることになる
## 契約への提案
1. **`findings`の用意済み（C-E7を赤にしている原因）**。次のどれかを選んでほしい
   - 案A（契約変更・推奨）: foreachに「本体が書いたJSON配列を集めて、foreachの後にそのデータ名で1つにする」キーを足す（例: `collect: [findings]`。0回なら`[]`）。Checkはforeachの後でそのデータを用意済みと見る。下の2も同時に解ける
   - 案B（契約変更）: エンジンが用意するデータに`findings`を加え、run中に書かれた`findings`の和（無ければ`[]`）にする
   - 案C（契約内・同梱だけ）: developとreviewの先頭に「`findings`に`[]`を書くだけ」のノードを置く。execは出力を書けるがC-E7のスタブの`RunCommand`は出力を返さないので、エージェント（12本目）になる。LLMを1回空回しする上、2の問題は残る
   - 案D（契約内・同梱だけ）: synthesizerの入力を`diff`だけにし、指摘はmasudaが別の経路（例: `/masuda/findings.json`）で見せる。データの外の取り決めが増える
2. **`findings`が後勝ち**。データは名前ごとに最後の値が見えるので、観点が2つ以上あると、`fix`（`over: findings`）とsynthesizerは最後に書いた観点（またはverifier）の指摘しか見ない。途中レビューの指摘も最終レビューの指摘で見えなくなる。旧エンジンは指摘をストアに貯めてstatusを持たせていた。案Aか案Bで集約を契約にするか、masudaの`Items(findings)`・実体化で貯める取り決めにするかを決めてほしい
3. `docs/work-orders.md`のE7はテスト実行を「`/masuda/in/<occ>/`に展開した`run-check`」としているが、指示は`/masuda/checks/<名前>`。作業指示の文面を直すとよい（契約ファイルではないが監督の持ち物なので触っていない）
