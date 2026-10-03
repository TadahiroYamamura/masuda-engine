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

## E11. commit前の承認（`target: step-diff`）

- M12で、`target: diff`をコミット済みの差分にした結果、commitの前に置く`approve-interim`（`implement/build-step`）の承認対象が空になると分かった。契約に`target: step-diff`（ブランチ先頭..作業ツリー＝これからcommitされる内容）を足した（`workflow-schema.md`のapproval.target）
- `Load`/`Check`で`step-diff`を受け付け、approvalノードは`Runner.Diff(DiffFromHead, "", into)`で差分を取り、その内容を`Subject`・ハッシュを`TargetHash`にする（「publishされない変更」の一覧は付けない。全部がこれからcommitされるため）
- 同梱`implement/build-step`の`approve-interim`を`target: step-diff`に変える
- 契約テスト: C-E1〜C-E7が緑のまま。`contract/`に「`target: step-diff`のゲートが`Diff(step-diff)`で開く」ケースを監督が足す

## E12. engineリポジトリに`.masuda/`を置く（#8）

masudaの開発作業をmasudaのrunで進める体制（masudaのマイルストーンv0.2）の一部。engineの開発作業もmasudaのrun（`workflows/develop`・`workflows/fix`）で進められるように、このリポジトリに`.masuda/`を置く。形は`../masuda/.masuda/`（masuda自身のもの。同時に整えているので、着手時に`git -C ../masuda status -- .masuda`と中身を見る）に倣う。契約（`engine/api.go`・`docs/workflow-schema.md`）は変えない。pushしない。

- `.masuda/settings.json`: `image: "default"`、`egress: []`（Goモジュールはイメージで前取りする）、`secrets: []`、`envFiles: []`、`checks.test`は`go build ./... && go vet ./... && go test ./...`、`claudeSettings: {"model": "opus"}`（既定の役をOpusにする。ユーザー決定）、`images.default.diskMiB`はmasudaと同じ値
- `.masuda/images/default/Dockerfile`: masudaの`.masuda/images/default/Dockerfile`と同じ構成（ubuntu:24.04、ca-certificates・curl・git・tmux・openssh-server、Go 1.26.8、`ctx/go.mod`・`ctx/go.sum`からの`go mod download all`、gopls、`GOCACHE=/tmp/go-cache`、Claude Codeの版固定`bash -s -- <版>`）。版は`../masuda/internal/guest/guest.go`の`ClaudeCodeVersion`と同じ数字を直書き。`ctx/go.mod`・`ctx/go.sum`はこのリポジトリの`go.mod`・`go.sum`の写し。冒頭のコメントに「`go.mod`を変えたら`ctx/`の写しも更新する」ことを書く
- `.masuda/pitfalls.jsonl`（形式は`../masuda/docs/user/settings.md`の`pitfalls.jsonl`の節。`id`・`category`・`trigger`・`question`・`background`すべて必須、`category`は`spec`・`security`・`data`・`release`・`regression`・`performance`・`maintainability`・`other`のいずれか）。少なくとも次を書く。`background`は`HANDOFF.md`・`git log`から実際にあったことを引く:
  - 契約`engine/api.go`・`docs/workflow-schema.md`は変えない（`HANDOFF.md`の「契約への提案」に書いて止まる）
  - 同梱の定義（`engine/defaults/`）を足したり消したりしたら、同梱の列挙と`bundled_test.go`の歩行テストを更新する
  - 役の`continues`は「続ける側の`tools`は続けられる側の部分集合」。全役に共通の道具（`Skill`等）を足すときは全部に足す
  - 累積データ（`x-masuda-accumulate`）の要素は`id`で後勝ち。`withdrawn`の扱いは#7（保存時に捨てられる）
  - `docs/workflow-schema.md`はmasudaのドキュメントサイトに写される。利用者が読む文として書く
  - 外部依存は最小限（ネットワーク・git・プロセス起動のライブラリは入れない）
  - `HANDOFF.md`はセッション終了時に決まった見出しで上書きする
  - テストケース名は日本語で何を確かめるかを文で書く（既存テストに倣う）
