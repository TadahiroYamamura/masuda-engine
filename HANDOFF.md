# HANDOFF
## 作業項目
E11（commit前の承認 `target: step-diff`）。完了（d5b4147）
## 完了した契約テスト
C-E1〜C-E7すべて緑（`go test -count=1 ./contract/`）。C-E5の`approve-step`（`target: step-diff`）は、`resolve`経由の偶然ではなく`approval`の明示的な分岐で`Diff(step-diff)`を呼んで通る。`go vet ./...`指摘なし、`go test -count=1 ./...`緑
## 未完と理由
なし
## 次の一手
- masuda側（M13）で下の取り決めを確かめ、サンドボックスイメージを再ビルドしてから実機1周で、interim gateのSubjectにこれからcommitされる差分が出ることを確かめる
## 注意点
- 実装の置き場所
  - `engine/run.go`の`approval`: `target`で分岐する。`diff`→`Runner.Diff(DiffCommitted, "", DataRef{Name: "committed-diff", Occurrence: ゲートの出現})`＋`withUnpublished`（E10のまま）。`step-diff`→`Runner.Diff(DiffFromHead, "", DataRef{Name: "step-diff", Occurrence: ゲートの出現})`、Subjectは差分そのもの、TargetHashはそのsha256、未コミット一覧は付けない。それ以外→`resolve`（データ名）
  - diff・step-diffを`resolve`に通さないのは、`resolve`がフレームの束縛（`with`）や同名の以前の値を先に返すため。ゲートは常にその時点のブランチを見る
  - `Decide`で`ApprovedCommit`を記録するのも、publishが参照する`approvedCommit`も、`target: diff`のゲートだけ。step-diffの承認はpublishの条件にならない
  - `engine/avail.go`: approvalの`target`が`diff`/`step-diff`なら可用性を問わない。それ以外（`plan`・データ名）は可用性の解析で拒否される。`fix-diff`はエンジンのデータとして従来どおり受け付ける（foreachの外では`iterationTree`が無く実行時エラーになりうるが、E11の範囲外なので触っていない）
  - `engine/parse_workflow.go`の`target`のエラー文に`step-diff`を足した
  - 同梱`implement/build-step`の`approve-interim`は`target: step-diff`。Mermaidはノードの`target`をそのまま出すので追加の変更は無い
- masuda側（M13）への取り決め
  - interim gate（`target: step-diff`）の`GateRequest.Subject`は`Runner.Diff(DiffFromHead, "", into)`が`into`に書いた内容そのもの（ブランチ先頭..作業ツリー、未追跡ファイルを含む＝`staging`の`Commit`がこれから取り込む内容）。「publishされない変更」の見出しは付かない
  - `TargetHash`はその内容のsha256（hex）。`Decide`の`TargetHash`はこれと一致させること
  - `into`は`DataRef{Name: "step-diff", Occurrence: <ゲートの出現>}`。masudaの`Runner.Diff`は`step-diff`（`DiffFromHead`）を既に受けている（エージェントの`step-diff`データと同じ種類）はずだが、M13で確認すること
  - `gate show`等で`Target`を表示するなら、`diff`＝「publishされる内容（コミット済み）」、`step-diff`＝「これからcommitされる内容（未コミット）」と区別して出すとよい
## 契約への提案
なし
