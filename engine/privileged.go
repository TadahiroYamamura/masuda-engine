package engine

import "fmt"

// privileged runs a privileged node within Advance. Like exec, nothing is
// recorded until the command has returned, so a crash in between runs it
// again on the next Advance; a privileged node is meant to be safe to repeat.
// An error from the Runner (the name is not declared or not approved, the
// host could not run it) is returned as it is, as exec does: the run stops
// without a result, and once a human has fixed the cause the next Advance
// tries the node again.
//
// Unlike exec it neither sets the policy nor takes a snapshot. The command's
// own policy is the host's declaration and approval, and nothing runs in the
// main sandbox meanwhile (as with commit or an approval gate), so the main
// sandbox keeps the policy it has. The host runs the command apart from the
// worktree, so its end is no boundary of changes: the worktree is as the
// previous boundary left it, and the deviation checks keep that one as their
// base.
func (m *mover) privileged(cur *occurrence, n *Node) (Status, bool, error) {
	if err := m.need(); err != nil {
		return Status{}, false, err
	}
	out, err := m.e.runner.RunPrivileged(m.ctx, PrivilegedTask{
		Run: m.run, Occurrence: cur.ID, Node: n.ID, Name: n.PrivilegedName,
	})
	if err != nil {
		return Status{}, false, err
	}
	res := result{Outcome: OutcomeDone}
	if out.TimedOut || out.ExitCode != 0 {
		res = result{Outcome: OutcomeFailed, Feedback: fmt.Sprintf("特権コマンド%q: ", n.PrivilegedName) + commandFailure(out)}
	}
	// Not applied means a concurrent Advance ran the command too and recorded
	// first; its result stands.
	_, err = m.e.putResult(m.run, cur, res, "")
	return Status{}, true, err
}
