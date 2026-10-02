# HANDOFF
## 作業項目
E10（`target: diff`の承認対象・triageで中断されたゲートの後始末・feedbackのログ）と、その仕上げ（契約eca2e00: `ChangedSince("")`＝ブランチ先頭を、commitの計画外変更の検出と承認の未コミット一覧に使う）。完了
## 完了した契約テスト
C-E1〜C-E7すべて緑（`go test -count=1 ./contract/`）。`go vet ./...`指摘なし、`go test -count=1 ./...`緑
## 未完と理由
なし
## 次の一手
- masuda側（M12）で下の取り決めを実装し、サンドボックスイメージを再ビルドしてから実機1周で、review gateのSubjectと、triage後にゲート一覧から古いゲートが消えることを確かめる
## 注意点
- 実装の置き場所
  - `engine/run.go`の`approval`: `target: diff`は`Runner.Diff(DiffCommitted, "", DataRef{Name: "committed-diff", Occurrence: ゲートの出現})`。`TargetHash`はこの差分だけのsha256。`withUnpublished`が`ChangedSince(ctx, run, "")`（ブランチ先頭＝未コミットの変更すべて）の一覧を`## publishされない変更（未コミット）`の見出しの下に1行1ファイルで足す（一覧が空なら見出しごと省く）。エージェントが読む`diff`データ（`DiffFromBase`）は従来どおり`resolve`が作る
  - `engine/commit.go`の`commit`: 計画外変更の検出も`ChangedSince(ctx, run, "")`。commitの出現はもう基準スナップショットを取らない（`workStart`は削除）。書き込めないエージェントの前後比較は従来どおりスナップショット基準
  - 基準をブランチ先頭にした帰結: 以前のcommitのdeviationゲートで人間が加えなかった（承認したが`ApprovedFiles`に入れなかった）ファイルは作業ツリーに残り、後のcommitでも見える。聞き直さないよう、`Byproducts`に入れる範囲を「このcommitのゲート」から「runの全deviationゲート」に広げた。`expected_byproducts`も同じ理由で毎回`Byproducts`に入る（従来どおり）
  - 既知の制限: 加えなかったファイルをその後エージェントがさらに書き換えても、byproductのまま聞き直さない（ブランチ先頭基準ではいつ変わったかを区別できない）。却下（rejected）されたdeviationのファイルが作業ツリーに残っていれば、次のcommitで再び聞く
  - `engine/triage.go`の`supersede`: 中断された出現の開いているゲートを閉じる。呼ぶのは`decideTriage`（triageの判断を記録した直後）と`step`の入り直し（`reenter`）の2箇所。記録は`create`（無いときだけ書く）なので重複しない
  - `engine/run.go`の`putResult`/`record`は`detail`を取る。`reportResult`だけが`feedbackDetail(feedback)`（先頭200文字、超えたら`…`）を渡す。invalidになった報告でもエージェントが送ったfeedbackを載せる
- **開いているゲートの判定方法**（engineの記録、キーはすべて`<run>/`の下）
  - approval: `gate/<occ>`があり、`result/<occ>`も`gate-closed/<occ>`も無い
  - deviation: `deviation/<occ>/<n>`があり、`decision/<occ>/<n>`が無い（`superseded`の判断もここに書かれる）
  - triage: `triage-gate/<occ>/<n>`があり、`triage/<occ>/<n>`が無い
  - 実行ログでは、`gate-open`の対は`decision`イベント。triageで無効になったゲートは`decision`（`outcome: "superseded"`、`occurrence`はゲートを開いた出現、`detail`は`<ゲート名>: triageで無効になった`）
- `superseded`で閉じないケース: dismissで中断された出現が既に結果を持つ（書き込めないエージェントのdeviationゲート待ちに懸念が来た）とき。runは入り直さずそのゲートを待ち続けるので、ゲートは開いたまま
- masuda側（M12）への取り決め
  - `Runner.ChangedSince`の`from`が空のときはブランチ先頭からの変更を返すこと（契約eca2e00）。確認済み: masudaの`internal/runner/runner.go`の`ChangedSince`は空のとき`staging.BranchRef(ws.Branch)`を基準に、`git add -A`で取り込んだ今の作業ツリー（未追跡ファイルを含む）との`diff-tree`を返す。`staging`の`Commit`がこのrefを進めるので、commit直後も「未コミットのものだけ」になる。変更は不要
  - `Runner.Diff`は`DiffCommitted`（`committed-diff`）を受けること。baseからブランチ先頭（`staging.BranchRef`）までの差分で、作業ツリーを取り込まない。今の`internal/runner`は未知の種類としてエラーを返すので、review gateで止まる
  - masudaのゲート記録（`records/gates/`、`OpenGate`で書き`Decide`で埋める）は、`Runner.Log`に来る`kind: decision`・`outcome: superseded`のイベントで、その`occurrence`の最後のゲートを判断済み（outcome `superseded`）にすること。これをしないと`ListOpen`に古いゲートが残り、`Decide`は「not waiting」で失敗する
  - `gate show`等で`superseded`の判断を表示するなら「triageで無効」と出す
  - `finish`イベントの`detail`にエージェントのfeedback（先頭200文字）が入る
## 契約への提案
なし（E10で出した「未コミット一覧の基準をブランチ先頭に」は契約eca2e00で採用され、実装済み）
