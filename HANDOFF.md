# HANDOFF
## 作業項目
E12（#8）: engineの開発をmasudaのrunで進めるため、`.masuda/`を置いた。契約（`engine/api.go`・`docs/workflow-schema.md`）は変えていない。

- `0be1588` `.masuda/settings.json`（image default、egress・secrets・envFiles空、`checks.test`は`go build ./... && go vet ./... && go test ./...`、`claudeSettings.model: opus`、`images.default.diskMiB: 8192`）、`.masuda/images/default/Dockerfile`と`ctx/go.mod`・`ctx/go.sum`（`go.mod`・`go.sum`の写し）、`.gitignore`に`.masuda/settings.local.json`・`.masuda/claude.local/`
- `fe51ca8` `.masuda/pitfalls.jsonl`（8件: contract-unchanged・bundled-enumeration・continues-tools-subset・accumulate-last-wins・schema-doc-is-user-facing・minimal-dependencies・handoff-overwrite・test-names-as-sentences）
## 完了した契約テスト
C-E1〜C-E9は無修正で緑（`go build ./... && go vet ./... && go test -count=1 ./...`）。ほかの検証:
- `masuda workflow check --repo ../masuda-engine`（フェイクのserve、一時dirのソケット）が`ok`。わざと`category`を壊すと`pitfalls.jsonl: line 2: ...`で問題1件になることも確かめて戻した。serveは止めた
- `docker build -t masuda-engine-dev-check:e12-<時刻> .masuda/images/default`が通り、中で`claude --version`=2.1.287、go1.26.8、gopls v0.23.0、jsonschemaの前取りを確認。そのタグだけ`docker rmi`した
## 未完と理由
- なし。実機（実VM）での`masuda run`はしていない（指示書の検証に含まれない）
## 次の一手
- masuda側のv0.2（#68 #69 #70 #61）。engine側でこのリポジトリに対して実際にrunを1周させ、イメージ・checks・pitfallsが効くかを見る
- masuda #69の結果次第で、役ごとのモデル指定（`Agent.Model`）の契約の提案が来る
- `withdrawn`の保存は#7（v0.3）
## 注意点
- 着手時点で`../masuda/.masuda/`は別の実装者が整えている途中だった（gitignoreされたまま、Dockerfileは`install.sh | bash`で版未固定、pitfalls.jsonl無し、`checks.test`は`go test ./...`のみ）。こちらは指示書の構成で書いた。向こうが仕上がったら差（コメント・goplsの版固定の有無など）を見比べるとよい
- goplsは`@latest`（masudaのDockerfileに倣った）。再現性が要るなら版を固定する
- Claude Codeの版はmasudaの`internal/guest/guest.go`の`ClaudeCodeVersion`の直書き。masudaで上がったらここも上げる。`go.mod`・`go.sum`を変えたら`cp go.mod go.sum .masuda/images/default/ctx/`
- `test-names-as-sentences`は「日本語の文で書く」とした。既存テストの関数名は英語の文（`TestTriageDismissKeepsAFinishedResult`等）で、指示書の「既存テストに倣う」とユーザーのルール（日本語）が食い違うため、日本語を取った。どちらにするか監督が決めて直してよい
- `docs/work-orders.md`のE12の追記（監督の未コミットの変更）はコミットしていない
## 契約への提案
- なし。前回からの持ち越し: 同梱スキーマの列挙から`selected-perspectives`を外すか、`workflow`ノードに`inputs`を書けるようにするか（reviewerへ計画を渡す手段）
