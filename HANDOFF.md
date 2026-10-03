# HANDOFF
## 作業項目
同梱の役がスキルを呼べるよう、`engine/defaults/agents/*.md`の`tools`に`Skill`を足した。同梱の役は15個すべてが`tools`を明示しているので、15個すべてが対象（`tools`を省略している役は無い）。契約（`engine/api.go`・`docs/workflow-schema.md`）は変えていない。

- `Skill`はスキルの指示を読み込むだけの道具。`Agent.WriteCapable`（`engine/api.go`）は`Write`か`Edit`を持つか`tools`省略かだけを見るので、書き込めるかの判定は変わらない
- `continues`の「続ける側の`tools`は続けられる側の部分集合」の検査（`engine/check.go`の`toolsWithin`）も、全役に同じ`Skill`を足したので崩れない
- `docs/workflow-schema.md`の`tools`の記述は「`Write`か`Edit`を持つ（または省略）エージェントが『書き込める』」で、`Skill`が書き込みに数えられないことは既に読めるので変えていない
追加: 実機でplan-questionsが`plan`の`checks`（問いと答えの欄）と対象リポジトリの`.masuda/settings.json`の`checks`（ビルド・テストのコマンド）を取り違え、「`plan`の`checks`が空配列だが検査コマンドを載せなくてよいか」と問うたため、`plan-questions.md`と`plan-reviser.md`に両者が無関係であること（`plan`の`checks`は最初の計画では`[]`）を明記した（別コミット）。

## 完了した契約テスト
C-E1〜C-E9すべて緑（`go build ./... && go vet ./... && go test -count=1 ./...`）。`TestBundled*`も緑
## 未完と理由
- 実機でサブエージェントがスキルを呼べるかは確かめていない（masuda側でゲストの`~/.claude/`へスキルを写す機能が未実装のため）
## 次の一手
- masuda側で`.masuda/claude/`（共有）と`.masuda/claude.local/`（個人）の`CLAUDE.md`・`rules/*.md`・`skills/<name>/SKILL.md`をゲストの`~/.claude/`へ写す機能を入れ、engineの版を上げて、実機で役がスキルを呼ぶかを見る
- 前回からの持ち越し: masuda側の`comment-criteria`観点の新設など、`comment-manifest`への追随（前回のHANDOFFの内容は`git show 0300b66:HANDOFF.md`で読める）
## 注意点
- この変更はmasuda側の`.masuda/claude/`（と`.masuda/claude.local/`）のスキル配置と対になる。それが無いとゲストにスキルが無く、`Skill`は呼ばれない
- 対象リポジトリで`agents/*.md`を差し替えている場合、差し替えた定義の`tools`に`Skill`が無ければその役はスキルを呼べない。また差し替えた役が同梱の役を`continues`する（またはその逆の）組み合わせでは、`tools`の部分集合の検査に`Skill`が効くことがある
## 契約への提案
- 前回からの持ち越し: 取り下げられた指摘をレポートで見せるため、累積データの`withdrawn`の要素を保存時に捨てず、読み出し・foreachでだけ除き、synthesizerのような「全部を読む」入力を別に設けるか
- 前回からの持ち越し: 同梱スキーマの列挙から`selected-perspectives`を外すか、`workflow`ノードに`inputs`を書けるようにするか（reviewerへ計画を渡す手段）
