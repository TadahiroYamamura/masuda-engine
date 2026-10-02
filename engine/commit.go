package engine

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

const dataCommitMessage = "commit-message"

type planDoc struct {
	Summary string `json:"summary"`
	Steps   []struct {
		Number      int      `json:"number"`
		Description string   `json:"description"`
		Files       []string `json:"files"`
	} `json:"steps"`
	ExpectedByproducts []string `json:"expected_byproducts"`
}

type stepDoc struct {
	Description string   `json:"description"`
	Files       []string `json:"files"`
}

// prepareBase fixes the snapshot an occurrence compares the worktree with.
// The worktree does not change between nodes, so an agent or exec starts
// where the frame's previous boundary left it; only a frame's first such
// node has to take a snapshot of its own. A commit compares with where the
// work since the last commit started.
func (m *mover) prepareBase(fr *frame, n *Node, occ *occurrence) error {
	switch n.Type {
	case NodeAgent, NodeExec:
		occ.Base = m.lastBoundary(fr.ID)
	case NodeCommit:
		occ.Base = m.workStart()
	default:
		return nil
	}
	if occ.Base == "" {
		snap, err := m.e.snapshot(m.ctx, m.run, occ)
		if err != nil {
			return err
		}
		occ.Base = snap
	}
	if n.Type == NodeAgent {
		if a := m.e.set.agentFor(n.Role); a != nil && !a.WriteCapable() {
			_, hash, err := m.e.runner.ChangedSince(m.ctx, m.run, occ.Base)
			if err != nil {
				return err
			}
			occ.BaseHash = hash
		}
	}
	return nil
}

func (m *mover) lastBoundary(frameID string) SnapshotRef {
	occs := m.recs.byFrame[frameID]
	for i := len(occs) - 1; i >= 0; i-- {
		if res := m.recs.results[occs[i].ID]; res != nil && res.Snapshot != "" {
			return res.Snapshot
		}
	}
	return ""
}

// lastCommit is the id of the run's latest occurrence that committed.
func (m *mover) lastCommit() string {
	for i := len(m.recs.occs) - 1; i >= 0; i-- {
		if res := m.recs.results[m.recs.occs[i].ID]; res != nil && res.Commit != "" {
			return m.recs.occs[i].ID
		}
	}
	return ""
}

// workStart is the base of the first agent or exec after the run's last
// commit, in any frame: occurrence ids are in time order, and frames run
// one at a time, so that is where the uncommitted work began.
func (m *mover) workStart() SnapshotRef {
	after := m.lastCommit()
	for _, o := range m.recs.occs {
		if o.ID <= after || o.Base == "" {
			continue
		}
		if n, err := m.e.nodeOf(o); err == nil && (n.Type == NodeAgent || n.Type == NodeExec) {
			return o.Base
		}
	}
	return ""
}

// approvedFiles are the files every deviation gate of the run has approved
// so far. An approval that names no files approves what the gate showed.
func (r *records) approvedFiles() []string {
	var out []string
	for _, gs := range r.devs {
		for _, g := range gs {
			if g.Decision == nil || g.Decision.Outcome != OutcomeApproved {
				continue
			}
			files := g.Decision.ApprovedFiles
			if len(files) == 0 {
				files = g.Files
			}
			for _, f := range files {
				if !slices.Contains(out, f) {
					out = append(out, f)
				}
			}
		}
	}
	slices.Sort(out)
	return out
}

