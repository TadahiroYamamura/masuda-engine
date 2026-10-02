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
| `foreach` | `over`、`body` | `on_incomplete`、`with`、`max` | `done`、`incomplete`、bodyの終わり方 |
| `workflow` | `workflow` | `with`、`max` | 呼んだワークフローの終わり方 |
| `commit` | `scope`（`step`/`plan`） | — | `done`、`rejected` |
| `publish` | — | `target`（`local`既定/`remote`）、`export` | `done` |
| `discard` | — | `export` | `done` |

- `approval.target`は`plan`、`diff`、または任意のデータ名。承認はその内容のハッシュに結びつく
- `foreach.over`は`steps`（承認済み計画のステップ）、`findings`（自動修正対象の指摘）、`perspectives`（全観点）、`perspectives(from=<node>)`（そのノードが`selected-perspectives`に選んだ観点）、または`<データ名>[]`（JSON配列のデータ）。各項目は`step`・`finding`・`perspective`・またはデータ名の単数形で`body`の入力になる
- `with`は呼び出し先の入力名にデータ名を結びつける。書かなければ同名のデータを渡す
- `question.questions`は固定の質問。`role`を書くとエージェントが質問を`ask_human`で出す。答えは`outputs`の1つめのデータ名で保存される（JSON、id→answer）
- ゲート名`triage`・`deviation`は予約

### 読み込み時の検査（Set.Check）

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

- `schemas/<data>.json`があるデータは、受け取る境界でJSON Schema（draft 2020-12）で検証する
- 同梱スキーマ: `plan`（summary、steps[]、expected_byproducts[]）、`findings`（file、line、severity、autofix、message…）、`commit-message`、`selected-perspectives`、`answers`
- スキーマのトップレベルが`string`型なら、出力ファイルの内容そのものを1つの文字列として検証する（JSONとしてパースしない）。`commit-message`がこれに当たる
- スキーマが無いデータは空でないことだけ確かめる
- エンジンが用意するデータ: `diff`、`step-diff`、`fix-diff`（unified diff）
