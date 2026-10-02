package engine

import (
	"fmt"
	"sort"
	"strings"
)

// checker runs Set.Check for one root. Problems are collected, never fatal,
// so that one Check reports everything it can see.
type checker struct {
	set      *Set
	root     string
	problems []Problem
	seen     map[string]bool
}

func (c *checker) fail(path, node, format string, args ...any) {
	p := Problem{Path: path, Node: node, Message: fmt.Sprintf(format, args...)}
	key := p.Path + "\x00" + p.Node + "\x00" + p.Message
	if c.seen[key] {
		return
	}
	c.seen[key] = true
	c.problems = append(c.problems, p)
}

func (s *Set) check(root string) []Problem {
	c := &checker{set: s, root: root, seen: map[string]bool{}}
	if s.Workflows[root] == nil {
		c.fail(root, "", "no such workflow")
		return c.problems
	}
	workflows := s.reachableWorkflows(root)
	for _, path := range workflows {
		c.references(s.Workflows[path])
	}
	c.callCycles(root)
	// The rules below look through calls and roles, so they only make sense
	// once every reference resolves and calls form no cycle.
	if len(c.problems) != 0 {
		return c.problems
	}
	for _, path := range workflows {
		c.routes(s.Workflows[path])
		c.unboundedCycles(s.Workflows[path])
	}
	newFlow(c).run()
	newAvail(c).run()
	c.exports(workflows)
	if len(c.problems) == 0 {
		return nil
	}
	return c.problems
}

func (s *Set) agentFor(role string) *Agent {
	name, ok := strings.CutPrefix(role, "agents/")
	if !ok {
		return nil
	}
	return s.Agents[name]
}

func callee(n *Node) string {
	switch n.Type {
	case NodeWorkflow:
		return n.Workflow
	case NodeForeach:
		return n.Body
	}
	return ""
}

func effectiveMax(n *Node) int {
	if n.Max > 0 {
		return n.Max
	}
	if n.Type == NodeAgent || n.Type == NodeExec {
		return DefaultMax
	}
	return 0
}

