# HANDOFF
## 作業項目
同梱の役がスキルを呼べるよう、`engine/defaults/agents/*.md`の`tools`に`Skill`を足した。同梱の役は15個すべてが`tools`を明示しているので、15個すべてが対象（`tools`を省略している役は無い）。契約（`engine/api.go`・`docs/workflow-schema.md`）は変えていない。

- `Skill`はスキルの指示を読み込むだけの道具。`Agent.WriteCapable`（`engine/api.go`）は`Write`か`Edit`を持つか`tools`省略かだけを見るので、書き込めるかの判定は変わらない
- `continues`の「続ける側の`tools`は続けられる側の部分集合」の検査（`engine/check.go`の`toolsWithin`）も、全役に同じ`Skill`を足したので崩れない
- `docs/workflow-schema.md`の`tools`の記述は「`Write`か`Edit`を持つ（または省略）エージェントが『書き込める』」で、`Skill`が書き込みに数えられないことは既に読めるので変えていない
追加: 実機でplan-questionsが`plan`の`checks`（問いと答えの欄）と対象リポジトリの`.masuda/settings.json`の`checks`（ビルド・テストのコマンド）を取り違え、「`plan`の`checks`が空配列だが検査コマンドを載せなくてよいか」と問うたため、`plan-questions.md`と`plan-reviser.md`に両者が無関係であること（`plan`の`checks`は最初の計画では`[]`）を明記した（別コミット）。

追加: `review-checker.md`の`inputs`に`comment-manifest`を足し、コメントに関する指摘（`comment-criteria`等）を検証するときに一覧と差分の照合を確かめるよう本文に書いた（別コミット）。`bundled_test.go`の`checkCommentManifest`でreview-checkerの`Inputs`も確かめる。`workflows/review`でも累積データなので`Set.Check`は通る

## 完了した契約テスト
C-E1〜C-E9すべて緑（`go build ./... && go vet ./... && go test -count=1 ./...`）。`TestBundled*`も緑
## 未完と理由
- なし。実機でサブエージェントがスキルを呼べることはmasudaの`live/claude_dir_test.go`（`TestClaudeDirReachesSubagent`、Claude Code 2.1.287、`Skill=1`）で確認済み
## 次の一手
- **v0.2の目標は「masudaを使ってmasudaが作れる体制」**（ユーザー決定、2026-10-03）。engine側の項目は**#8**（engineリポジトリに`.masuda/`を置く）。masuda側はmasudaのマイルストーンv0.2（#68 #69 #70 #61）
- masuda #69の結果次第で、役ごとのモデル指定（`Agent.Model`）の契約の提案が来る
- `withdrawn`の保存は**#7**（v0.3、masuda #67と一緒に）
- mainは`fd33f3c`までpush済み。次のリリースはv0.2.0（契約変更: `continues`、`done`以外の出力の保存）
## 注意点
- この変更はmasuda側の`.masuda/claude/`（と`.masuda/claude.local/`）のスキル配置と対になる。それが無いとゲストにスキルが無く、`Skill`は呼ばれない
- 対象リポジトリで`agents/*.md`を差し替えている場合、差し替えた定義の`tools`に`Skill`が無ければその役はスキルを呼べない。また差し替えた役が同梱の役を`continues`する（またはその逆の）組み合わせでは、`tools`の部分集合の検査に`Skill`が効くことがある
- 解消済み: `0300b66`のHANDOFFにあった「review-checkerは`comment-manifest`を入力に取らず、`comment-criteria`の指摘の検証で一覧を読めない」は、review-checkerの`inputs`に足して解消した。masuda側で`agents/review-checker.md`に足す必要は無い
## 契約への提案
- `withdrawn`の保存はIssue #7に移した（v0.3）
- 前回からの持ち越し: 同梱スキーマの列挙から`selected-perspectives`を外すか、`workflow`ノードに`inputs`を書けるようにするか（reviewerへ計画を渡す手段）
