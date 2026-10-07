package engine

import (
	"fmt"
	"strings"
)

// mermaid draws root and every workflow it calls as one flowchart, one
// subgraph per workflow. Besides the user's nodes it draws what the engine
// always inserts, so the diagram shows what actually runs: the deviation gate
// before each commit and after each read-only agent, and triage, which can
// interrupt any node.
func (s *Set) mermaid(root string) (string, error) {
	if s.Workflows[root] == nil {
		return "", fmt.Errorf("%s: no such workflow", root)
	}
	paths := s.reachableWorkflows(root)
	prefix := map[string]string{}
	for i, p := range paths {
		prefix[p] = fmt.Sprintf("w%d_", i)
	}
	id := func(path, node string) string { return prefix[path] + strings.ReplaceAll(node, "-", "_") }

	var b strings.Builder
	fmt.Fprintf(&b, "%%%% %s\nflowchart TD\n", root)
	fmt.Fprintf(&b, "  entry((start)) --> %s\n", id(root, s.Workflows[root].Start))
	var human []string
	for _, path := range paths {
		w := s.Workflows[path]
		fmt.Fprintf(&b, "  subgraph %sgraph[%s]\n", prefix[path], quote(path))
		ends := map[string]bool{}
		for _, nid := range w.Order {
			n := w.Nodes[nid]
			fmt.Fprintf(&b, "    %s%s\n", id(path, nid), s.shape(n))
			if n.Type == NodeApproval || n.Type == NodeQuestion {
				human = append(human, id(path, nid))
			}
			for _, t := range n.Next {
				if t.End {
					ends[endOutcome(t)] = true
				}
			}
		}
		for _, o := range sortedKeys(ends) {
			label := "end"
			if o != OutcomeDone {
				label = "end:" + o
			}
			fmt.Fprintf(&b, "    %send_%s(((%s)))\n", prefix[path], strings.ReplaceAll(o, "-", "_"), quote(label))
		}
		b.WriteString("  end\n")

		for _, nid := range w.Order {
			n := w.Nodes[nid]
			from := id(path, nid)
			if n.Type == NodeCommit || (n.Type == NodeAgent && !s.agentFor(n.Role).WriteCapable()) {
				dev := from + "_deviation"
				when := "before commit"
				text := "deviation gate (engine)<br/>opens if files outside the plan changed"
				if n.Type == NodeAgent {
					when = "after run"
					text = "deviation gate (engine)<br/>opens if this read-only agent changed the tree"
				}
				fmt.Fprintf(&b, "  %s -. %s .-> %s{{%s}}\n", from, quote(when), dev, quote(text))
				human = append(human, dev)
			}
			for _, o := range sortedKeys(n.Next) {
				t := n.Next[o]
				to := id(path, t.Node)
				if t.End {
					to = prefix[path] + "end_" + strings.ReplaceAll(endOutcome(t), "-", "_")
				}
				fmt.Fprintf(&b, "  %s -->|%s| %s\n", from, quote(o), to)
			}
			if cl := callee(n); cl != "" {
				fmt.Fprintf(&b, "  %s -. %s .-> %s\n", from, quote(string(n.Type)), id(cl, s.Workflows[cl].Start))
			}
		}
	}
	b.WriteString("  triage{{\"triage gate (engine)<br/>can interrupt any node when an agent reports a concern\"}}\n")
	b.WriteString("  classDef human fill:#fde68a,stroke:#b45309\n")
	b.WriteString("  classDef engine stroke-dasharray: 4 3\n")
	for _, h := range human {
		fmt.Fprintf(&b, "  class %s human\n", h)
	}
	b.WriteString("  class triage engine\n")
	return b.String(), nil
}

func (s *Set) shape(n *Node) string {
	label := fmt.Sprintf("%s<br/>type: %s", n.ID, n.Type)
	switch n.Type {
	case NodeAgent:
		label += "<br/>" + n.Role
	case NodeExec:
		label += "<br/>" + strings.Join(n.Command, " ")
	case NodePrivileged:
		label += "<br/>name: " + n.PrivilegedName
	case NodeApproval:
		label += fmt.Sprintf("<br/>gate: %s, target: %s", n.Gate, n.Target)
		return "{" + quote(label) + "}"
	case NodeQuestion:
		if n.Role != "" {
			label += "<br/>" + n.Role
		}
		return "{" + quote(label) + "}"
	case NodeForeach:
		label += fmt.Sprintf("<br/>over: %s<br/>body: %s", n.Over, n.Body)
	case NodeWorkflow:
		label += "<br/>" + n.Workflow
	case NodeCommit:
		label += "<br/>scope: " + n.Scope
	case NodePublish:
		label += "<br/>target: " + n.PublishTarget
	}
	if m := effectiveMax(n); m > 0 {
		label += fmt.Sprintf("<br/>max: %d", m)
	}
	return "[" + quote(label) + "]"
}

func quote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, "#quot;") + `"`
}
