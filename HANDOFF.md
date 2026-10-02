# HANDOFF
## 作業項目
E1（定義の読み込み）完了。`engine.Load`・`engine.Bundled`・`Agent.WriteCapable`、同梱スキーマ`schemas/{plan,findings,commit-message,selected-perspectives,answers}.json`、最小の同梱`workflows/smoke`・`agents/echo`
## 完了した契約テスト
C-E1（`go test ./contract/ -run TestCE1` 緑）。C-E2以降の全テストの定義ファイルもLoadは通る（各テストは`Check`等のスタブでpanicして止まる）
## 未完と理由
なし（E1の範囲内）。E2以降は範囲外のため未着手
## 次の一手
E2（`Set.Check`・`Reachable`・`Mermaid`）。`docs/work-orders.md`のE2を読む
## 注意点
- 実装の場所: `engine/load.go`（収集と上書き）、`parse_workflow.go`、`parse_agent.go`、`schema.go`、`yamlutil.go`、`bundled.go`。同梱は`go:embed`の制約で`engine/defaults/`にある
- Loadで済ませた検査: 種類ごとのキー・必須キー、`next`の形、`max>=1`、データ名、`timeout`、`command[0]`が絶対パス、egress/secretsの形、ファイル内の参照（`start`・`next`の行き先・`perspectives(from=<node>)`のノード）。E2に残したもの: role/workflow/bodyの参照先の存在、outcomeと`next`の対応、予約ゲート名（C-E2が「Loadは通る」前提）、その他「読み込み時の検査」全部
- `Set.Agents`のキーは`agents/`を除いた参照パス（例`planner`）。frontmatterの`name`はこれと一致必須
- `end:<label>`に`done`・`exhausted`・`blocked`・`failed`は使えない。エージェントが宣言できないoutcomeは`exhausted`・`blocked`・`failed`
- `foreach.on_incomplete`は省略時`stop`、`publish.target`は省略時`local`を埋めてある。`Node.Max`は省略時0のまま（既定値の解釈はエンジン側）
- `commit-message`は平文（契約テストでは`"feat: flag"`）。スキーマは`type: string`。E3で検証する際、トップレベルが`type: string`のスキーマなら内容をJSON文字列として検証する、などの取り決めが要る。他のスキーマ付きデータは`jsonschema.UnmarshalJSON`で読んで検証すること（`engine/load_test.go`の`TestBundledSchemas`が例）
- findingsのフィールドは契約の列挙に合わせ`file`・`line`・`severity`（高/中/低）・`autofix`・`message`が必須、`end_line`・`suggestion`は任意。`line<=end_line`はスキーマで表せず未検査
- 依存: `go.yaml.in/yaml/v3`、`github.com/santhosh-tekuri/jsonschema/v6`
## 契約への提案
なし
