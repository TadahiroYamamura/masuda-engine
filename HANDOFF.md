# HANDOFF
## 作業項目
同梱の計画スキーマ`plan`の構造化（masudaの`gate show`で計画を読みやすく出すための前提）。`plan.json`に`goal`・`steps[].title`・`steps[].tests`・`alternatives[]`・`risks[]`を足し、`planner.md`の書き方の指示を「goal→人間が認識できる単位のステップ→description・tests→files」の分解に書き直した。`docs/workflow-schema.md`の同梱スキーマの記述も合わせた

続けて`implementer.md`の冒頭（`step`だけを実装する段落の後ろ）に、ステップの`tests`に挙がったテストもそのステップで書いて自分で通すこと、テストのコードはステップの`files`に挙がっているファイルに置くこと、の2文を足した（plannerの「実装とそのテストは同じステップ、テストファイルもfilesに挙げる」方針と対になる）
## 完了した契約テスト
C-E1〜C-E7すべて緑（`go test -count=1 ./...`）。`go vet ./...`指摘なし。テストのfixtureの計画はすべて新スキーマに直し、`TestBundledSchemas`に新項目の必須・`additionalProperties`の検査と、旧スキーマの計画が拒否されることの検査を足した
## 未完と理由
なし
## 次の一手
- masuda側で`go.mod`のengineをこの版に上げる（タグを打つかmainへpushした後に`go get github.com/TadahiroYamamura/masuda-engine@<tagまたはmain> && go mod tidy`）。それまでmasudaは`go.work`経由でしか新スキーマを使えない
- 実機1周で、plannerが新スキーマの計画（特にtitleが機能単位になり、テストファイルがfilesに入るか）を書けるか、implementerが`tests`のテストを同じステップで書くかを確かめる
## 注意点
- `engine/commit.go`の`planDoc`/`stepDoc`は変えていない。読むのは`summary`・`steps[].description`・`files`・`expected_byproducts`だけで、新しい項目は無視される。commitメッセージの代替は従来どおり`description`
- `alternatives`・`risks`は必須（空配列可）、`steps[].tests`も必須（空配列可）。キーの書き忘れはスキーマ検証で落ちてplannerに差し戻される
- 旧スキーマの計画（既存ワークスペースの記録）は新スキーマでは検証に通らない。エンジンが記録済みの計画を再検証する経路は無いので、再開中のrunには影響しないはず（再開後に計画を書き直す場合は新スキーマが要る）
- `implementer.md`は`tests`の扱い（このステップで書いて通す、置き場所は`files`の中）だけを足した。`files`以外は触らないという指示はそのまま
## 契約への提案
なし。`docs/workflow-schema.md`は同梱スキーマの列挙の1行だけを、監督の指示に従って新スキーマに合わせた
