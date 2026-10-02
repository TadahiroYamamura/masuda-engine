package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
)

// prepareItems fixes what a foreach iterates over when it is entered, so a
// recomputed position walks the same items even if the data moves on.
func (m *mover) prepareItems(fr *frame, n *Node, occ *occurrence) error {
	if d, ok := parseDataOver(n.Over); ok {
		items, err := m.dataItems(fr, n, d, occ.ID)
		if err != nil {
			return err
		}
		occ.Items = items
		return nil
	}
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
		if _, ok := parseDataOver(n.Over); ok {
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

// iterationTree is the snapshot the nearest enclosing iteration over a data
// array (findings, typically) began from.
func (m *mover) iterationTree(fr *frame) (SnapshotRef, error) {
	f := fr
	for f.Tree == "" {
		if f.Parent == "" {
			return "", fmt.Errorf("data %q exists only inside a foreach over a data array (such as findings)", DiffFromRef)
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

// errItems is a data value a foreach cannot iterate; the run stops on it
// rather than failing Advance, since retrying cannot change the data.
type errItems struct{ msg string }

func (e *errItems) Error() string { return e.msg }

// dataItems makes the items of a foreach over a data array: one per element
// that passes the filter, keyed by its id (or its index in the whole array,
// so keys do not shift with the filter). For accumulated data, elements an
// earlier iteration of the run already finished done are marked Done.
func (m *mover) dataItems(fr *frame, n *Node, d dataOver, occ string) ([]Item, error) {
	ref, err := m.resolve(fr, d.Data, occ)
	if err != nil {
		return nil, fmt.Errorf("%s: node %s: over %s: %w", fr.Workflow, n.ID, n.Over, err)
	}
	b, err := m.e.runner.GetData(m.ctx, m.run, ref)
	if err != nil {
		return nil, err
	}
	arr, err := jsonArray(b)
	if err != nil {
		return nil, &errItems{fmt.Sprintf("%s: node %s: over %s: %q is not a JSON array", fr.Workflow, n.ID, n.Over, d.Data)}
	}
	var done map[string]bool
	if m.e.set.accumulates(d.Data) {
		if done, err = m.doneKeys(d.Data); err != nil {
			return nil, err
		}
	}
	input := singular(d.Data)
	var items []Item
	for i, el := range arr {
		if d.Filter && !fieldMatches(el, d.Field, d.Value) {
			continue
		}
		key := elementID(el)
		if key == "" {
			key = strconv.Itoa(i)
		}
		items = append(items, Item{Key: key, Input: input, Content: el, Done: done[key]})
	}
	return items, nil
}

// doneKeys are the item keys of every iteration over data that finished
// done anywhere in the run.
func (m *mover) doneKeys(data string) (map[string]bool, error) {
	ends, err := m.e.store.List(runKey(m.run, prefFrameEnd))
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, kv := range ends {
		if string(kv.Value) != OutcomeDone {
			continue
		}
		var f frame
		if err := m.e.getJSON(m.run, prefFrame+kv.Key[len(runKey(m.run, prefFrameEnd)):], &f); err != nil {
			if errors.Is(err, errMissing) {
				continue
			}
			return nil, err
		}
		if d, ok := parseDataOver(f.Over); ok && d.Data == data && f.Item != "" {
			out[f.Item] = true
		}
	}
	return out, nil
}

// fieldMatches compares an element's field with a filter value: true and
// false as booleans, anything that parses as a number as a number, anything
// else as a string (quotes around it optional).
func fieldMatches(el json.RawMessage, field, value string) bool {
	var obj map[string]any
	if json.Unmarshal(el, &obj) != nil {
		return false
	}
	got, ok := obj[field]
	if !ok {
		return false
	}
	switch value {
	case "true", "false":
		b, ok := got.(bool)
		return ok && b == (value == "true")
	}
	if f, err := strconv.ParseFloat(value, 64); err == nil {
		g, ok := got.(float64)
		return ok && g == f
	}
	if uq, err := strconv.Unquote(value); err == nil {
		value = uq
	}
	g, ok := got.(string)
	return ok && g == value
}
