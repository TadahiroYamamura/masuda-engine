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
- `check: test`は`exec`ノードに置き換える（コマンドは`["/masuda/checks/test"]`。masudaが対象リポジトリの`settings.json`の`checks.test`をそのパスのスクリプトとして置く。エンジンから見るとただの`exec`）
- `masuda workflow check`相当として、全同梱定義が`Check`に通ること
- 契約テスト: C-E7（スタブで`develop`を歩いてpublishに到達）

## E8. 契約の修正への追従（E7の仕上げ）

- `Decision.ApprovedFiles`の意味を契約どおりに直す（空は「何も加えない」。ゲートに出したが承認されなかったファイルはそのcommitの`Byproducts`に回す。「空なら全部」の扱いをやめる）
- **累積データ**（`workflow-schema.md`「累積データ」）: `x-masuda-accumulate`を持つスキーマのデータは書き込みを配列として集め（`id`で後勝ちの重複排除）、読み出しは連結、未書き込みは`[]`。`Set.Check`では常に用意済み。同梱`findings`のスキーマに`x-masuda-accumulate: true`と要素の`id`を加える
- **データを回すforeach**（同「foreach.over」）: `<データ名>[]`と`<データ名>[field=value]`、`findings`は`findings[]`の省略形。エンジンが項目を作り、累積データでは以前`done`で終わった要素を飛ばす。`Runner.Items`は`steps`・`perspectives`系だけに呼ぶ
- 同梱の`develop`・`review`・`build-step`を累積`findings`前提に見直し（`fix`は`findings[autofix=true]`を回す、synthesizerは累積の全件を読む）、C-E7を緑にする
- 契約テスト: C-E1〜C-E7すべて

## E9. 指摘の取り下げ（後回し。M8の実機1周の後に判断）

- 累積データには要素を消す手段が無く、review-checkerが`inaccurate`とした誤検知も台帳に残ってsynthesizerが拾う。案: 累積データの要素に`withdrawn: true`を同じ`id`で書けば、読み出しから除く（累積の一般規則として契約に足す）。foreachのフィルタは`withdrawn`な要素を常に除く
- 契約変更を伴うので、監督が決めてから着手する → **決めた**: 累積データの要素に`withdrawn: true`を同じ`id`で書けば読み出しとforeachから除く。`workflow-schema.md`「累積データ」に追記すること（監督が文面を書く前に着手してよい。実装後にHANDOFFで文面案を出す）
- **fixerの出口**（M8で実機確認）: 同梱fixerの`outcomes`に`cannot_fix`（直せない理由を`feedback`に）を足し、`fix-finding`で`cannot_fix: end:unresolved`に流す。recheckerの`unresolved`からの再試行回数は`max`で既に抑えている
- **観点の置き場所**（M8で実機確認）: trigger-matcherのプロンプトを、ゲストのcloneの`/workspace/.masuda/reviews/`ではなく**`/masuda/reviews/*.md`**（masudaが起動時に置くスナップショット）を読むように直す。reviewerへ渡る`perspective`の中身は従来どおり`Runner.Items`経由
- 合わせて: ノードの`egress:`でポート付き（`host:port`）を`Load`で拒否する（`workflow-schema.md`に明記済み。masudaの`settings.json`がポートを許さないため、許すと常にBLOCKEDになる）

## E10. ドキュメント整備で見つかった不備

- **`target: diff`の承認対象**（契約を直した。`workflow-schema.md`のapproval.targetの行）: approvalノードの`target: diff`で開くゲートの`TargetHash`と`Subject`は、`Runner.Diff`の新しい種類で取る「baseからブランチ先頭まで」の差分にする。`api.go`の`DiffKind`に`DiffCommitted DiffKind = "committed-diff"`を**監督が足す**（この項目に着手する前に`git log -1 -p -- engine/api.go`で確認）。未コミットの変更は`Runner.ChangedSince(最後のcommit以後の基準)`で一覧を取り、`Subject`の末尾に「publishされない変更」として添える。承認後に新しいcommitがあれば`publish`を`blocked`にする規則（E6）はそのまま
- **triageで中断されたゲートの後始末**: dismiss/redoで入り直したとき、中断された出現が開いていたゲートを「triageで無効」として閉じる（`decision`に記録、`gate-open`の対になるイベントを出す）。`Status`・ホストの一覧から消えること
- **エージェントの`feedback`をログへ**: `finish`イベントの`detail`に`report_result`の`feedback`（先頭200文字）を含める
- 契約テスト: C-E1〜C-E7が緑のまま。`contract/`に「`target: diff`のゲートの`Subject`が、未コミットの`notes.txt`を含むときにそれを『publishされない変更』として分けて載せる」ケースを**監督が足す**ので、着手時に確認

## 契約テストの対応表

| テスト | 項目 |
|---|---|
| C-E1 | E1 |
| C-E2 | E2 |
| C-E3 | E3 |
| C-E4 | E4 |
| C-E5 | E5 |
| C-E6 | E6 |
| C-E7 | E7・E8 |
