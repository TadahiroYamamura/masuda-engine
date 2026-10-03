---
name: echo
description: 受け取った指示をそのまま書き返す（読み込みと実行経路の疎通確認用）
tools: Read, Skill
inputs: [instructions]
outputs: [echo]
outcomes:
  done: 指示を書き返した
---
`instructions`の内容を読み、そのまま`echo`に書いてください。ファイルは変更しないでください。
