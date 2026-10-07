# HANDOFF
## 作業項目
2026-10-07: masuda#99（案a）。`type: privileged`ノードを足した。v0.3.0として公開した（`ce9a878`、タグv0.3.0）。作業はmasudaのセッション（masuda-d3）の監督のもと、サブエージェントが実装した。

- 契約: `NodePrivileged`・`Node.PrivilegedName`・`PrivilegedTask`・`Runner.RunPrivileged`（`engine/api.go`）、`docs/workflow-schema.md`の「特権コマンド（privileged）」
- ノードは`name`（masudaの`privilegedCommands`の名前の形だけを検査）と`max`だけを持つ。終了コード0で`done`、それ以外（時間切れを含む）で`failed`（feedbackにログの末尾）、`max`超えで`exhausted`。SnapshotもSetPolicyも呼ばず、計画外変更の基準点にもしない。承認済み計画の前に置いてよい
- 未宣言・未承認はRunnerのエラーとして、記録せずにAdvanceがエラーを返す。masudaではこれが`suspended`になり、承認してresumeすると同じ出現をやり直す
- 同梱の`implementer.md`: 宣言は`/masuda/privileged-commands.json`（masudaが置く）から読む。呼べたが失敗した・確かめられなかったときは`done`を返さない（コードの誤りなら直して再実行し、それでもだめなら`stuck`）
- 同梱のワークフローは変えていない（developにprivilegedノードを入れるかは未決）
## 完了した契約テスト
- `go build ./... && go vet ./... && go test ./...`が緑（`ce9a878`）。判定を16通り壊して落ちることを確かめた
## 未完と理由
- 同梱のdevelopで特権コマンドを強制する方法（宣言があるときだけprivilegedノードを通す等）は決めていない
## 次の一手
1. 上の未決をmasuda側の利用の様子を見て決める
## 注意点
- Runnerのインターフェースを変えたので、masudaの`internal/runner`とフェイク（contract・engineのテスト）が追従している。Runnerに足すときは同じ範囲に及ぶ
- privilegedノードはmasudaの`advance()`が`c.mu`を握ったまま同期で呼ぶ。masuda側の実行の関数で`c.mu`を取ると止まる（masudaの契約テストC-M11が検出する）
## 契約への提案
- なし
