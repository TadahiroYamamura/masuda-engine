package engine

import (
	"fmt"
	"strings"
)

// exec runs an exec node within Advance: policy, command, outputs, snapshot,
// result. Nothing is recorded until the command has returned, so a crash in
// between runs the command again on the next Advance. Recording a "started"
// marker first was rejected: the engine could not tell a crashed command
// from a running one, and an exec node is meant to be safe to repeat.
func (m *mover) exec(fr *frame, cur *occurrence, n *Node) (Status, bool, error) {
	if err := m.need(); err != nil {
		return Status{}, false, err
	}
	inputs := map[string]DataRef{}
	for k, v := range fr.Inputs {
		inputs[k] = v
	}
	for _, name := range n.Inputs {
		if _, ok := inputs[name]; ok {
			continue
		}
		ref, err := m.resolve(fr, name, cur.ID)
		if err != nil {
			return Status{}, false, fmt.Errorf("%s: node %s: %w", cur.Workflow, n.ID, err)
		}
		inputs[name] = ref
	}
	p := Policy{Egress: n.Egress, Secrets: n.Secrets}
	if err := m.setPolicy(cur, p); err != nil {
		return Status{}, false, err
	}
	out, err := m.e.runner.RunCommand(m.ctx, CommandTask{
		Run: m.run, Occurrence: cur.ID, Node: n.ID, Command: n.Command,
		Inputs: inputs, Outputs: n.Outputs, Timeout: n.Timeout, Policy: p,
	})
	if err != nil {
		return Status{}, false, err
	}
	res := result{Outcome: OutcomeDone}
	if out.TimedOut || out.ExitCode != 0 {
		res = result{Outcome: OutcomeFailed, Feedback: commandFailure(out)}
	} else {
		var reasons []string
		for _, name := range n.Outputs {
			c, ok := out.Outputs[name]
			if !ok {
				reasons = append(reasons, fmt.Sprintf("宣言した出力%qが書かれていない", name))
				continue
			}
			if err := m.e.validateData(name, c); err != nil {
				reasons = append(reasons, fmt.Sprintf("出力%qが不正: %v", name, err))
			}
		}
		if len(reasons) > 0 {
			detail := strings.Join(reasons, "; ")
			m.e.log(m.run, Event{Kind: "invalid", Occurrence: cur.ID, Workflow: cur.Workflow, Node: n.ID, Outcome: OutcomeDone, Detail: detail})
			res = result{Outcome: OutcomeDone, Invalid: true, Feedback: "前回の実行の出力は受け付けられなかった: " + detail}
		} else {
			for _, name := range n.Outputs {
				if err := m.e.runner.PutData(m.ctx, m.run, DataRef{Name: name, Occurrence: cur.ID}, out.Outputs[name]); err != nil {
					return Status{}, false, err
				}
			}
			res.Outputs = n.Outputs
		}
	}
	if res.Snapshot, err = m.e.snapshot(m.ctx, m.run, cur); err != nil {
		return Status{}, false, err
	}
	// Not applied means a concurrent Advance ran the command too and recorded
	// first; its result stands.
	_, err = m.e.putResult(m.run, cur, res)
	return Status{}, true, err
}

func commandFailure(out CommandResult) string {
	head := fmt.Sprintf("コマンドが失敗した（exit %d）", out.ExitCode)
	if out.TimedOut {
		head = "コマンドがタイムアウトした"
	}
	if out.LogTail == "" {
		return head
	}
	return head + ":\n" + out.LogTail
}
