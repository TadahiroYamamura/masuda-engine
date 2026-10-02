# HANDOFF
## 作業項目
E5（foreach・commit・publish・discard・逸脱）完了。foreachノード、commitノードとdeviationゲート、書き込めないエージェントの前後比較、publishノード、deviationの`Decide`（`ApprovedFiles`）、`fix-diff`の解決
## 完了した契約テスト
C-E1〜C-E5（`go test -count=1 ./contract/ -run 'TestCE1|TestCE2|TestCE3|TestCE4|TestCE5'` 緑、`go vet ./...` 指摘なし）。C-E6（`ReportConcern`が`ErrNotImplemented`）・C-E7（同梱`workflows/develop`が無い）は想定どおり失敗
## 未完と理由
- role付きquestionの終わり方（契約`docs/workflow-schema.md`で決定済み: `ask_human`の答えを出現に溜め、`done`で`outputs[0]`に保存して`answered`）は未実装。E5の指示の実装項目に含まれていなかったため手を付けていない。`ReportResult`はquestionノードに`ErrNotImplemented`を返したまま。C-E7の`develop`で必要になるならE7の前に入れる
- publishの`Commit`は「直近の`Runner.Commit`が返したハッシュ」で代用（E5の指示どおり）。reviewゲート（`target: diff`）承認時点のstagingのコミットを記録する形にはなっていない
## 次の一手
E6（triage・ログ）。`docs/work-orders.md`のE6を読む
## 注意点
- 実装の場所: `engine/foreach.go`（`prepareItems`・`foreach`・`iterationTree`・`stepFrame`）、`engine/commit.go`（`prepareBase`・`commit`・`openDeviation`・`readOnlyDeviation`・`decideDeviation`・`publish`）。`run.go`は`step`の分岐・`enter`での基準/項目の固定・`resolve`の`fix-diff`・`bindInputs`（callとforeachで共用）・`finishAgent`・`decide`の振り分けを変えた。内部テスト`engine/foreach_test.go`
- Storeのキー追加: `deviation/<occ>/<nnn>`（`devGate`: GateRequestとファイル一覧）、`decision/<occ>/<nnn>`（Decision、createで1回だけ）。`loadRecords`が`recs.devs[occ]`に読み込む。frameに`Parent`・`Over`・`Item`・`ItemRef`・`Tree`、occurrenceに`Base`・`BaseHash`・`Items`、resultに`Deviation`・`DeviationHash`・`Commit`を追加
- foreach: 項目は進入時に出現へ固定。フレームID`<occ>.<n>`はDoneも含めた1始まりの番号。項目は`PutData(DataRef{Item.Input, fid})`して`frame.Inputs[Item.Input]`に束縛（`Input`が空なら`itemInput(over)`）。差し戻しは最初に作るフレームの`Feedback`へ。`perspectives(from=<node>)`はフレーム内でそのノードが最後に`selected-perspectives`を出した出現を`from`にする
- 基準スナップショット: agent/execは進入時に「同じフレームで直前に終わった出現の`result.Snapshot`」、無ければ進入時に`Snapshot(occ)`。commitの基準は「run全体で最後に成功したcommit以後、最初のagent/execの`Base`」。契約の「直前の境界」を文字通りcommitに当てると、実装エージェントの終わり（変更後）になり実機では常に空になるため、こう解釈した
- 書き込めないエージェント: 進入時に`ChangedSince(Base)`のハッシュを`BaseHash`に残し、報告時（Invalidでも）にもう一度取ってハッシュが違い一覧が空でなければ`result.Deviation`。監督の指示は「終了時の`ChangedSince`が空でなければ」だったが、契約テストのstubは`from`を無視して同じ一覧を返すため、そのままだとC-E5の1つめでreviewer（Read, Grep）が誤ってdeviationゲートを開く。契約の文言「前後比較」に沿う比較にした（実機では進入時は空なので同じ結果になる）。却下はblocked、承認でoutcomeどおり遷移
- commit: 最後のdeviationゲートが未決なら待つ、却下なら`rejected`（差し戻し＝却下ファイル＋コメント）。ゲートがまだ無いときだけ`ChangedSince`で計画外を見る。承認後は見直さずに`Runner.Commit`する（人間が見ていない作業ツリーについて聞き直さないため。Allowed外はホストが`Deviations`で返し、次のゲートを開く。C-E5もステップ1承認後に`changed`をステップ2の内容に差し替えてからAdvanceするので、見直すと誤ってゲートが開く）。`Allowed`は`scope: step`ならそのステップの`files`（`stepFrame`で親をたどり`ItemRef`を読む）、`plan`なら全ステップの`files`、に加えてrun全体で承認された`ApprovedFiles`（空の承認はゲートのファイル全部を承認したとみなす）。`Message`は最後のcommit以後に書かれた`commit-message`、無ければステップの`description`（planスコープは`summary`）
- 「同じフレーム系列でdeviationが承認したファイル」はrun全体の累積で実装した。C-E5でステップ1で承認した`c.go`をステップ2（兄弟フレーム）で許す必要があり、runのフレームはすべてrootの子孫なので同じになる
- `Decide`: ゲートが`deviation`なら`decideDeviation`（approvedはハッシュ一致と`ApprovedFiles`⊆ゲートのファイルを確認）。`dismiss`/`halt`/`redo`は`ErrNotImplemented`のまま（E6）
- 進入回数の数え直し: `records.decided`（approvalのresult、またはその出現が開いたdeviationゲートの判断）で数え直す。questionの`Answer`は数え直しに含めていない。E6のtriageもここに足す
- E4からの注意点（exec・固定question・StatusPending・データ解決順）は変わっていない

E6へ引き継ぐ未決事項:
1. triageの判断を`decided`に足す方法（triageゲートを出現にどう紐付けるか。deviationと同じく`<occ>/<n>`キーで持つと`decided`をそのまま使える）
2. role付きquestionの実装をどの作業項目で行うか（上の「未完と理由」）
3. publishの`Commit`をreviewゲート承認時点のコミットに結びつけるか（現状は直近のコミット）
## 契約への提案
なし
