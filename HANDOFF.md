# HANDOFF
## 作業項目
E10（`target: diff`の承認対象・triageで中断されたゲートの後始末・feedbackのログ）。完了
## 完了した契約テスト
C-E1〜C-E7すべて緑（`go test -count=1 ./contract/`）。`go vet ./...`指摘なし、`go test -count=1 ./...`緑
## 未完と理由
なし（ただし「publishされない変更」の一覧の基準に既知の不正確さがある。下の契約への提案を参照）
## 次の一手
- 監督が「契約への提案」を判断する（一覧の基準をブランチ先頭にするか）
- masuda側（M12）で下の取り決めを実装し、サンドボックスイメージを再ビルドしてから実機1周で、review gateのSubjectと、triage後にゲート一覧から古いゲートが消えることを確かめる
## 注意点
- 実装の置き場所
  - `engine/run.go`の`approval`: `target: diff`は`Runner.Diff(DiffCommitted, "", DataRef{Name: "committed-diff", Occurrence: ゲートの出現})`。`TargetHash`はこの差分だけのsha256。`withUnpublished`が`workStart()`からの`ChangedSince`の一覧を`## publishされない変更（未コミット）`の見出しの下に1行1ファイルで足す（一覧が空、または基準が無ければ見出しごと省く）。エージェントが読む`diff`データ（`DiffFromBase`）は従来どおり`resolve`が作る
  - `engine/triage.go`の`supersede`: 中断された出現の開いているゲートを閉じる。呼ぶのは`decideTriage`（triageの判断を記録した直後）と`step`の入り直し（`reenter`）の2箇所。記録は`create`（無いときだけ書く）なので重複しない
  - `engine/run.go`の`putResult`/`record`は`detail`を取る。`reportResult`だけが`feedbackDetail(feedback)`（先頭200文字、超えたら`…`）を渡す。invalidになった報告でもエージェントが送ったfeedbackを載せる
- **開いているゲートの判定方法**（engineの記録、キーはすべて`<run>/`の下）
  - approval: `gate/<occ>`があり、`result/<occ>`も`gate-closed/<occ>`も無い
  - deviation: `deviation/<occ>/<n>`があり、`decision/<occ>/<n>`が無い（`superseded`の判断もここに書かれる）
  - triage: `triage-gate/<occ>/<n>`があり、`triage/<occ>/<n>`が無い
  - 実行ログでは、`gate-open`の対は`decision`イベント。triageで無効になったゲートは`decision`（`outcome: "superseded"`、`occurrence`はゲートを開いた出現、`detail`は`<ゲート名>: triageで無効になった`）
- `superseded`で閉じないケース: dismissで中断された出現が既に結果を持つ（書き込めないエージェントのdeviationゲート待ちに懸念が来た）とき。runは入り直さずそのゲートを待ち続けるので、ゲートは開いたまま
- masuda側（M12）への取り決め
  - `Runner.Diff`は`DiffCommitted`（`committed-diff`）を受けること。baseからブランチ先頭（`staging.BranchRef`）までの差分で、作業ツリーを取り込まない。今の`internal/runner`は未知の種類としてエラーを返すので、review gateで止まる
  - masudaのゲート記録（`records/gates/`、`OpenGate`で書き`Decide`で埋める）は、`Runner.Log`に来る`kind: decision`・`outcome: superseded`のイベントで、その`occurrence`の最後のゲートを判断済み（outcome `superseded`）にすること。これをしないと`ListOpen`に古いゲートが残り、`Decide`は「not waiting」で失敗する
  - `gate show`等で`superseded`の判断を表示するなら「triageで無効」と出す
  - `finish`イベントの`detail`にエージェントのfeedback（先頭200文字）が入る
## 契約への提案
- **「publishされない変更」の一覧の基準**: 指示どおり`workStart()`（runで最後に成功したcommit以後の最初のagent/execの基準スナップショット）からの`ChangedSince`で取っているが、契約の文面（「作業ツリーに残る未コミットの変更（deviationで加えなかったもの等）」）を満たさない場合が2つある
  1. 基準がcommitより前のスナップショットになる: agent/execの基準はフレーム内の直前の境界を引き継ぐので、同じフレームの先行ノードの終了スナップショットになる。bundledの`develop`では`review-commit`の後の`report`（synthesizer）が`plan`の終了スナップショットを基準に持ち、コミット済みのファイルまで「publishされない変更」に並ぶ（CE5の`workflows/x`のreviewerも同様。スタブでは見えない）
  2. commitより前から残る未コミットの変更（deviationで加えなかったファイル、`expected_byproducts`）は、その後のスナップショットに含まれるので一覧に出ない
  - 案: 一覧は「ブランチ先頭から作業ツリーまでに変わったパス」とする。`Runner.ChangedSince`の`from`が空のときの意味を「ブランチ先頭」と`api.go`のコメントで定め（masudaの実装は既にそう動く）、engineは`ChangedSince(ctx, run, "")`を呼ぶ。シグネチャは変わらない。採用されればengine側は`withUnpublished`の基準を1行変えるだけ
