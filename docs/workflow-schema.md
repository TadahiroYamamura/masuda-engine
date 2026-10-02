# ワークフロー定義のスキーマ（v1）

この文書は契約の一部。実装者は変えない。変更の提案は`HANDOFF.md`へ。

## ファイルと参照

- ワークフロー: `workflows/**.yaml`、エージェント: `agents/**.md`、スキーマ: `schemas/<data>.json`
- 参照は`.masuda/`からの拡張子なしのパス（`workflows/develop`、`agents/planner`）
- 同じパスが対象リポジトリにあれば同梱を丸ごと置き換える

## ワークフローファイル

```yaml
version: 1
inputs: [instructions]          # 受け取るデータ名（省略可）
start: investigate
nodes:
  investigate:
    type: agent
    role: agents/investigator
    outputs: [investigation]
    egress: [docs.example.com]   # このノードの間だけ許可するホスト（省略可）
    next: plan
  plan:
    type: agent
    role: agents/planner
    inputs: [investigation]
    outputs: [plan]
    max: 4
    next:
      done: approve-plan
      needs_more_investigation: investigate
      out_of_scope: end:out_of_scope
  approve-plan:
    type: approval
    gate: plan
    target: plan
    next: {approved: implement, rejected: plan}
  fetch-task:
    type: exec
    command: ["/usr/bin/python3", "/workspace/scripts/fetch-task.py"]
    outputs: [task]
    egress: [api.linear.app]
    secrets: [LINEAR_API_KEY]
    timeout: 5m
    next: {done: investigate, failed: end:fetch_failed}
  ask:
    type: question
    questions:
      - {id: scope, text: "この依頼はこのまま1タスクとして進めてよいですか", options: [yes, split]}
    outputs: [answers]
    next: {answered: plan}
```

### 共通

- ノード名は`[a-z0-9-]+`。`end`は予約
- `next`はoutcome→行き先。文字列1つは`{done: x}`の省略形。行き先はノード名、`end`、`end:<ラベル>`
- `max`は進入回数の上限。`agent`と`exec`は省略時3。上限で`exhausted`。`exhausted`に行き先が無ければ実行はblockedで止まる
- `inputs`/`outputs`はデータ名の配列。データ名は`[a-z][a-z0-9-]*`

### 種類ごとのキー

| type | 必須 | 任意 | outcome |
|---|---|---|---|
| `agent` | `role` | `max`、`inputs`、`outputs`、`egress`、`secrets` | エージェント定義が宣言したもの + `exhausted` |
| `exec` | `command`（絶対パスのargv配列） | `inputs`、`outputs`、`max`、`egress`、`secrets`、`timeout`（Go duration） | `done`（exit 0）、`failed`、`exhausted` |
| `approval` | `gate`、`target` | — | `approved`、`rejected` |
| `question` | `role`または`questions`、`outputs`（1つ） | — | `answered` |
| `foreach` | `over`、`body` | `on_incomplete`、`with`、`max` | `done`、bodyの終わり方（`stop`のとき）、`incomplete`（`continue`のときだけ） |
| `workflow` | `workflow` | `with`、`max` | 呼んだワークフローの終わり方 |
| `commit` | `scope`（`step`/`plan`） | — | `done`、`rejected` |
| `publish` | — | `target`（`local`既定/`remote`）、`export` | `done` |
| `discard` | — | `export` | `done` |

- `approval.target`は`plan`、`diff`、または任意のデータ名。承認はその内容のハッシュに結びつく
- `foreach.over`は次のどれか。各項目は`step`・`perspective`・またはデータ名の単数形（`findings`→`finding`）で`body`の入力になる
  - `steps`（承認済み計画のステップ）、`perspectives`（全観点）、`perspectives(from=<node>)`（そのノードが`selected-perspectives`に選んだ観点）: **Runnerが項目を返す**（`Runner.Items`）
  - `<データ名>[]`（JSON配列のデータ）: **エンジンが項目を作る**。配列の各要素が項目で、キーは要素の`id`フィールド（無ければ添字）。任意のフィルタ`<データ名>[<field>=<value>]`で要素を絞れる（例: `findings[autofix=true]`。値は`true`/`false`/数値/文字列の等価比較のみ）。累積データ（下記）では、以前の反復が`done`で終わった要素（同じキー）は飛ばす
  - `findings`は`findings[]`の省略形
- `with`は呼び出し先の入力名にデータ名を結びつける。書かなければ同名のデータを渡す
- `question.questions`は固定の質問。エンジンが`Runner.OpenQuestion`で開き、`Engine.Answer`で答えが来たら`outputs`の1つめのデータ名にJSON（id→answer）で保存して`answered`で遷移する
- `question.role`を書くと、エージェントがタスクとして質問を組み立て、`ask_human`（ホストが`Engine.Answer`へ仲介する）で人間に聞く。1タスク中に複数回聞いてよく、エンジンは答えをその出現に溜める。エージェントが`done`で報告したとき、溜まった答え（id→answer、後勝ち）を`outputs`の1つめに保存し、ノードは`answered`で遷移する（`done`は`answered`に読み替える）。答えが1つも無いまま`done`なら無効な結果として差し戻す。エージェント定義の`outcomes`は`done`のみでよい
- ゲート名`triage`・`deviation`は予約

