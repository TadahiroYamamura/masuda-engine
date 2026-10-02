# HANDOFF
## 作業項目
E4（exec・question・方針・スナップショット）完了。E3までの`New`・`Start`・`Advance`・`Status`・`ReportResult`・`Decide`に加え、execノード・固定questionsのquestionノード・`Answer`・ノード境界のスナップショット・`StatusPending`
## 完了した契約テスト
C-E1・C-E2・C-E3・C-E4（`go test -count=1 ./contract/ -run 'TestCE1|TestCE2|TestCE3|TestCE4'` 緑、`go vet ./...` 指摘なし）。C-E5〜C-E7は想定どおり失敗する（foreach・deviation・`ReportConcern`が`ErrNotImplemented`、C-E7は同梱の`workflows/develop`が無いため）
## 未完と理由
- role付きquestionは「エージェントタスクを出す」まで（E4の指示どおり）。そのoccurrenceへの`ReportResult`は`ErrNotImplemented`、タスク実行中の`Answer`は「questionを待っていない」エラーになる。下の未決事項を決めてから実装する
## 次の一手
E5（foreach・commit・publish・discard・逸脱）。`docs/work-orders.md`のE5を読む
## 注意点
- 実装の場所: `engine/exec.go`（execノード）、`engine/question.go`（固定question・`Answer`）、`engine/run.go`（歩行・進入・遷移・agent/approval/workflow/discard・ReportResult/Decide・`snapshot`/`setPolicy`/`nodeOf`/`status`ヘルパー）、`engine/records.go`（Storeのキーと記録の型）、`engine/validate.go`。内部テスト`engine/run_test.go`・`engine/exec_test.go`
- Storeのキー（`<run>/`の下）: E3の`start`・`occ/<id>`・`result/<id>`・`frame/<fid>`・`frame-end/<fid>`・`gate/<occ>`・`blocked`に、`question/<occ>`（開いたQuestionRequest。OpenQuestionはこれを書けたときの1回だけ）を追加。`concern/<occ>`は未使用（E6）
- スナップショット: agent/execの出現が終わるとき（agentは`ReportResult`で記録する直前、Invalidな報告も含む。execは結果記録の直前、failed・Invalidも含む）に`Runner.Snapshot(occ)`を呼び、`result.Snapshot`に残す（occレコードは進入時の不変記録なのでresult側に置いた）。exhaustedの出現・approval・question・workflow・discardでは取らない。E5の`fix-diff`・deviationの`ChangedSince(from)`は「直前の境界のスナップショット」をresultから引けばよい（例: agent進入時点の基準＝同じrunで直前に終わったagent/execのresult.Snapshot。run開始直後は空なので、そのときの基準をどうするかはE5で決める）
- exec: Advanceの中で同期実行。順序は入力解決（フレーム入力＋ノードの`inputs`、`resolve`経由）→`SetPolicy`（実行直前に毎回）→`RunCommand`→判定→`Snapshot`→result記録。exit非0またはTimedOutは`failed`（差し戻し＝「コマンドが失敗した（exit N）:\n」＋LogTail）。exit 0で出力の欠落・検証失敗は`Invalid`な結果として同じノードへ再進入（回数に数える）。受け付けた出力は`PutData(DataRef{name, occ})`。結果は実行後にしか書かないので、途中で落ちると次のAdvanceで再実行され、並行Advanceでは二重実行し得る（記録は先着1件）
- question（固定）: 初回の歩行で`question/<occ>`を作って`OpenQuestion`、以後は保存済みのリクエストで`StatusQuestion`を返す。`Answer`は全質問への回答・選択肢との一致・未質問idの不在を確かめ、`outputs[0]`のスキーマで検証してから`PutData(DataRef{outputs[0], occ})`（JSON、id→answer）し、`answered`で記録。不正な回答は記録せずエラー（答え直せる）
- question（role付き）: 進入時に`prepareAgent`を通る（`n.Role != ""`で判定。SetPolicyも呼ぶ）。タスクの`Outputs`にはノードの`outputs`（答え）を含めずエージェントの`outputs`だけにした（答えはエンジンが`Answer`から保存する想定のため）
- `Status`: 見るだけの歩行が書き込みを必要とした（`errPending`）とき`Status{Kind: StatusPending}`を返す。Start直後でAdvance前も同じくPending。`Occurrence`は空。`ReportResult`/`Decide`/`Answer`内部の`waiting`は従来どおり`walk(false)`を直接使う
- イベント: E3の分に加え policy（execでも）・snapshot・question-open・answer
- E3からの注意点（データ解決順・進入回数のリセット・差し戻しの受け渡し・approval・ヒューズ）は変わっていない。`fix-diff`は`resolve`で`ErrNotImplemented`のまま

E5へ引き継ぐ未決事項:
1. role付きquestionの完了の形: エージェントの`done`を`answered`に読み替えるか。実行中の`Answer`（ホストが`ask_human`を仲介し、質問idはエージェントが決める）を`question/<occ>`相当に蓄積して`ReportResult`時に`outputs[0]`へ保存するのか、エージェント自身が答えを出力として書くのか。`Answer`がStatusAgentの出現を受け付けるようにする必要がある
2. スナップショットの基準: deviation検査（C-E5の2つめ）で`ChangedSince`に渡す「エージェント開始時点」をどこから取るか（直前の境界のresult.Snapshotか、進入時に別途Snapshotを取るか）。現在は終了時にしか取っていない
3. 進入回数のリセットをdeviation・triageの判断でも行うか（E3から継続）
## 契約への提案
なし（E3の提案1はStatusPendingとして契約に入り、実装済み）
