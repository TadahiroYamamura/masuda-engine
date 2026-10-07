package engine

import (
	"fmt"
	"strings"
)

// state is what the state-space check tracks besides the current node. The
// space is node × 4 and each (node, state) pair is visited once, so the search
// ends even when a workflow loops.
type state struct {
	dirty bool // uncommitted changes may exist
	plan  bool // an approved plan exists
}

// summary is what a workflow does when entered in one state: per end
// outcome, the states it can finish in.
type summary struct {
	exits map[string]map[state]bool
}

type flowKey struct {
	path  string
	entry state
}

type flow struct {
	c         *checker
	summaries map[flowKey]*summary
}

func newFlow(c *checker) *flow { return &flow{c: c, summaries: map[flowKey]*summary{}} }

// run explores root from a fresh workspace: no approved plan, no changes.
func (f *flow) run() { f.summarize(f.c.root, state{}) }

type visit struct {
	node string
	st   state
}

// summarize explores one workflow breadth-first over (node, state) and
// reports, at the node, every rule broken in some reachable state together
// with the path that led there. Callees are summarised per entry state and
// memoised; the call graph is acyclic (checked before), so this recursion
// ends.
func (f *flow) summarize(path string, entry state) *summary {
	key := flowKey{path, entry}
	if s, ok := f.summaries[key]; ok {
		return s
	}
	s := &summary{exits: map[string]map[state]bool{}}
	f.summaries[key] = s
	w := f.c.set.Workflows[path]

	parent := map[visit]visit{}
	trail := func(v visit) string {
		var out []string
		for {
			out = append(out, v.node)
			p, ok := parent[v]
			if !ok {
				break
			}
			v = p
		}
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
		return strings.Join(out, " → ")
	}

	start := visit{w.Start, entry}
	seen := map[visit]bool{start: true}
	queue := []visit{start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		n := w.Nodes[cur.node]
		violate := func(format string, args ...any) {
			msg := fmt.Sprintf(format, args...)
			if t := trail(cur); strings.Contains(t, "→") {
				msg += fmt.Sprintf(" (path in %s: %s)", path, t)
			}
			f.c.fail(path, cur.node, "%s", msg)
		}
		exits := f.step(n, cur.st, violate)
		for _, outcome := range sortedKeys(exits) {
			t, routed := n.Next[outcome]
			if !routed {
				continue // unrouted exhausted: the run stops as blocked
			}
			for st := range exits[outcome] {
				if t.End {
					o := endOutcome(t)
					if s.exits[o] == nil {
						s.exits[o] = map[state]bool{}
					}
					s.exits[o][st] = true
					continue
				}
				nv := visit{t.Node, st}
				if !seen[nv] {
					seen[nv] = true
					parent[nv] = cur
					queue = append(queue, nv)
				}
			}
		}
	}
	return s
}

// step returns, per outcome, the states node n can leave in when entered in
// st, and reports the rules the node itself breaks in st.
func (f *flow) step(n *Node, st state, violate func(string, ...any)) map[string]map[state]bool {
	out := map[string]map[state]bool{}
	add := func(outcome string, sts ...state) {
		if out[outcome] == nil {
			out[outcome] = map[state]bool{}
		}
		for _, x := range sts {
			out[outcome][x] = true
		}
	}
	// Over its entry limit a node does not run, so exhausted leaves the
	// state as it was.
	if effectiveMax(n) > 0 {
		add(OutcomeExhausted, st)
	}
	// A new plan voids the approval of the old one. The engine stores outputs
	// only on done, but treating every outcome as voiding keeps the check on
	// the safe side.
	after := st
	if f.c.writes(n, dataPlan) {
		after.plan = false
	}
	switch n.Type {
	case NodeAgent, NodeQuestion:
		if f.c.writeCapable(n) {
			if !st.plan {
				violate("write-capable agent %s can run without an approved plan; put an approval with target: plan before it", n.Role)
			}
			after.dirty = true
		}
		for o := range f.c.set.outcomes(n) {
			add(o, after)
		}
	case NodeExec:
		add(OutcomeDone, after)
		add(OutcomeFailed, after)
	case NodePrivileged:
		// The host runs a privileged command apart from the worktree (masuda:
		// in a VM of its own, on a snapshot), so it cannot write before the
		// plan is approved nor leave uncommitted changes: the state passes
		// through unchanged.
		add(OutcomeDone, st)
		add(OutcomeFailed, st)
	case NodeApproval:
		approved := st
		if n.Target == dataPlan {
			approved.plan = true
		}
		add(OutcomeApproved, approved)
		add(OutcomeRejected, st)
	case NodeCommit:
		if !st.plan {
			violate("commit can be reached without an approved plan, which decides what it commits")
		}
		clean := st
		clean.dirty = false
		add(OutcomeDone, clean)
		add(OutcomeRejected, st)
	case NodePublish:
		if st.dirty {
			violate("publish can be reached with uncommitted changes; put a commit before it")
		}
		add(OutcomeDone, st)
	case NodeDiscard:
		add(OutcomeDone, st)
	case NodeForeach:
		if n.Over == "steps" && !st.plan {
			violate("over: steps can be reached without an approved plan")
		}
		f.foreach(n, st, add)
	case NodeWorkflow:
		sub := f.summarize(n.Workflow, st)
		for o, sts := range sub.exits {
			for x := range sts {
				add(o, x)
			}
		}
	}
	return out
}

// foreach runs the body zero or more times. Every state the loop head can
// reach is a state the foreach can finish in, because the items may run out
// at any point.
func (f *flow) foreach(n *Node, st state, add func(string, ...state)) {
	head := map[state]bool{st: true}
	queue := []state{st}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		sub := f.summarize(n.Body, cur)
		for o, sts := range sub.exits {
			for x := range sts {
				if o != OutcomeDone && n.OnIncomplete != "continue" {
					add(o, x)
					continue
				}
				if !head[x] {
					head[x] = true
					queue = append(queue, x)
				}
			}
		}
	}
	for x := range head {
		add(OutcomeDone, x)
		if n.OnIncomplete == "continue" {
			add(OutcomeIncomplete, x)
		}
	}
}
