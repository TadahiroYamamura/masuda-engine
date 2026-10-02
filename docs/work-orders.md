# 作業単位（masuda-engine）

1項目を1セッションで終える。完了の判定は対応する契約テスト（`contract/`）が緑であること。契約（`engine/api.go`・`docs/workflow-schema.md`）は変えない。

共通の前提: 全体設計は`../masuda/docs/design/overview.md`の第5章。移植元は`git -C ../masuda show v1-frozen-workflow-engine:internal/workflow/<path>`で読む（`def/parse.go`・`def/types.go`・`check/*.go`・`engine/engine.go`・`engine/records.go`・`render/mermaid.go`・`data/*.go`・`defaults/`）。旧設計には工程型（investigate/plan/implement/review）があったが新設計には無い。旧`Env`は新`Runner`に対応する。

## E1. 定義の読み込み

- `engine.Load(repo, bundled)`: YAMLとMarkdown frontmatterを読み、`workflow-schema.md`の形を検査する（種類ごとに許すキー、`next`の形、`max`、データ名の形、`timeout`、`command`が絶対パスのargv）。`repo`のファイルが同じ参照パスの同梱を置き換える。`Origins`を埋める
- `engine.Bundled()`: `defaults/`を`embed`で返す。この時点では最小の`workflows/smoke`と`agents/echo`だけでよい（本物の同梱はE7）
- 同梱スキーマ`schemas/{plan,findings,commit-message,selected-perspectives,answers}.json`（draft 2020-12）。旧`data/plan.go`・`data/findings.go`の検証規則をJSON Schemaに写す
- `Agent.WriteCapable`
- やらないこと: ファイルをまたぐ検査（E2）
- 契約テスト: C-E1

## E2. 検査と表示

- `Set.Check(root)`: `workflow-schema.md`「読み込み時の検査」の全規則。状態（ノード・未コミット変更の有無・承認済み計画の有無）の探索は旧`check/flow.go`の考え方を使う
- `Set.Reachable`、`Set.Mermaid`（エンジンの割り込み triage・deviation を描き込む）
- 契約テスト: C-E2

## E3. エンジンの中核

- `Store`のキー設計: `<run>/occ/<id>`、`<run>/result/<id>`、`<run>/frame/<fid>`、`<run>/frame-end/<fid>`、`<run>/gate/<occ>`、`<run>/question/<occ>`、`<run>/concern/<occ>`、`<run>/blocked`、`<run>/start`。出現IDはゼロ詰め連番
- `New`・`Start`・`Advance`（`agent`・`approval`・`workflow`・`end`）・`Status`
- `ReportResult`: 宣言外outcomeの拒否、`done`時に全`outputs`が`ReadOutput`で読めることとスキーマ検証、失敗時は理由を差し戻しにして同じノードへ再進入（回数に数える）、受け付けた出力を`PutData`
- `Decide`: `TargetHash`不一致の承認は拒否。`rejected`のコメントは次ノードの差し戻し
- 進入回数の上限（`exhausted`）と`Fuse`
- 差し戻しの受け渡し（次が`workflow`/`foreach`なら呼び出し先の最初のノードへ）
- `Advance`の冪等性
- やらないこと: exec・question・foreach・commit・publish
- 契約テスト: C-E3

## E4. exec・question・方針・スナップショット

- `exec`: `Runner.RunCommand`。exit 0で`done`、他は`failed`（`LogTail`を差し戻しに）。`Outputs`をスキーマ検証して`PutData`
- `question`: 固定の`questions`なら`Runner.OpenQuestion`→`Answer`待ち。`role`ならエージェントタスクを出し、エージェントが`ask_human`経由で質問する（エンジンから見ると`OpenQuestion`→`Answer`→`ReportResult`）。答えは`outputs[0]`にJSONで保存
- 毎ノード進入時（`agent`/`exec`）に`Runner.SetPolicy`
- ノード境界で`Runner.Snapshot`し、出現に記録。`diff`・`step-diff`・`fix-diff`は`Runner.Diff`で必要時に計算
- 契約テスト: C-E4

## E5. foreach・commit・publish・discard・逸脱

- `foreach`: 最初に`Runner.Items`で項目を確定し出現に記録、項目ごとにフレームを作って直列。`steps`は`Done`の項目を飛ばす。`on_incomplete`。`findings`の反復開始時にスナップショット（`fix-diff`の起点）
- `commit`: 直前のスナップショットからの変更を`ChangedSince`で取り、計画の対象（`Allowed`）外があれば`deviation`ゲート→承認されたファイルは以後`Allowed`に加える。`Runner.Commit`。`rejected`
- 書き込めないエージェントの前後で`ChangedSince`し、変わっていれば`deviation`ゲート
- `publish`（承認済みハッシュを渡す）・`discard`
- 契約テスト: C-E5

## E6. triage・ログ

- `ReportConcern`→次の`Advance`で何より先に`triage`ゲート。`dismiss`は再進入（回数に数えない）、`halt`は`blocked`、`redo`は差し戻し付き再進入
- `Runner.Log`に全イベント（`api.go`のEvent kinds）
- 契約テスト: C-E6

## E7. 同梱ワークフローとエージェントの移植

- 旧`defaults/workflows/*`と`defaults/agents/*.md`を新しい語彙へ。工程型は`agent`/`exec`/`workflow`で表す。`develop`・`review`・`implement/build-step`・`review/perspectives`・`review/perspective-review`・`review/cross-cutting`・`fix-finding`と11エージェント
- `check: test`は`exec`ノードに置き換える（コマンドは対象リポジトリの`settings.json`の`checks.test`をmasudaが`/masuda/in/<occ>/`に展開した`run-check`スクリプトとして渡す。エンジンから見るとただの`exec`）
- `masuda workflow check`相当として、全同梱定義が`Check`に通ること
- 契約テスト: C-E7（スタブで`develop`を歩いてpublishに到達）

## 契約テストの対応表

| テスト | 項目 |
|---|---|
| C-E1 | E1 |
| C-E2 | E2 |
| C-E3 | E3 |
| C-E4 | E4 |
| C-E5 | E5 |
| C-E6 | E6 |
| C-E7 | E7 |