### スナップショットと計画外変更の検出の基準点

エンジンは`agent`・`exec`の出現が終わるたびに`Runner.Snapshot`を取る。

- **書き込めないエージェント**は、進入時と終了時の`ChangedSince(基準)`の結果（ハッシュ）を比べ、違っていれば`deviation`ゲートを開く。基準はフレーム内の直前の境界のスナップショットで、無ければ進入時に取る
- **`commit`の計画外変更の検出**は、runで最後に成功した`commit`より後の最初の`agent`/`exec`の基準スナップショット（実質的に`HEAD`の作業ツリー）からの`ChangedSince`で見る。直前の境界を使うと実装直後の状態が基準になり、変更が常に空に見えるため
- `deviation`ゲートで承認された直後の`commit`は、作業ツリーを見直さずに`Runner.Commit`へ進む。見直すと人間が見ていない変更について聞き直すことになる。`Allowed`外の変更はホストが`Deviations`で返せば次のゲートが開く
- `ApprovedFiles`はrun全体で累積する

### 読み込み時の検査（Set.Check）

`Check`は**rootとして始めるワークフロー**に対して行う。他のワークフローから呼ばれる部品（`workflow`・`foreach`の`body`）は、呼び出し元の検査の中で、呼び出し時点の状態（承認済み計画の有無など）を前提に検査される。部品を単独でrootとして検査すると「承認前の書き込み」で拒否されうるが、それは誤りではない。rootの一覧は「どのワークフローの`Reachable`にも含まれないワークフロー」。

- 参照先が存在し、呼び出しが循環しない
- 各ノードが出しうるoutcomeすべてに行き先がある。出さないoutcomeへの行き先も誤り
- 承認ノードも`max`付きノードも含まない閉路は拒否する
- 入力・`with`・`inputs`が、そのノードに至るどの経路でも用意済みであること
- 承認済み計画が無い状態で、書き込めるエージェントや`commit`に到達しない
- 未コミットの変更がある状態で`publish`に到達しない
- `egress`・`secrets`は宣言の形だけ検査する（許可されているかはmasudaが実行時に見る）

## エージェント定義

```markdown
---
name: planner
description: 調査結果から変更方針とステップ分解を計画として書く
tools: Read, Grep, Glob, Bash
inputs: [investigation]
outputs: [plan]
outcomes:
  done: 変更方針とステップ分解が書けた
  needs_more_investigation: 調査結果だけでは判断できない疑問が残っている
  out_of_scope: 依頼が1タスクの範囲を超えている
---
（役割のプロンプト本文）
```

- `outcomes`は必須で`done`を含む。`exhausted`・`blocked`・`failed`は宣言できない
- `tools`は配列かカンマ区切り。省略は全ツール。`Write`か`Edit`を持つ（または省略）エージェントが「書き込める」
- `outputs`に`diff`・`step-diff`・`fix-diff`は名乗れない（エンジンが用意する）

## データとスキーマ

### 累積データ

スキーマのトップレベルに`"x-masuda-accumulate": true`を持つデータは**累積データ**で、普通のデータ（最後に書かれた値だけが見える）と違い、runの中で書かれた値を1つの配列に集める。

- 書き込みは配列でなければならず、要素は`id`フィールドがあればそれで重複を排除する（後勝ち）。無ければ常に追加
- 読み出しは、その時点までに受け付けた全書き込みを連結した配列。1度も書かれていなければ`[]`。したがって累積データは**どの経路でも常に用意済み**として扱い、`Set.Check`のデータ可用性の解析で欠落にしない
- 累積データを`inputs`に取るエージェント・execは、runのそれまでの全要素を受け取る（例: synthesizerは全観点と横断チェックの指摘をまとめて読む）
- 同梱の`findings`は累積データ。旧設計の「指摘の台帳」に当たる

- `schemas/<data>.json`があるデータは、受け取る境界でJSON Schema（draft 2020-12）で検証する
- 同梱スキーマ: `plan`（summary、steps[]、expected_byproducts[]）、`findings`（累積。要素は`id`、file、line、severity、autofix、message…）、`commit-message`、`selected-perspectives`、`answers`
- スキーマのトップレベルが`string`型なら、出力ファイルの内容そのものを1つの文字列として検証する（JSONとしてパースしない）。`commit-message`がこれに当たる
- スキーマが無いデータは空でないことだけ確かめる
- エンジンが用意するデータ: `diff`、`step-diff`、`fix-diff`（unified diff）
