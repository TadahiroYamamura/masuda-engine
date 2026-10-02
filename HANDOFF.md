# HANDOFF
## 作業項目
E2（検査と表示）完了。`Set.Check(root)`・`Set.Reachable`・`Set.Mermaid`
## 完了した契約テスト
C-E1・C-E2（`go test -count=1 ./contract/ -run 'TestCE1|TestCE2'` 緑、`go vet ./...` 指摘なし）。C-E3〜C-E5の各定義は`mustCheck`を通過し、`engine.New`のスタブでpanicして止まる（想定どおり）
## 未完と理由
なし（E2の範囲内）。E3以降は範囲外のため未着手
## 次の一手
E3（エンジンの中核）。`docs/work-orders.md`のE3を読む
## 注意点
- 実装の場所: `engine/check.go`（入口・参照・呼び出し循環・outcomeと`next`・予約ゲート・`with`のキー・閉路・export・共通ヘルパー）、`flow.go`（状態探索）、`avail.go`（データ可用性）、`reachable.go`、`mermaid.go`。内部テストは`engine/check_test.go`（各規則が意図した理由で拒否されること、C-E3〜E5の定義と同梱が通ること）
- 検査の段: 参照解決＋呼び出し循環で問題があれば、それ以降（経路・状態・データ）は走らせない
- outcomeの扱い: `exhausted`は実効max>0（agent/execは常に、他は`max`指定時）のとき行き先を書いてよいが必須でない。`blocked`への行き先は誤り。foreachの必須outcomeは、`continue`なら`done`・`incomplete`、`stop`なら`done`＋本体の終わり方（end label）。**foreachの`incomplete`は`stop`でも書いてよい（必須ではない）**: api.goのコメントは「continueのときだけ」だが、C-E5の定義が`stop`で`incomplete`に行き先を書いているため。E5で`stop`時に何を出すか実装するときはこれと揃えること（`stop`で本体が`done`以外で終わったら、そのend labelをforeachのoutcomeとして出す、という前提で検査している）
- 状態探索: 書き込めるエージェント（`question`の`role`も含む）は承認済み計画が必要で、実行後は未コミット扱い。`plan`を書くノードは承認を無効化する。`approval target: plan`の`approved`で承認済み。`commit`は計画必須で`done`で未コミット解消、`rejected`では解消しない。`foreach over: steps`も承認済み計画を要求する。execは状態を変えない扱い（旧設計踏襲）
- データ可用性: エンジンのデータ（`diff`・`step-diff`・`fix-diff`）は常に用意済み扱い。呼び出し元で用意済みのものは呼び出し先でも見える（名前で解決）。出力は`done`（questionは`answered`）でだけ増える。foreachの後には本体の出力を持ち出さない。`commit scope: step`は`step`が用意済みであることを要求する（foreach over stepsの本体なら満たす）
- foreachの項目の入力名: `steps`→`step`、`findings`→`finding`、`perspectives*`→`perspective`、`<data>[]`→単数形（`-ies`→`-y`、末尾`s`を落とす、`ss`はそのまま）。`engine/check.go`の`itemInput`。E5で`Runner.Items`が返す`Item.Input`の期待値と揃えること
- `export`は経路ごとの必須にしていない。到達範囲の誰も書かない名前だけ誤り
- `Reachable`は root・呼び出し先ワークフロー・role のエージェント・言及されたデータ名のうちスキーマを持つもの（`schemas/<name>`）を返す（ソート済み）
- `Mermaid`は呼び出し先をsubgraphで描く。deviationはcommitの前と書き込めないエージェントの後、triageは全体に1つ
## 契約への提案
監督の判断が要るもの（E2はこのまま完了扱いでよく、ブロッキングではない）:
1. foreachの`incomplete`の意味: api.goの`OutcomeIncomplete`のコメント（continueのときだけ）と、`workflow-schema.md`の表（常に`done`・`incomplete`・bodyの終わり方）・C-E5の定義（`stop`で`incomplete`を書く）が食い違う。案: `stop`では本体のend labelをそのまま出し、`incomplete`は`continue`のときだけ出す、と表に明記する（現実装の前提）。その場合`stop`での`incomplete`の行き先を誤りにするかは要決定（現状は許容）
2. 単独では承認前に書き込むワークフロー（foreach over stepsの本体など）をrootとして`Check`したときの扱い: C-E7は同梱の全ワークフローを`Check(path)`して問題ゼロを要求するが、現実装ではstepの本体が「承認前の書き込み」で拒否される。案A: 入力に`step`を宣言するワークフローは承認済み計画のある状態から始まるとみなす。案B: C-E7が検査するのはrootとして使うワークフローだけにする。案C: 同梱の本体を書き込まない形にする（実用上無理）。E7着手前に決める必要がある