- `.gitignore`に`.masuda/settings.local.json`と`.masuda/claude.local/`を足す（masudaの`masuda init`が足す行と同じ。`../masuda/cmd/masuda/init.go`の`localIgnores`を見る）。`.env`の行はそのまま
- **検証**: `go build ./... && go vet ./... && go test -count=1 ./...`が緑。`pitfalls.jsonl`の検査は、masudaのCLIで行う: `cd ../masuda && GOWORK=off go run ./cmd/masuda serve --fake-sandbox --data-dir <一時dir> --socket <一時dir>/m.sock`を立て、`GOWORK=off go run ./cmd/masuda workflow check --repo ../masuda-engine --socket <一時dir>/m.sock`が問題を出さないこと（終わったらそのserveを止める）。Dockerfileは`docker build -t masuda-engine-dev-check:<一意な接尾辞> .masuda/images/default`が通ること（終わったら`docker rmi`でそのタグだけ消す。他のイメージ・コンテナには触らない）
- 禁止: `$XDG_RUNTIME_DIR/`配下のソケットと`~/.local/share/masuda*`に触れる、`docker rm -f`・`docker system prune`、他プロセスの`kill`、`git push`、`../masuda`の変更
- 完了の判定: 上の検証が緑で、`.masuda/`がgitに追跡されていること。契約テストC-E1〜C-E9は無修正で緑のまま。終わったら`HANDOFF.md`を上書きし、最終報告は「コミット・検証・指示から外れた点」を10行以内

## E13. 役定義にモデルとeffortを書けるようにする（masuda #69、契約変更）

**契約変更（監督が決定、ユーザー承認済み 2026-10-03）**: `engine/api.go`の`Agent`に`Model string`と`Effort string`を足し、`docs/workflow-schema.md`のエージェント定義に`model`・`effort`を書く。この項目に限り、この2箇所を変えてよい（他の公開名・シグネチャは変えない）。

背景: masudaのrunでは役（サブエージェント）がゲストのClaude Codeの既定のモデルで動き、役ごとにモデルや推論の努力量（effort）を変える手段が無い。Claude Codeのサブエージェント定義はfrontmatterの`model`（`sonnet`・`opus`・`haiku`等の別名、フルのモデルID、`inherit`）と`effort`（`low`・`medium`・`high`・`xhigh`・`max`）を受け付け、どちらも効くことをmasuda側で実測した。ノード単位の上書きは入れない（ゲストは役名でサブエージェントを起動するため）。同じ役を違う設定で使いたい利用者は役定義を複製する。

- `Agent.Model`・`Agent.Effort`: 省略は空文字列（「指定しない」。Runner／ゲストの既定に任せる）。エンジンは値を解釈せず、Runnerへ`AgentTask.Agent`経由でそのまま渡す（`AgentTask`には足さない。`Agent`に入っていれば届く）
- 読み込み（frontmatter）: `model`は空でない文字列。`effort`は`low`・`medium`・`high`・`xhigh`・`max`のいずれか（Claude Codeが受け付ける値。それ以外は`tools`等の形の誤りと同じ段階で拒否し、メッセージに受け付ける値を列挙する）。`model`の値は検査しない（別名・フルID・`inherit`のどれも通す。Claude Code側の語彙で、engineが追いかけない）
- `WriteCapable`・`continues`の検査（`tools`の部分集合）は変えない。`continues`で続きが成立したサブエージェントは起動時の設定のまま動くので、続ける側の`model`・`effort`は使われない。このことを`workflow-schema.md`の「続き」に1行足す
- `docs/workflow-schema.md`「エージェント定義」: 例に`model: sonnet`と`effort: low`を加え、箇条書きに「`model`・`effort`は任意。Runner（masudaではゲストのClaude Codeのサブエージェント定義のfrontmatter）にそのまま渡す。省略はその環境の既定（masudaでは`claudeSettings.model`のメインセッションのモデルを継承）。`effort`は`low`・`medium`・`high`・`xhigh`・`max`」を足す。この文書はmasudaのドキュメントサイトに写されるので、利用者が読む文として書く
- 同梱の役（`engine/defaults/agents/*.md`）には`model`・`effort`を書かない（既定を継承する）。`bundled_test.go`は変えなくてよい
- テスト（テストケース名は日本語で、何を確かめるかを文で書く）: `model`・`effort`がfrontmatterから`Agent`に入る、`effort`の不正な値が拒否される（メッセージに値の一覧）、`effort`が無い役は空のまま。判定の分岐をわざと壊してテストが落ちることを確かめる
- 検証: `go build ./... && go vet ./... && go test -count=1 ./...`が緑。契約テストC-E1〜C-E9は無修正で緑のまま
- `HANDOFF.md`の「契約への提案」には「E13で`Agent.Model`・`Agent.Effort`を足した（承認済み）。masuda側は`internal/guest.AgentFile`と`docs/guest-protocol.md`で追従する（M14b）」と書く
- 禁止: `git push`、`../masuda`の変更、新しい依存の追加

