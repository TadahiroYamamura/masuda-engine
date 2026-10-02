# masuda-engine

masudaのワークフローエンジン。Goライブラリ。全体像は[masudaリポジトリの`docs/design/overview.md`](../masuda/docs/design/overview.md)、契約は`engine/api.go`と`docs/workflow-schema.md`。

## 開発

- `go build ./... && go vet ./... && go test ./...`
- 契約テスト: `go test ./contract/`。これが緑なら作業項目は完了、赤なら未完。実装者の自己申告では判定しない
- 外部依存は最小限に。JSON Schema検証とYAMLパーサーは可。ネットワーク・git・プロセス起動のライブラリは入れない（エンジンはそれらを知らない）

## GitHub操作

`gh`を直接使わず`scripts/gh.sh`を使う（`.env`のトークンを渡すラッパー。`.env`はClaudeから読めない）。

## 契約の扱い

- `engine/api.go`の公開名・シグネチャ・フィールド、`docs/workflow-schema.md`は契約。**変えない**
- 変えたくなったら、`HANDOFF.md`の「契約への提案」に理由と案を書いて止まり、監督の判断を待つ
- 非公開のファイル・パッケージは自由に足してよい

## 作業の進め方

- 作業単位は`docs/work-orders.md`の1項目。1項目を1セッションで終える
- セッション開始時: `HANDOFF.md`→`docs/work-orders.md`の該当項目→契約ファイルの順に読む
- 探索（移植元のコードを読む、長いテスト出力を読む）はサブエージェントに出し、結果だけ受け取る。実装は自分で書く
- 移植元: masudaリポジトリのタグ`v1-frozen-workflow-engine`の`internal/workflow/`（`def`・`check`・`engine`・`render`・`data`）。読むときは`git -C ../masuda show v1-frozen-workflow-engine:internal/workflow/<path>`。設計は変わっているので写経せず、契約に合わせて書き直す
- コミットはユーザーの承認を得てから。メッセージは`<type>(<scope>): <summary>`に`## 意図`・`## 設計上の考慮点`・（あれば）`## 懸念事項`。ADRは書かない

## HANDOFF.md

セッション終了時に、次の見出しで**上書き**する（スキルは使わない。読むのはエージェント）。

```
# HANDOFF
## 作業項目
## 完了した契約テスト
## 未完と理由
## 次の一手
## 注意点
## 契約への提案
```

## コメント

コードのコメントは、10行以上の要約、他の選択肢がある中での選択理由、コードから読めない背景、トレードオフ、のいずれかを満たすものだけ書く。
