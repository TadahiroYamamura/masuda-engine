package engine

import (
	"fmt"
	"sort"
)

// reachable lists root, the workflows it calls, the agents its nodes run and
// the schemas of every data name those mention: everything a run of root
// reads from the definition set.
func (s *Set) reachable(root string) ([]string, error) {
	if s.Workflows[root] == nil {
		return nil, fmt.Errorf("%s: no such workflow", root)
	}
	out := map[string]bool{}
	data := map[string]bool{}
	addData := func(names ...string) {
		for _, d := range names {
			data[d] = true
		}
	}
	for _, path := range s.reachableWorkflows(root) {
		out[path] = true
		w := s.Workflows[path]
		addData(w.Inputs...)
		for _, id := range w.Order {
			n := w.Nodes[id]
			if cl := callee(n); cl != "" && s.Workflows[cl] == nil {
				return nil, fmt.Errorf("%s: node %s: %s: no such workflow", path, id, cl)
			}
			if n.Role != "" {
				a := s.agentFor(n.Role)
				if a == nil {
					return nil, fmt.Errorf("%s: node %s: role %s: no such agent", path, id, n.Role)
				}
				out[n.Role] = true
				addData(a.Inputs...)
				addData(a.Outputs...)
			}
			addData(n.Inputs...)
			addData(n.Outputs...)
			addData(n.Export...)
			if n.Type == NodeApproval {
				addData(n.Target)
			}
			if n.Type == NodeForeach {
				addData(overSource(n.Over), itemInput(n.Over))
			}
			for _, v := range n.With {
				addData(v)
			}
		}
	}
	for d := range data {
		if _, ok := s.Schemas[d]; ok {
			out["schemas/"+d] = true
		}
	}
	refs := make([]string, 0, len(out))
	for r := range out {
		refs = append(refs, r)
	}
	sort.Strings(refs)
	return refs, nil
}
