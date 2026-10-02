package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// A concern is kept as three records so that each is written once and by
// one party: the guest's report (concern/), the engine's opening of the
// triage gate together with the occurrence it interrupted (triage-gate/),
// and the human's decision (triage/). All are keyed "<occ>/<nnn>", occ being
// the occurrence the concern was reported from.
const (
	prefConcern    = "concern/"
	prefTriageGate = "triage-gate/"
	prefTriageDec  = "triage/"
)

type concern struct {
	N          int         `json:"n"`
	Occurrence string      `json:"occurrence"`
	Text       string      `json:"text"`
	Gate       *triageGate `json:"-"`
	Decision   *Decision   `json:"-"`
}

type triageGate struct {
	Request GateRequest `json:"request"`
	// Interrupted is the occurrence the run stood on when the gate opened.
	// The run cannot move while the gate is open, so it is also where the
	// run stands when the decision is followed.
	Interrupted string `json:"interrupted,omitempty"`
}

func (c *concern) key() string { return devKey(c.Occurrence, c.N) }

func (e *Engine) reportConcern(ctx context.Context, run RunID, occ, text string) error {
	var s startRecord
	if err := e.getJSON(run, keyStart, &s); err != nil {
		if errors.Is(err, errMissing) {
			return errNotStarted
		}
		return err
	}
	var o occurrence
	if err := e.getJSON(run, prefOcc+occ, &o); err != nil {
		return fmt.Errorf("engine: concern from occurrence %s: %w", occ, err)
	}
	for {
		kvs, err := e.store.List(runKey(run, prefConcern+occ+"/"))
		if err != nil {
			return err
		}
		c := concern{N: len(kvs) + 1, Occurrence: occ, Text: text}
		applied, err := e.create(run, prefConcern+c.key(), c)
		if err != nil {
			return err
		}
		if applied {
			e.log(run, Event{Kind: "concern", Occurrence: occ, Workflow: o.Workflow, Node: o.Node, Detail: text})
			return nil
		}
	}
}

func (e *Engine) loadConcerns(run RunID, r *records) error {
	kvs, err := e.store.List(runKey(run, prefConcern))
	if err != nil {
		return err
	}
	byKey := map[string]*concern{}
	for _, kv := range kvs {
		var c concern
		if err := json.Unmarshal(kv.Value, &c); err != nil {
			return fmt.Errorf("%s: %w", kv.Key, err)
		}
		r.concerns = append(r.concerns, &c)
		byKey[c.key()] = &c
	}
	sort.Slice(r.concerns, func(i, j int) bool { return r.concerns[i].key() < r.concerns[j].key() })
	kvs, err = e.store.List(runKey(run, prefTriageGate))
	if err != nil {
		return err
	}
	for _, kv := range kvs {
		if c := byKey[kv.Key[len(runKey(run, prefTriageGate)):]]; c != nil {
			var g triageGate
			if err := json.Unmarshal(kv.Value, &g); err != nil {
				return fmt.Errorf("%s: %w", kv.Key, err)
			}
			c.Gate = &g
		}
	}
	kvs, err = e.store.List(runKey(run, prefTriageDec))
	if err != nil {
		return err
	}
	for _, kv := range kvs {
		if c := byKey[kv.Key[len(runKey(run, prefTriageDec)):]]; c != nil {
			var d Decision
			if err := json.Unmarshal(kv.Value, &d); err != nil {
				return fmt.Errorf("%s: %w", kv.Key, err)
			}
			c.Decision = &d
		}
	}
	for _, c := range r.concerns {
		if c.Gate == nil || c.Decision == nil || c.Gate.Interrupted == "" {
			continue
		}
		id := c.Gate.Interrupted
		r.triaged[id] = true
		switch c.Decision.Outcome {
		case "redo":
			fb := c.Decision.Comment
			if fb == "" {
				fb = "報告された懸念によりやり直す: " + c.Text
			}
			r.reenter[id] = fb
		case "dismiss":
			// A node that already finished is not interrupted: its result
			// stands and the run follows it. One still at work is entered
			// again with what it was given the first time.
			if r.results[id] == nil {
				fb := ""
				for _, o := range r.occs {
					if o.ID == id {
						fb = o.Feedback
					}
				}
				r.reenter[id] = fb
			} else {
				delete(r.reenter, id)
			}
		}
	}
	return nil
}

// triage handles the first concern not yet settled, before anything else
// moves. handled=false means there is none and the run moves as usual.
func (m *mover) triage() (st Status, handled, moved bool, err error) {
	for _, c := range m.recs.concerns {
		switch {
		case c.Gate == nil:
			st, moved, err = m.openTriage(c)
			return st, true, moved, err
		case c.Decision == nil:
			return Status{Kind: StatusGate, Occurrence: c.Occurrence, Gate: &c.Gate.Request}, true, false, nil
		case c.Decision.Outcome == "halt":
			reason := "懸念によりtriageで停止した: " + c.Text
			if c.Decision.Comment != "" {
				reason += "\n" + c.Decision.Comment
			}
			o := m.occ(c.Gate.Interrupted)
			wf, node := "", ""
			if o != nil {
				wf, node = o.Workflow, o.Node
			}
			st, moved, err = m.block(c.Gate.Interrupted, wf, node, reason)
			return st, true, moved, err
		}
	}
	return Status{}, false, false, nil
}

