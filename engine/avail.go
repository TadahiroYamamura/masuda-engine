package engine

import (
	"sort"
	"strings"
)

// avail is the data-availability check: every input a node or callee reads
// must be available on every path to it. It is a must-analysis: where paths
// merge, only data available on all of them survives.
type avail struct {
	c    *checker
	memo map[string]map[string]map[string]bool
}

func newAvail(c *checker) *avail {
	return &avail{c: c, memo: map[string]map[string]map[string]bool{}}
}

func (a *avail) run() {
	root := a.c.set.Workflows[a.c.root]
	entry := map[string]bool{}
	for _, in := range root.Inputs {
		entry[in] = true
	}
	a.analyse(root, entry, true)
}

// analyse runs w entered with entry available and returns, per end outcome,
// what is available there. With report set, it also reports missing data in
// w and, through calls, in everything it reaches.
func (a *avail) analyse(w *Workflow, entry map[string]bool, report bool) map[string]map[string]bool {
	key := w.Path + "\x00" + setKey(entry)
	if !report {
		if r, ok := a.memo[key]; ok {
			return r
		}
	}
	in := map[string]map[string]bool{w.Start: copySet(entry)}
	ends := map[string]map[string]bool{}
	for changed := true; changed; {
		changed = false
		for _, id := range w.Order {
			have := in[id]
			if have == nil {
				continue
			}
			n := w.Nodes[id]
			after := a.effect(w, n, have, false)
			for _, o := range sortedKeys(n.Next) {
				t := n.Next[o]
				got := after[o]
				if got == nil {
					got = have // an outcome the node does not produce adds nothing
				}
				if t.End {
					eo := endOutcome(t)
					ends[eo] = meet(ends[eo], got)
					continue
				}
				merged := meet(in[t.Node], got)
				if in[t.Node] == nil || len(merged) != len(in[t.Node]) {
					in[t.Node] = merged
					changed = true
				}
			}
		}
	}
	if report {
		for _, id := range w.Order {
			if have := in[id]; have != nil {
				a.effect(w, w.Nodes[id], have, true)
			}
		}
	}
	a.memo[key] = ends
	return ends
}

// effect returns, per outcome, the data available after node nd finishes,
// given have before it. With report set, it reports what nd needs but have
// lacks.
func (a *avail) effect(w *Workflow, nd *Node, have map[string]bool, report bool) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	available := func(name string) bool { return have[name] || a.ready(name) }
	need := func(name, format string, args ...any) {
		if report && !available(name) {
			a.c.fail(w.Path, nd.ID, format, args...)
		}
	}
	withOutputs := func(outs []string) map[string]bool {
		m := copySet(have)
		for _, o := range outs {
			m[o] = true
		}
		return m
	}
	switch nd.Type {
	case NodeAgent, NodeExec, NodeQuestion:
		for _, in := range a.c.nodeInputs(nd) {
			need(in, "reads %q, which is not available on every path to this node", in)
		}
		done := OutcomeDone
		if nd.Type == NodeQuestion {
			done = OutcomeAnswered
		}
		out[done] = withOutputs(a.c.nodeOutputs(nd))
	case NodeApproval:
		if nd.Target != string(DiffFromBase) && nd.Target != string(DiffFromHead) {
			need(nd.Target, "target %q is not plan, diff, step-diff, or data available on every path to this node", nd.Target)
		}
	case NodeCommit:
		if nd.Scope == "step" {
			need("step", "scope: step needs the input %q, which is not available on every path to this node (use it in the body of a foreach over steps)", "step")
		}
	case NodeForeach:
		if src := overSource(nd.Over); src != "" {
			need(src, "over: %s reads %q, which is not available on every path to this node", nd.Over, src)
		}
		body := a.c.set.Workflows[nd.Body]
		entry := a.bind(w, nd, body, have, itemInput(nd.Over), report)
		if report {
			a.analyse(body, entry, true)
		}
		// The body may run zero times, so nothing it writes is guaranteed
		// afterwards.
	case NodeWorkflow:
		cw := a.c.set.Workflows[nd.Workflow]
		entry := a.bind(w, nd, cw, have, "", report)
		for o, got := range a.analyse(cw, entry, report) {
			m := copySet(have)
			for d := range got {
				m[d] = true
			}
			out[o] = m
		}
	}
	return out
}

// bind resolves each input the callee declares to a data name (its own name
// unless `with` binds it elsewhere), reports those not available, and returns
// the callee's entry set. Data resolve by name across the whole run (the
// latest value wins), so what the caller has is visible to the callee too.
func (a *avail) bind(w *Workflow, nd *Node, cw *Workflow, have map[string]bool, item string, report bool) map[string]bool {
	entry := copySet(have)
	if item != "" {
		entry[item] = true
	}
	for _, in := range cw.Inputs {
		src := in
		if b, ok := nd.With[in]; ok {
			src = b
		}
		if report && !have[src] && !a.ready(src) && src != item {
			if src == in {
				a.c.fail(w.Path, nd.ID, "%s needs the input %q, which is not available on every path to this node", cw.Path, in)
			} else {
				a.c.fail(w.Path, nd.ID, "%s needs the input %q (bound to %q), which is not available on every path to this node", cw.Path, in, src)
			}
		}
		entry[in] = true
	}
	return entry
}

// ready is data available on every path regardless of what ran: what the
// engine computes, and accumulated data (an empty array until written).
func (a *avail) ready(name string) bool {
	return engineData[name] || a.c.set.accumulates(name)
}

// meet intersects two availability sets; nil stands for "not reached yet"
// and is the identity.
func meet(x, y map[string]bool) map[string]bool {
	if x == nil {
		return copySet(y)
	}
	out := map[string]bool{}
	for k := range x {
		if y[k] {
			out[k] = true
		}
	}
	return out
}

func copySet(m map[string]bool) map[string]bool {
	out := make(map[string]bool, len(m))
	for k, v := range m {
		if v {
			out[k] = true
		}
	}
	return out
}

func setKey(m map[string]bool) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}
