package engine

import (
	"errors"
	"fmt"
	"slices"
)

// prepareItems fixes what a foreach iterates over when it is entered, so a
// recomputed position walks the same items even if the data moves on.
func (m *mover) prepareItems(fr *frame, n *Node, occ *occurrence) error {
	var from DataRef
	src := overSource(n.Over)
	if node := overFrom(n.Over); node != "" {
		found := false
		occs := m.recs.byFrame[fr.ID]
		for i := len(occs) - 1; i >= 0 && !found; i-- {
			o := occs[i]
			if res := m.recs.results[o.ID]; o.Node == node && res != nil && slices.Contains(res.Outputs, src) {
				from, found = DataRef{Name: src, Occurrence: o.ID}, true
			}
		}
		if !found {
			return fmt.Errorf("%s: node %s: over %s: %s has not written %q", fr.Workflow, n.ID, n.Over, node, src)
		}
	} else if src != "" {
		ref, err := m.resolve(fr, src, occ.ID)
		if err != nil {
			return fmt.Errorf("%s: node %s: over %s: %w", fr.Workflow, n.ID, n.Over, err)
		}
		from = ref
	}
	items, err := m.e.runner.Items(m.ctx, m.run, n.Over, from)
	if err != nil {
		return err
	}
	occ.Items = items
	return nil
}

// foreach runs one frame per item, one after another. Each iteration's
// frame is "<occ>.<n>" (n counts every item, done or not, from 1), so the
// position is recomputed from which frames exist and how they ended.
func (m *mover) foreach(fr *frame, cur *occurrence, n *Node) (Status, bool, error) {
	callee := m.e.set.Workflows[n.Body]
	if callee == nil {
		return Status{}, false, fmt.Errorf("engine: workflow %s is not loaded", n.Body)
	}
	incomplete, first := false, true
	for i, it := range cur.Items {
		if it.Done {
			continue
		}
		fid := fmt.Sprintf("%s.%d", cur.ID, i+1)
		if b, ok, err := m.e.store.Get(runKey(m.run, prefFrameEnd+fid)); err != nil {
			return Status{}, false, err
		} else if ok {
			first = false
			if end := string(b); end != OutcomeDone {
				if n.OnIncomplete == "continue" {
					incomplete = true
					continue
				}
				return m.finish(cur, end, "", nil)
			}
			continue
		}
		if _, ok, err := m.e.store.Get(runKey(m.run, prefFrame+fid)); err != nil {
			return Status{}, false, err
		} else if ok {
			return m.step(fid)
		}
		if err := m.need(); err != nil {
			return Status{}, false, err
		}
		input := it.Input
		if input == "" {
			input = itemInput(n.Over)
		}
		ref := DataRef{Name: input, Occurrence: fid}
		if err := m.e.runner.PutData(m.ctx, m.run, ref, it.Content); err != nil {
			return Status{}, false, err
		}
		cf := frame{ID: fid, Workflow: callee.Path, Inputs: map[string]DataRef{input: ref},
			Parent: fr.ID, Over: n.Over, Item: it.Key, ItemRef: ref}
		// What sent the foreach back is about the work as a whole; the first
		// iteration that runs is where it can be acted on.
		if first {
			cf.Feedback = cur.Feedback
		}
		if err := m.bindInputs(fr, n, callee, cur.ID, &cf, input); err != nil {
			return Status{}, false, err
		}
		if n.Over == "findings" {
			snap, err := m.e.snapshot(m.ctx, m.run, &occurrence{ID: fid, Workflow: cur.Workflow, Node: cur.Node})
			if err != nil {
				return Status{}, false, err
			}
			cf.Tree = snap
		}
		_, err := m.e.create(m.run, prefFrame+fid, cf)
		return Status{}, true, err
	}
	out := OutcomeDone
	if incomplete {
		out = OutcomeIncomplete
	}
	return m.finish(cur, out, "", nil)
}

// iterationTree is the snapshot the nearest enclosing findings iteration
// began from.
func (m *mover) iterationTree(fr *frame) (SnapshotRef, error) {
	f := fr
	for f.Tree == "" {
		if f.Parent == "" {
			return "", fmt.Errorf("data %q exists only inside a foreach over findings", DiffFromRef)
		}
		var p frame
		if err := m.e.getJSON(m.run, prefFrame+f.Parent, &p); err != nil {
			return "", err
		}
		f = &p
	}
	return f.Tree, nil
}

// stepFrame is the nearest enclosing iteration over the plan's steps.
func (m *mover) stepFrame(fr *frame) (*frame, error) {
	f := fr
	for f.Over != "steps" {
		if f.Parent == "" {
			return nil, errors.New("commit with scope step runs outside a foreach over steps")
		}
		var p frame
		if err := m.e.getJSON(m.run, prefFrame+f.Parent, &p); err != nil {
			return nil, err
		}
		f = &p
	}
	return f, nil
}
