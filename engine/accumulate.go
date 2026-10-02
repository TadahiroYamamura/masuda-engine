package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

const accumulateKey = "x-masuda-accumulate"

// accumulates reports whether name is accumulated data: its schema carries
// "x-masuda-accumulate": true at the top level.
func (s *Set) accumulates(name string) bool {
	raw, ok := s.Schemas[name]
	if !ok {
		return false
	}
	var top map[string]json.RawMessage
	if json.Unmarshal(raw, &top) != nil {
		return false
	}
	var v bool
	return json.Unmarshal(top[accumulateKey], &v) == nil && v
}

var errNotArray = errors.New("累積データなのでJSONの配列でなければならない")

func jsonArray(b []byte) ([]json.RawMessage, error) {
	var arr []json.RawMessage
	if t := bytes.TrimSpace(b); len(t) == 0 || t[0] != '[' {
		return nil, errNotArray
	}
	if err := json.Unmarshal(b, &arr); err != nil {
		return nil, fmt.Errorf("%w: %v", errNotArray, err)
	}
	return arr, nil
}

// elementID is an element's `id` as compact JSON text ("" when it has none),
// so ids of any JSON type compare and key consistently.
func elementID(el json.RawMessage) string {
	var obj map[string]json.RawMessage
	if json.Unmarshal(el, &obj) != nil {
		return ""
	}
	id, ok := obj["id"]
	if !ok {
		return ""
	}
	var s string
	if json.Unmarshal(id, &s) == nil {
		return s
	}
	var buf bytes.Buffer
	if json.Compact(&buf, id) != nil {
		return string(id)
	}
	return buf.String()
}

// withdrawn reports whether an element carries "withdrawn": true.
func withdrawn(el json.RawMessage) bool {
	var obj map[string]json.RawMessage
	if json.Unmarshal(el, &obj) != nil {
		return false
	}
	var v bool
	return json.Unmarshal(obj["withdrawn"], &v) == nil && v
}

// mergeAccumulated adds a write to the accumulated value: an element whose
// id is already there replaces it in place (last write wins), any other is
// appended. Replacing in place rather than moving to the end keeps the order
// readers see stable across rewrites of the same finding.
//
// Withdrawn elements are dropped from the stored value rather than kept and
// filtered on each read, so GetData, inputs and foreach all see the same
// array without every reader knowing the rule. The cost is that rewriting a
// withdrawn id without withdrawn appends it at the end instead of at its old
// place.
func mergeAccumulated(cur, add []byte) ([]byte, error) {
	base, err := jsonArray(cur)
	if err != nil {
		return nil, err
	}
	news, err := jsonArray(add)
	if err != nil {
		return nil, err
	}
	at := map[string]int{}
	for i, el := range base {
		if id := elementID(el); id != "" {
			at[id] = i
		}
	}
	for _, el := range news {
		id := elementID(el)
		if i, ok := at[id]; ok && id != "" {
			base[i] = el
			continue
		}
		if id != "" {
			at[id] = len(base)
		}
		base = append(base, el)
	}
	kept := []json.RawMessage{}
	for _, el := range base {
		if !withdrawn(el) {
			kept = append(kept, el)
		}
	}
	return json.Marshal(kept)
}

// putOutputs stores what occurrence occ wrote. Accumulated data is stored
// as the whole accumulated array after this write, so every later reader is
// handed one value through Runner.GetData without the Runner knowing about
// accumulation.
func (e *Engine) putOutputs(ctx context.Context, run RunID, recs *records, occ string, names []string, got map[string][]byte) error {
	for _, name := range names {
		c := got[name]
		if e.set.accumulates(name) {
			cur := []byte("[]")
			if o, ok := recs.latest[name]; ok {
				b, err := e.runner.GetData(ctx, run, DataRef{Name: name, Occurrence: o})
				if err != nil {
					return err
				}
				cur = b
			}
			merged, err := mergeAccumulated(cur, c)
			if err != nil {
				return fmt.Errorf("engine: %s: %w", name, err)
			}
			c = merged
		}
		if err := e.runner.PutData(ctx, run, DataRef{Name: name, Occurrence: occ}, c); err != nil {
			return err
		}
	}
	return nil
}