// reachableWorkflows lists root and every workflow it calls, transitively,
// in a stable order (root first, then sorted). Missing callees are skipped.
func (s *Set) reachableWorkflows(root string) []string {
	seen := map[string]bool{}
	var walk func(string)
	walk = func(path string) {
		w := s.Workflows[path]
		if w == nil || seen[path] {
			return
		}
		seen[path] = true
		for _, id := range w.Order {
			if c := callee(w.Nodes[id]); c != "" {
				walk(c)
			}
		}
	}
	walk(root)
	out := make([]string, 0, len(seen))
	for p := range seen {
		if p != root {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return append([]string{root}, out...)
}

// references checks that every role and workflow a node names exists.
func (c *checker) references(w *Workflow) {
	for _, id := range w.Order {
		n := w.Nodes[id]
		if n.Role != "" && c.set.agentFor(n.Role) == nil {
			c.fail(w.Path, id, "role %s: no such agent", n.Role)
		}
		if cl := callee(n); cl != "" && c.set.Workflows[cl] == nil {
			c.fail(w.Path, id, "%s: no such workflow", cl)
		}
	}
}

// callCycles rejects workflows that call themselves directly or through
// others: the flow analyses summarise a callee before its caller and need the
// call graph to be acyclic.
func (c *checker) callCycles(root string) {
	const (
		white = iota
		grey
		black
	)
	color := map[string]int{}
	var stack []string
	var visit func(path string)
	visit = func(path string) {
		color[path] = grey
		stack = append(stack, path)
		w := c.set.Workflows[path]
		for _, id := range w.Order {
			cl := callee(w.Nodes[id])
			if cl == "" || c.set.Workflows[cl] == nil {
				continue
			}
			switch color[cl] {
			case grey:
				i := indexOf(stack, cl)
				c.fail(path, id, "workflow calls form a cycle: %s", strings.Join(append(append([]string{}, stack[i:]...), cl), " → "))
			case white:
				visit(cl)
			}
		}
		stack = stack[:len(stack)-1]
		color[path] = black
	}
	visit(root)
}

// endOutcomes is what a workflow can finish with: done for each `end`, the
// label for each `end:<label>`.
func (s *Set) endOutcomes(path string) map[string]bool {
	out := map[string]bool{}
	w := s.Workflows[path]
	if w == nil {
		return out
	}
	for _, n := range w.Nodes {
		for _, t := range n.Next {
			if t.End {
				out[endOutcome(t)] = true
			}
		}
	}
	return out
}

func endOutcome(t Target) string {
	if t.Label == "" {
		return OutcomeDone
	}
	return t.Label
}

// outcomes is what a node can produce and therefore must route. exhausted is
// left out: unrouted, it stops the run as blocked, which the schema allows.
func (s *Set) outcomes(n *Node) map[string]bool {
	set := func(names ...string) map[string]bool {
		m := map[string]bool{}
		for _, x := range names {
			m[x] = true
		}
		return m
	}
	switch n.Type {
	case NodeAgent:
		out := map[string]bool{}
		if a := s.agentFor(n.Role); a != nil {
			for o := range a.Outcomes {
				out[o] = true
			}
		}
		return out
	case NodeExec:
		return set(OutcomeDone, OutcomeFailed)
	case NodeApproval:
		return set(OutcomeApproved, OutcomeRejected)
	case NodeQuestion:
		return set(OutcomeAnswered)
	case NodeForeach:
		if n.OnIncomplete == "continue" {
			return set(OutcomeDone, OutcomeIncomplete)
		}
		out := s.endOutcomes(n.Body)
		out[OutcomeDone] = true
		return out
	case NodeWorkflow:
		return s.endOutcomes(n.Workflow)
	case NodeCommit:
		return set(OutcomeDone, OutcomeRejected)
	default: // publish, discard
		return set(OutcomeDone)
	}
}

// optionalOutcomes may be routed but need not be.
func optionalOutcomes(n *Node) map[string]bool {
	out := map[string]bool{}
	if effectiveMax(n) > 0 {
		out[OutcomeExhausted] = true
	}
	// api.go says incomplete comes from on_incomplete: continue, yet the
	// schema lists it for every foreach and the contract fixtures route it
	// under the default stop. Accepting the route without requiring it keeps
	// both readings valid until the contract settles which one holds.
	if n.Type == NodeForeach {
		out[OutcomeIncomplete] = true
	}
	return out
}

// routes checks next against what each node can produce, plus the per-node
// rules that need other files (gate names, with, from=).
func (c *checker) routes(w *Workflow) {
	for _, id := range w.Order {
		n := w.Nodes[id]
		fail := func(format string, args ...any) { c.fail(w.Path, id, format, args...) }
		outs := c.set.outcomes(n)
		opt := optionalOutcomes(n)
		for _, o := range sortedKeys(n.Next) {
			switch {
			case o == OutcomeBlocked:
				fail("next.%s: blocked is not an outcome; it means the run stops", o)
			case o == OutcomeExhausted && !opt[o]:
				fail("next.exhausted: this node has no max, so it never ends exhausted")
			case !outs[o] && !opt[o]:
				fail("next.%s: this node never produces %q (it can produce %s)", o, o, list(outs))
			}
		}
		for _, o := range sortedKeys(outs) {
			if _, ok := n.Next[o]; !ok {
				fail("outcome %q has no destination in next", o)
			}
		}
		if n.Type == NodeApproval && (n.Gate == GateTriage || n.Gate == GateDeviation) {
			fail("gate name %q is reserved for the engine", n.Gate)
		}
		if cl := callee(n); cl != "" {
			declared := map[string]bool{}
			for _, in := range c.set.Workflows[cl].Inputs {
				declared[in] = true
			}
			for _, k := range sortedKeys(n.With) {
				if !declared[k] {
					fail("with.%s: %s declares no input %q", k, cl, k)
				}
			}
		}
		if from := overFrom(n.Over); from != "" {
			if src := w.Nodes[from]; !c.writes(src, dataSelectedPerspectives) {
				fail("over: %s: node %s does not write %q", n.Over, from, dataSelectedPerspectives)
			}
		}
	}
}

// unboundedCycles requires every cycle within a workflow to pass an approval
// or a node with an entry limit, so nothing loops forever unattended.
func (c *checker) unboundedCycles(w *Workflow) {
	bounded := func(n *Node) bool { return n.Type == NodeApproval || effectiveMax(n) > 0 }
	const (
		white = iota
		grey
		black
	)
	color := map[string]int{}
	var stack []string
	var visit func(id string)
	visit = func(id string) {
		color[id] = grey
		stack = append(stack, id)
		n := w.Nodes[id]
		for _, o := range sortedKeys(n.Next) {
			t := n.Next[o]
			if t.End || bounded(w.Nodes[t.Node]) {
				continue
			}
			switch color[t.Node] {
			case grey:
				i := indexOf(stack, t.Node)
				c.fail(w.Path, t.Node, "cycle %s has no approval and no node with max; it could loop forever", strings.Join(append(append([]string{}, stack[i:]...), t.Node), " → "))
			case white:
				visit(t.Node)
			}
		}
		stack = stack[:len(stack)-1]
		color[id] = black
	}
	for _, id := range w.Order {
		if !bounded(w.Nodes[id]) && color[id] == white {
			visit(id)
		}
	}
}

// exports catches export names nothing on the way could ever write. Whether
// the value exists on a particular path is not required: export writes out
// what there is.
func (c *checker) exports(workflows []string) {
	producible := map[string]bool{}
	for name := range engineData {
		producible[name] = true
	}
	for _, in := range c.set.Workflows[c.root].Inputs {
		producible[in] = true
	}
	for name := range c.set.Schemas {
		if c.set.accumulates(name) {
			producible[name] = true
		}
	}
	for _, path := range workflows {
		w := c.set.Workflows[path]
		for _, id := range w.Order {
			for _, o := range c.nodeOutputs(w.Nodes[id]) {
				producible[o] = true
			}
		}
	}
	for _, path := range workflows {
		w := c.set.Workflows[path]
		for _, id := range w.Order {
			for _, e := range w.Nodes[id].Export {
				if !producible[e] {
					c.fail(path, id, "export %q: nothing reachable from %s writes it", e, c.root)
				}
			}
		}
	}
}

const (
	dataPlan                 = "plan"
	dataSelectedPerspectives = "selected-perspectives"
)

// nodeOutputs is every data name a node writes when it ends done (answered
// for a question).
func (c *checker) nodeOutputs(n *Node) []string {
	out := append([]string{}, n.Outputs...)
	if n.Type == NodeAgent {
		if a := c.set.agentFor(n.Role); a != nil {
			out = append(out, a.Outputs...)
		}
	}
	return out
}

// nodeInputs is every data name a node reads besides its workflow's inputs.
func (c *checker) nodeInputs(n *Node) []string {
	in := append([]string{}, n.Inputs...)
	if n.Role != "" {
		if a := c.set.agentFor(n.Role); a != nil {
			in = append(in, a.Inputs...)
		}
	}
	return in
}

func (c *checker) writes(n *Node, name string) bool {
	if n == nil {
		return false
	}
	for _, o := range c.nodeOutputs(n) {
		if o == name {
			return true
		}
	}
	return false
}

// writeCapable reports whether a node runs an agent that can change the
// worktree.
func (c *checker) writeCapable(n *Node) bool {
	if n.Role == "" {
		return false
	}
	a := c.set.agentFor(n.Role)
	return a != nil && a.WriteCapable()
}

// singular turns a data name into the input name of one of its items
// ("candidates" → "candidate"). English plurals are irregular; this covers
// the regular -ies / -s forms and leaves anything else as it is, which the
// body can still bind through with.
func singular(name string) string {
	switch {
	case strings.HasSuffix(name, "ies") && len(name) > 3:
		return strings.TrimSuffix(name, "ies") + "y"
	case strings.HasSuffix(name, "s") && !strings.HasSuffix(name, "ss") && len(name) > 1:
		return strings.TrimSuffix(name, "s")
	}
	return name
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return 0
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func list(m map[string]bool) string {
	if len(m) == 0 {
		return "nothing"
	}
	return "[" + strings.Join(sortedKeys(m), ", ") + "]"
}
