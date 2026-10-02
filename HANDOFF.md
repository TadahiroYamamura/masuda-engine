# HANDOFF
## 作業項目
E3（エンジンの中核）完了。`New`・`Start`・`Advance`（agent・approval・workflow・discard・end）・`Status`・`ReportResult`・`Decide`
## 完了した契約テスト
C-E1・C-E2・C-E3（`go test -count=1 ./contract/ -run 'TestCE1|TestCE2|TestCE3'` 緑、`go vet ./...` 指摘なし）。C-E4〜C-E7は想定どおり失敗する（exec・foreachなどに入ると`ErrNotImplemented`を返す。`Answer`・`ReportConcern`も`ErrNotImplemented`のまま。C-E7は同梱の`workflows/develop`が無いため）
## 未完と理由
なし（E3の範囲内）。E4以降は範囲外のため未着手
## 次の一手
E4（exec・question・方針・スナップショット）。`docs/work-orders.md`のE4を読む
## 注意点
- 実装の場所: `engine/records.go`（Storeのキーと記録の型、読み込み）、`engine/run.go`（歩行・進入・遷移・各ノード・ReportResult/Decide）、`engine/validate.go`（出力のスキーマ検証）。`api.go`は関数本体だけ書き換えた。内部テスト`engine/run_test.go`（workflowノード越しの差し戻し・ゲートが1回だけ開くこと・Statusの振る舞い）
- Storeのキー（すべて`<run>/`の下）: `start`（root・入力名）、`occ/<id>`（7桁ゼロ詰め、既存の最大+1）、`result/<id>`、`frame/<fid>`（rootは`root`、呼び出し先は呼び出し元の出現ID）、`frame-end/<fid>`（outcome文字列）、`gate/<occ>`（開いたGateRequest）、`blocked`（理由文字列）。`question/<occ>`・`concern/<occ>`は未使用（E4/E6で使う）。書き込みはすべて「キーが無いこと」をOpCheckするApplyで行い、負けたら読み直す
- 位置の再計算: Advanceのたびに`blocked`→`frame-end/root`→occ/resultを全部読み、rootフレームの最後の出現から辿る。結果があれば遷移、`Exhausted`なら`exhausted`で終える、workflowノードなら子フレームへ降りる。`walk(mutate)`の1関数で、mutate=falseのときに書き込みが必要になると`errPending`を返す。`Status`・`ReportResult`・`Decide`はこの見るだけの歩行で「今待っている出現」を求めて照合する。よって**ReportResult/Decideの直後、Advance前のStatusはエラー**（`errPending`）
- agentの`Inputs`（フレームの入力＋ノードの`inputs`＋エージェントの`inputs`）・`Outputs`（ノード＋エージェントの和）・`Policy`は進入時に出現へ固定する。SetPolicyは進入時に1回（exhaustedの進入では呼ばない）。execのSetPolicyはE4で実行直前に呼ぶこと
- データの解決順（`mover.resolve`）: フレームの束縛済み入力 → run内で最後に受け付けた出力（resultの`Outputs`から出現ID順で求める）→ `diff`・`step-diff`は`Runner.Diff`でその出現の値として計算 → runの入力（Occurrence ""）。`fix-diff`は`ErrNotImplemented`（E5でforeach over findingsのスナップショットから計算する）
- 進入回数: 同じフレームの出現を順に見て、そのノードの非exhausted出現を数え、判断済み（resultあり）のapprovalが出たら0に戻す。deviation・triageの判断で戻すかはE5/E6で決める（`enter`の中）
- 差し戻し: resultの`Feedback`が次の出現の`Feedback`になる。次がworkflowなら子フレームの`Feedback`に入り、その最初のノードが受け取る。foreach（E5）も同じく子フレームの`Feedback`に入れること
- approval: 開いたときにTargetHash（内容のsha256 hex）とSubject（内容そのもの）を`gate/<occ>`に保存し、OpenGateはその1回だけ。`target: diff`は`Runner.Diff(DiffFromBase)`をその出現の値として計算して使う。Decideは`approved`/`rejected`だけ受け付け、他（dismiss・halt・redo）は`ErrNotImplemented`（E5/E6）。承認のハッシュ不一致はエラーで、記録しない
- ReportResult: 宣言外outcomeはエラー（記録せず、`invalid`イベントだけ出す）。`done`で出力の欠落・検証失敗は`Invalid`な結果として記録し、日本語の理由を差し戻しにして同じノードへ再進入（回数に数える）。受け付けた出力は`PutData(DataRef{name, occ})`。書き込めないエージェントのdeviation検査（C-E5の2つめ）はE5でここかagent待機中に挟むこと
- イベント: start・enter・policy・finish・end・gate-open・decision・invalid・blocked
- ヒューズ: 進入時に出現数が`Fuse`以上なら`blocked`
## 契約への提案
ブロッキングではないもの:
1. `Status`が「Advanceが必要な状態」（結果報告直後など）を表す手段が契約に無い。現在は非公開エラーを返している。ホストが区別したいなら`StatusKind`に`pending`のような値を足すか、公開エラー変数を足す案がある