// commit checks what changed against the plan, asks a human about anything
// outside it, and commits. Once a deviation gate is approved the engine
// commits without looking again: the host's Commit enforces Allowed and
// reports anything still outside it as Deviations, which opens the next
// gate. Looking again would ask about a worktree the human did not see.
func (m *mover) commit(fr *frame, cur *occurrence, n *Node) (Status, bool, error) {
	gates := m.recs.devs[cur.ID]
	if len(gates) > 0 {
		g := gates[len(gates)-1]
		if g.Decision == nil {
			return Status{Kind: StatusGate, Occurrence: cur.ID, Gate: &g.Request}, false, nil
		}
		if g.Decision.Outcome == OutcomeRejected {
			fb := "計画外の変更が却下された: " + strings.Join(g.Files, ", ")
			if g.Decision.Comment != "" {
				fb += "\n" + g.Decision.Comment
			}
			return m.finish(cur, OutcomeRejected, fb, nil)
		}
	}
	if err := m.need(); err != nil {
		return Status{}, false, err
	}
	plan, err := m.plan(fr, cur.ID)
	if err != nil {
		return Status{}, false, fmt.Errorf("%s: node %s: %w", fr.Workflow, n.ID, err)
	}
	req := CommitRequest{Run: m.run, Occurrence: cur.ID, Scope: n.Scope, Byproducts: plan.ExpectedByproducts}
	var fallback string
	if n.Scope == "step" {
		sf, err := m.stepFrame(fr)
		if err != nil {
			return Status{}, false, fmt.Errorf("%s: node %s: %w", fr.Workflow, n.ID, err)
		}
		b, err := m.e.runner.GetData(m.ctx, m.run, sf.ItemRef)
		if err != nil {
			return Status{}, false, err
		}
		var step stepDoc
		if err := json.Unmarshal(b, &step); err != nil {
			return Status{}, false, fmt.Errorf("%s: node %s: step %s: %w", fr.Workflow, n.ID, sf.Item, err)
		}
		req.Step, req.Allowed, fallback = sf.Item, step.Files, step.Description
	} else {
		for _, s := range plan.Steps {
			req.Allowed = append(req.Allowed, s.Files...)
		}
		fallback = plan.Summary
	}
	for _, f := range m.recs.approvedFiles() {
		if !slices.Contains(req.Allowed, f) {
			req.Allowed = append(req.Allowed, f)
		}
	}
	if len(gates) == 0 {
		files, hash, err := m.e.runner.ChangedSince(m.ctx, m.run, cur.Base)
		if err != nil {
			return Status{}, false, err
		}
		var outside []string
		for _, f := range files {
			if !slices.Contains(req.Allowed, f) && !slices.Contains(req.Byproducts, f) {
				outside = append(outside, f)
			}
		}
		if len(outside) > 0 {
			return m.openDeviation(cur, outside, hash)
		}
	}
	if req.Message, err = m.commitMessage(); err != nil {
		return Status{}, false, err
	}
	if req.Message == "" {
		req.Message = fallback
	}
	out, err := m.e.runner.Commit(m.ctx, req)
	if err != nil {
		return Status{}, false, err
	}
	if len(out.Deviations) > 0 {
		return m.openDeviation(cur, out.Deviations, out.DeviationsHash)
	}
	_, err = m.e.putResult(m.run, cur, result{Outcome: OutcomeDone, Commit: out.Commit})
	return Status{}, true, err
}

func (m *mover) plan(fr *frame, occ string) (*planDoc, error) {
	ref, err := m.resolve(fr, dataPlan, occ)
	if err != nil {
		return nil, err
	}
	b, err := m.e.runner.GetData(m.ctx, m.run, ref)
	if err != nil {
		return nil, err
	}
	var p planDoc
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}
	return &p, nil
}

// commitMessage is the commit-message written since the last commit, if
// any. An older one describes work that is already committed.
func (m *mover) commitMessage() (string, error) {
	o, ok := m.recs.latest[dataCommitMessage]
	if !ok || o <= m.lastCommit() {
		return "", nil
	}
	b, err := m.e.runner.GetData(m.ctx, m.run, DataRef{Name: dataCommitMessage, Occurrence: o})
	return string(b), err
}