## E14. 同梱`develop`の見直し: 途中レビューを外し、ゲートの却下を直前の役へ戻す（masuda #68の計測、engine #10）

背景: masuda自身でのdogfooding（2026-10-03、masuda #68のコメントに計測の表）で、`develop`は`fix`の数倍の時間とトークンがかかり、原因が工程の数にあると分かった。(1) ステップごとの途中レビュー（`implement/interim-review`）は記録の範囲で自動修正0件（見つけた低1件は`autofix: false`でfixerが動かず、最終レビューが同じ指摘を出して直した）。(2) plan gateの却下はplanner→plan-questions→reviser→interviewerを一式やり直して約12分、review gateの却下は最終レビューの全段をやり直して約30分。契約（`engine/api.go`・`docs/workflow-schema.md`）は変えない。同梱定義とその本文・テストだけを変える。

- **`workflows/implement/build-step`**: `implement`→`test`→`commit`にする（`workflows/fix/build-step`と同じ形。`test`が`failed`なら`implement`へ）。`review`・`fix`・`recheck`・`approve-interim`のノードを消す。`workflows/implement/interim-review`は使われなくなるので定義ごと消し、同梱の列挙と`bundled_test.go`の歩行テスト（3ステップの歩行がinterimの結果を前提にしている）を新しい形に直す。ゲート名`interim`は予約の扱いのまま（利用者の定義が使ってよい）
- **plan gateの却下**: `approve-plan.rejected`を`plan`（planner）ではなく、新しいノード`revise-rejected`（`role: agents/plan-reviser`、`max: 3`、遷移は`revise-answered`と同じ: `done: approve-plan`、`needs_human: ask`、`needs_more_investigation: investigate`、`exhausted: approve-plan`）へ。人間の却下理由は`feedback`で届く。`plan-reviser.md`の本文に「feedbackに人間の却下理由があるときは、それを最優先の問いとして扱い、`plan-checklist`のうち既に`addressed`の問いは答え直さない（却下理由で前提が変わったものだけ見直す）」を足す。`ask`→`revise-answered`の形は変えない
- **review gateの却下**: `approve-review.rejected`は`rework`のままだが、`rework`を`role: agents/implementer`＋`continues: agents/implementer`にする（直前に実装したサブエージェントの続きで、人間の行コメント＝feedbackを直す。記憶が無くても成立する入力は今のまま）。`rework-test`→`rework-commit`の後は`review`（最終レビューの全段）へ戻さず、**`approve-review`へ直接**戻す（人間が自分の指摘が直ったかを差分で見る。レビューの全段は通さない。cross-cuttingもsynthesizerも通さない）。`review-commit.rejected: rework`（deviation gateの却下）は同じ`rework`を使うので、こちらも`approve-review`へ戻ることになる。それでよい（人間が直前に差分を見ている）
- 最終レビューの段（`review`→`cross-cutting`→`fix`→`recheck`→`review-commit`→`report`→`approve-review`）は変えない。masuda #67（レビュー段階のpr-review-guide: `review-questions`・`review-answers`を`review`の前後に差し込む、fixerの`fix-log`、レポートのJSON化）はv0.3でこの段に入るので、ノード名と順序は保つ
- `workflows/fix`は変えない（`fix`の`plan.rejected: plan`はquick-plannerが調査と計画を1セッションでやり直す形で、やり直しの範囲が小さい）
- `docs/workflow-schema.md`に途中レビューやinterimの記述があれば直す（無ければ何もしない）。同梱ワークフローの説明がmasudaの`docs/user/workflows.md`にあるが、それはmasuda側で直す（engineでは触らない）
- テスト（名前は日本語の文）: `bundled_test.go`の歩行テストを新しい`develop`に合わせる（3ステップがimplement→test→commitで進む、plan gateの却下が`revise-rejected`に入る、review gateの却下が`rework`→`rework-test`→`rework-commit`→`approve-review`に戻る）。判定の分岐をわざと壊して落ちることを確かめる
- 検証: `go build ./... && go vet ./... && go test -count=1 ./...`が緑。契約テストC-E1〜C-E9は無修正で緑のまま
- 禁止: `git push`、`../masuda`の変更、契約ファイルの変更、新しい依存の追加

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