func (m *mover) openTriage(c *concern) (Status, bool, error) {
	if err := m.need(); err != nil {
		return Status{}, false, err
	}
	leaf, err := m.leaf(rootFrame)
	if err != nil {
		return Status{}, false, err
	}
	sum := sha256.Sum256([]byte(c.Text))
	g := triageGate{Request: GateRequest{Run: m.run, Occurrence: c.Occurrence, Gate: GateTriage,
		TargetHash: hex.EncodeToString(sum[:]), Subject: []byte(c.Text)}}
	wf, node := "", ""
	if leaf != nil {
		g.Interrupted, wf, node = leaf.ID, leaf.Workflow, leaf.Node
	}
	applied, err := m.e.create(m.run, prefTriageGate+c.key(), g)
	if err != nil || !applied {
		return Status{}, true, err
	}
	if err := m.e.runner.OpenGate(m.ctx, g.Request); err != nil {
		return Status{}, false, err
	}
	m.e.log(m.run, Event{Kind: "gate-open", Occurrence: c.Occurrence, Workflow: wf, Node: node, Detail: GateTriage})
	return Status{Kind: StatusGate, Occurrence: c.Occurrence, Gate: &g.Request}, false, nil
}

// leaf is the occurrence the run stands on: the last one of frameID, or of
// the frame it is running inside a call or a foreach.
func (m *mover) leaf(frameID string) (*occurrence, error) {
	cur := m.recs.last(frameID)
	if cur == nil || m.recs.results[cur.ID] != nil {
		return cur, nil
	}
	n, err := m.e.nodeOf(cur)
	if err != nil {
		return nil, err
	}
	var children []string
	switch n.Type {
	case NodeWorkflow:
		children = []string{cur.ID}
	case NodeForeach:
		for i := range cur.Items {
			children = append(children, fmt.Sprintf("%s.%d", cur.ID, i+1))
		}
	}
	for _, child := range children {
		if len(m.recs.byFrame[child]) == 0 {
			continue
		}
		if _, ended, err := m.e.store.Get(runKey(m.run, prefFrameEnd+child)); err != nil {
			return nil, err
		} else if !ended {
			return m.leaf(child)
		}
	}
	return cur, nil
}

func (m *mover) occ(id string) *occurrence {
	for _, o := range m.recs.occs {
		if o.ID == id {
			return o
		}
	}
	return nil
}

func (e *Engine) decideTriage(run RunID, occ string, d Decision) error {
	switch d.Outcome {
	case "dismiss", "halt", "redo":
	default:
		return fmt.Errorf("engine: gate %s takes dismiss, halt or redo, not %q", GateTriage, d.Outcome)
	}
	recs, err := e.loadRecords(run)
	if err != nil {
		return err
	}
	var c *concern
	for _, x := range recs.concerns {
		if x.Gate != nil && x.Decision == nil {
			c = x
			break
		}
	}
	if c == nil || c.Occurrence != occ {
		return fmt.Errorf("engine: occurrence %s has no open triage gate", occ)
	}
	applied, err := e.create(run, prefTriageDec+c.key(), d)
	if err != nil {
		return err
	}
	if !applied {
		return fmt.Errorf("engine: triage gate %s/%d has already been decided", occ, c.N)
	}
	wf, node := "", ""
	for _, o := range recs.occs {
		if o.ID == c.Gate.Interrupted {
			wf, node = o.Workflow, o.Node
		}
	}
	e.log(run, Event{Kind: "decision", Occurrence: occ, Workflow: wf, Node: node, Outcome: d.Outcome, Detail: d.Comment})
	if d.Outcome == "redo" || (d.Outcome == "dismiss" && recs.results[c.Gate.Interrupted] == nil) {
		if err := e.supersede(run, recs, c.Gate.Interrupted); err != nil {
			return err
		}
	}
	action := map[string]string{"dismiss": "懸念を退けて続ける", "halt": "runを止める", "redo": "差し戻して入り直す"}[d.Outcome]
	e.log(run, Event{Kind: "triage", Occurrence: c.Gate.Interrupted, Workflow: wf, Node: node, Outcome: d.Outcome,
		Detail: strings.Join([]string{action, c.Text}, ": ")})
	return nil
}

// outcomeSuperseded closes a gate no human decided: triage sent the run into
// the node that opened it again, and the new entry opens its own gate.
const outcomeSuperseded = "superseded"

// supersede closes whatever gate occurrence id still has open, so neither
// Status nor a host listing gates keeps showing it. It is called both when
// the triage decision is recorded and when the run re-enters the node; the
// records are written only if absent, so the second call does nothing.
func (e *Engine) supersede(run RunID, r *records, id string) error {
	var o *occurrence
	for _, x := range r.occs {
		if x.ID == id {
			o = x
		}
	}
	if o == nil {
		return nil
	}
	n, err := e.nodeOf(o)
	if err != nil {
		return err
	}
	d := Decision{Outcome: outcomeSuperseded, Comment: "triageで無効になった"}
	closed := func(key, gate string) error {
		applied, err := e.create(run, key, d)
		if err == nil && applied {
			e.log(run, Event{Kind: "decision", Occurrence: id, Workflow: o.Workflow, Node: o.Node, Outcome: outcomeSuperseded,
				Detail: gate + ": " + d.Comment})
		}
		return err
	}
	if n.Type == NodeApproval && r.results[id] == nil {
		if _, ok, err := e.store.Get(runKey(run, prefGate+id)); err != nil {
			return err
		} else if ok {
			if err := closed(prefGateClosed+id, n.Gate); err != nil {
				return err
			}
		}
	}
	if gs := r.devs[id]; len(gs) > 0 && gs[len(gs)-1].Decision == nil {
		return closed(prefDevDec+devKey(id, gs[len(gs)-1].N), GateDeviation)
	}
	return nil
}