// openDeviation stores and announces the next deviation gate of cur.
func (m *mover) openDeviation(cur *occurrence, files []string, hash string) (Status, bool, error) {
	if err := m.need(); err != nil {
		return Status{}, false, err
	}
	g := devGate{N: len(m.recs.devs[cur.ID]) + 1, Files: files}
	g.Request = GateRequest{Run: m.run, Occurrence: cur.ID, Gate: GateDeviation, TargetHash: hash, Subject: []byte(strings.Join(files, "\n"))}
	applied, err := m.e.create(m.run, prefDevGate+devKey(cur.ID, g.N), g)
	if err != nil || !applied {
		return Status{}, true, err
	}
	if err := m.e.runner.OpenGate(m.ctx, g.Request); err != nil {
		return Status{}, false, err
	}
	m.e.log(m.run, Event{Kind: "gate-open", Occurrence: cur.ID, Workflow: cur.Workflow, Node: cur.Node, Detail: GateDeviation})
	return Status{Kind: StatusGate, Occurrence: cur.ID, Gate: &g.Request}, false, nil
}

// readOnlyDeviation holds an agent that cannot write but changed the
// worktree at a deviation gate before its outcome is followed. wait=true
// means the caller returns what this returned. A rejection stops the run:
// the engine cannot undo the change, and the agent was never meant to make
// one, so there is no node to send it back to.
func (m *mover) readOnlyDeviation(cur *occurrence, res *result) (st Status, wait, moved bool, err error) {
	gates := m.recs.devs[cur.ID]
	if len(gates) == 0 {
		st, moved, err = m.openDeviation(cur, res.Deviation, res.DeviationHash)
		return st, true, moved, err
	}
	g := gates[len(gates)-1]
	switch {
	case g.Decision == nil:
		return Status{Kind: StatusGate, Occurrence: cur.ID, Gate: &g.Request}, true, false, nil
	case g.Decision.Outcome == OutcomeRejected:
		st, moved, err = m.block(cur.ID, cur.Workflow, cur.Node,
			fmt.Sprintf("%s: node %s changed %v without being able to write, and the change was rejected", cur.Workflow, cur.Node, g.Files))
		return st, true, moved, err
	}
	return Status{}, false, false, nil
}

func (e *Engine) decideDeviation(run RunID, o *occurrence, gate *GateRequest, d Decision) error {
	recs, err := e.loadRecords(run)
	if err != nil {
		return err
	}
	gates := recs.devs[o.ID]
	if len(gates) == 0 {
		return fmt.Errorf("engine: occurrence %s has no deviation gate", o.ID)
	}
	g := gates[len(gates)-1]
	switch d.Outcome {
	case OutcomeApproved:
		if d.TargetHash != gate.TargetHash {
			return fmt.Errorf("engine: approval for %s was made against %q, but the gate is on %q", o.ID, d.TargetHash, gate.TargetHash)
		}
		for _, f := range d.ApprovedFiles {
			if !slices.Contains(g.Files, f) {
				return fmt.Errorf("engine: deviation %s: %q is not one of the files asked about %v", o.ID, f, g.Files)
			}
		}
	case OutcomeRejected:
	default:
		return fmt.Errorf("engine: gate %s: decision %q: %w", GateDeviation, d.Outcome, ErrNotImplemented)
	}
	applied, err := e.create(run, prefDevDec+devKey(o.ID, g.N), d)
	if err != nil {
		return err
	}
	if !applied {
		return fmt.Errorf("engine: deviation gate %s/%d has already been decided", o.ID, g.N)
	}
	e.log(run, Event{Kind: "decision", Occurrence: o.ID, Workflow: o.Workflow, Node: o.Node, Outcome: d.Outcome, Detail: d.Comment})
	return nil
}

// publish lands the run's latest commit. The contract has it carry the
// commit the review gate approved; until the review gate records which
// commit it saw, the latest commit stands in for it.
func (m *mover) publish(cur *occurrence, n *Node) (Status, bool, error) {
	if err := m.need(); err != nil {
		return Status{}, false, err
	}
	target := n.PublishTarget
	if target == "" {
		target = "local"
	}
	commit := ""
	if id := m.lastCommit(); id != "" {
		commit = m.recs.results[id].Commit
	}
	if err := m.e.runner.Publish(m.ctx, PublishRequest{Run: m.run, Target: target, Commit: commit, Export: n.Export}); err != nil {
		return Status{}, false, err
	}
	return m.finish(cur, OutcomeDone, "", nil)
}
