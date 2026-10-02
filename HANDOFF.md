# HANDOFF
## 作業項目
E8（契約の修正への追従。E7の仕上げ）。完了
## 完了した契約テスト
C-E1〜C-E7すべて緑（`go test -count=1 ./contract/`）。`go vet ./...`指摘なし、`go test ./engine/`緑
## 未完と理由
なし
## 次の一手
- `docs/work-orders.md`の次の項目へ。E系の項目はE8で一巡した
- masuda側（M5〜）が下の「注意点」の取り決めに沿っているかを、masudaの契約テストで確かめる
## 注意点
- 実装の置き場所
  - `engine/over.go`: `foreach.over`の構文（`steps`・`perspectives`・`perspectives(from=…)`はRunner、`findings`・`<data>[]`・`<data>[<field>=<value>]`はengine）。`overSource`・`itemInput`・`overFrom`もここ
  - `engine/accumulate.go`: 累積データの判定（スキーマのトップレベル`x-masuda-accumulate: true`）、配列の検査、idで後勝ちのマージ、出力の保存（`putOutputs`。agentとexecの両方が通る）
  - `engine/foreach.go`の`dataItems`・`doneKeys`・`fieldMatches`: データを回すforeachの項目作り
- 累積データの保存の仕方: 書き込みのたびに「直前の累積値＋今回の配列」を`PutData(DataRef{name, 書いた出現})`で保存する。読み手には最新の書き手のDataRefを渡す。未書き込みなら、読む出現のDataRefに`[]`をPutDataして渡す。Runnerは累積を知らなくてよい
- 累積データの読み出しは、フレームの束縛（`with`や呼び出し時のinputs）より常にrun全体の現在値を優先する（束縛名とデータ名が同じとき）
- データを回すforeachの`Item`: キーは要素の`id`（文字列ならそのまま、それ以外はJSON表記）か、無ければ**配列全体での添字（0始まり）**。`Input`はデータ名の単数形。累積データでは、run中のどこかで同じデータを回してdoneで終わった反復のキーを`Done`にする。配列でなければrunはblocked
- fix-diffの基準スナップショットは、データを回すforeachの反復ごとに取る（従来は`over: findings`のときだけ）
- `ApprovedFiles`: 空は「何も加えない」。そのcommitのゲートに出たが承認されなかったファイルは`CommitRequest.Byproducts`に入る。run全体で承認済みのファイルは従来どおり累積して`Allowed`に入る
- 同梱の変更
  - `findings`スキーマ: `x-masuda-accumulate: true`、要素に必須の`id`（空白を含まない文字列）
  - `develop`の`fix`、`build-step`の`interim-fix`: `over: findings[autofix=true]`
  - reviewerは`id`を`<観点名>-<出現ID>-<連番>`、cross-cutting-verifierは`cross-cutting-<出現ID>-<連番>`で付ける。差し戻しのやり直しで前回の指摘を直すときは元の`id`を使う（置き換わる）
  - review-checkerは累積の全件を受け取るので、自分の観点・直前のレビューの指摘だけを検証するようプロンプトで案内した。synthesizerは全件を受け取り、重複をまとめる
- masuda側（M5〜）への取り決め
  - `Runner.Items`が呼ばれるのは`over`が`steps`・`perspectives`・`perspectives(from=…)`のときだけ。`findings`の`Items`を実装する必要はもう無い
  - `GetData`で`findings`を返すときは、engineが保存した値（累積後の配列）をそのまま返せばよい。masudaが貯める必要は無い
  - 同じ`DataRef`に2回`PutData`されうる: 累積データを読みかつ書くエージェントで、未書き込みのとき（入力の`[]`と出力）。同梱には無いが、上書きを許すこと
  - deviationゲートの`ApprovedFiles`は空のまま送ってよい（「何も加えない」）。承認されなかったファイルは`Byproducts`として渡るので、commitに含めず、そのcommitの計画外変更としても返さないこと。作業ツリーには残るので、次のcommitで再びゲートに出る
  - `/masuda/checks/test`・観点の`Items`・`over: steps`の`Item.Done`はE7のときの取り決めのまま
- 既知の制限: 累積データは要素を消せない。reviewerがやり直しで誤検知を取り下げても台帳に残る
## 契約への提案
1. （任意・急がない）累積データの要素を取り下げる手段。今はreview-checkerが`inaccurate`にしても誤検知は消えず、synthesizerが拾う。案: 同梱`findings`の要素に任意の`withdrawn: true`を足し、同じ`id`で書き直せば取り下げとする（fixの`findings[autofix=true]`からはreviewerが`autofix: false`にして外す）。スキーマの「…」の範囲で同梱だけで済むが、指示の範囲外なので入れていない
2. E7の提案3（`docs/work-orders.md`のE7のテスト実行の文面）は監督が494f76aで直したので解消
