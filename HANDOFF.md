# HANDOFF
## 作業項目
E6（triage・ログ）完了。あわせてE5から持ち越した2件（role付きquestionの終わり方、publishの`Commit`を差分レビュー承認時点のコミットに結びつける）を実装した
## 完了した契約テスト
C-E1〜C-E6（`go test -count=1 ./contract/ -run 'TestCE1|TestCE2|TestCE3|TestCE4|TestCE5|TestCE6'` 緑、`go vet ./...` 指摘なし）。C-E7は同梱`workflows/develop`が無いため想定どおり失敗
## 未完と理由
なし（E6の範囲と持ち越し2件はすべて実装済み）
## 次の一手
E7（同梱ワークフローとエージェントの移植）。`docs/work-orders.md`のE7を読む
## 注意点
- 実装の場所: `engine/triage.go`（`reportConcern`・`loadConcerns`・`triage`・`openTriage`・`leaf`・`decideTriage`）、`engine/question.go`（`collectAnswer`・`collectedAnswers`）、`engine/commit.go`（`publish`・`approvedCommit`、`lastCommit`は`records`のメソッドに移した）。内部テスト`engine/triage_test.go`
- Storeのキー追加: `concern/<occ>/<nnn>`（ゲストの報告）、`triage-gate/<occ>/<nnn>`（開いたゲートと割り込んだ出現`Interrupted`）、`triage/<occ>/<nnn>`（Decision）、`answer/<occ>/<nnn>`（role付きquestionの`ask_human`の答え1件ずつ）。resultに`ApprovedCommit`を追加
- triageの流れ: `walk`は毎回、`step`の前に`m.triage()`で最初の未決の懸念を見る。ゲート未作成→葉の出現を`Interrupted`に記録して`OpenGate`、未決→`StatusGate`、halt→`block`（理由に懸念の本文とコメント）。dismiss/redoは`records.reenter[Interrupted]`になり、`step`がその出現を「最後の出現」として見たときに同じノードへ再進入する（redoはコメント、無ければ懸念の本文をfeedbackに。dismissは元のfeedbackのまま）。`Status.Occurrence`と`GateRequest.Occurrence`は懸念を報告した出現。`TargetHash`は懸念本文のハッシュ（dismiss/halt/redoでは照合しない）
- 葉の決め方: rootから最後の出現をたどり、結果の無いworkflow/foreachノードは終わっていない子フレームへ下りる。ゲートが開いている間は`ReportResult`等が`waiting`で拒否されrunが動かないので、判断を適用する時点でも同じ出現になる
- dismissは「割り込まれた出現にまだ結果が無いなら再進入、報告済みなら結果に従って進む」と解釈した。報告後・Advance前に懸念が来たとき、終わった作業をやり直させないため。redoは常に再進入
- 進入回数: triageで割り込まれた出現は`records.triaged`経由で`decided`に含まれ、そこで数え直す（「回数に数えない」より強いが、契約の「人間の判断で数え直す」と同じ扱い）
- 待っていない出現（過去の出現）からの懸念も記録してtriageする。存在しない出現・未開始のrunからは拒否。完了・停止済みのrunへの懸念は記録とログ（`concern`）だけ残り、`walk`が先にdone/blockedを返すのでゲートは開かない
- ログ: `concern`（ReportConcern）、`gate-open`（Detail=triage）、`decision`と`triage`（Decideで両方。`triage`のOccurrenceは割り込まれた出現、Detailは処置と懸念本文）を足した。他のkindsはE3〜E5で出ている。`start`も出している（契約のkindsの一覧には無い）
- role付きquestion: タスク中（`StatusAgent`でその出現を待っている）の`Engine.Answer`は検証せずに記録し、`answer`をログ。`ReportResult(done)`で後勝ちマージ→`answers`スキーマで検証→`outputs[0]`に`PutData`→結果は`answered`（resultのOutputsはエージェント自身の出力＋`outputs[0]`）。マージ結果が空・スキーマ不一致は無効な結果として差し戻す。done以外の宣言済みoutcomeはそのまま記録する（questionの行き先は`answered`だけなので、実際にはblockedになる）
- publish: `target: diff`のapprovalが`approved`になった時点の`records.lastCommit()`のハッシュを`result.ApprovedCommit`に記録。publishはrun全体で最後の「diffの承認」を探し、それが無い/コミットが空なら「差分のレビューで承認されたコミットが無い」、その後にcommitの出現があれば「承認後にコミットが進んだ」でblocked
- Decideの誤ったoutcome（承認ゲートにdismiss等、triageにapproved等）は`ErrNotImplemented`ではなく通常のエラーになった

E7へ引き継ぐ未決事項:
1. 同梱`develop`にtriageの到達先は書かない（エンジンの割り込みなので）。`Mermaid`は既に描き込んでいる
2. C-E7は`StatusQuestion`に空の答えを返す。同梱`develop`に固定questionを置くなら、空の答えが`Answer`の検証（全質問に答えが必要）で拒否される点に注意。role付きquestionなら`StatusAgent`として`done`が報告されるが、答えが無いので差し戻され続け、最後はexhausted/無限ループになりうる（question nodeは既定でmax無制限）。C-E7を通すなら`develop`にquestionを経路上で置かないか、置き方を工夫する
3. C-E7の`publish`は承認済みコミットを要する。`develop`はcommitの後に`target: diff`の承認を置き、その後にcommitを挟まずpublishする形でなければblockedになる
4. 書き込めないエージェントの計画外変更でdeviationゲートが待っている間にredoすると、再進入した出現の`BaseHash`に変更が含まれ、同じ変更は二度と検出されない（既知の制限）
## 契約への提案
なし
