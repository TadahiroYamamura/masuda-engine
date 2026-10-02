package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// errPending means the run cannot be described without moving it first
// (a result was reported but not yet followed, say). Status returns it;
// Advance never does.
var errPending = errors.New("engine: the run has moves pending; call Advance")

var errNotStarted = errors.New("engine: run not started")

func newEngine(set *Set, store Store, runner Runner, opts Options) *Engine {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Engine{set: set, store: store, runner: runner, now: now}
}

func (e *Engine) log(run RunID, ev Event) {
	ev.Time = e.now()
	ev.Run = run
	e.runner.Log(ev)
}

func (e *Engine) start(run RunID, root string, inputs []string) error {
	w := e.set.Workflows[root]
	if w == nil {
		return fmt.Errorf("engine: workflow %s is not loaded", root)
	}
	for _, in := range w.Inputs {
		if !slices.Contains(inputs, in) {
			return fmt.Errorf("engine: %s needs input %q", root, in)
		}
	}
	var prev startRecord
	switch err := e.getJSON(run, keyStart, &prev); {
	case err == nil:
		if prev.Root == root && slices.Equal(prev.Inputs, inputs) {
			return nil
		}
		return fmt.Errorf("engine: run %s already started with %s", run, prev.Root)
	case !errors.Is(err, errMissing):
		return err
	}
	fr := frame{ID: rootFrame, Workflow: root, Inputs: map[string]DataRef{}}
	for _, in := range inputs {
		fr.Inputs[in] = DataRef{Name: in}
	}
	// The root frame goes first: a start record without it would be a run
	// that can never move, whereas a frame without a start record is simply
	// written again by the retry.
	if _, err := e.create(run, prefFrame+rootFrame, fr); err != nil {
		return err
	}
	applied, err := e.create(run, keyStart, startRecord{Root: root, Inputs: inputs})
	if err != nil {
		return err
	}
	if !applied {
		return e.start(run, root, inputs)
	}
	e.log(run, Event{Kind: "start", Workflow: root})
	return nil
}

// mover walks a run from its root frame to the occurrence it currently
// stands on. With mutate false it only looks: any move it would make is
// reported as errPending instead.
type mover struct {
	e      *Engine
	ctx    context.Context
	run    RunID
	mutate bool
	recs   *records
	start  startRecord
}

func (m *mover) need() error {
	if !m.mutate {
		return errPending
	}
	return nil
}

func (e *Engine) walk(ctx context.Context, run RunID, mutate bool) (Status, error) {
	m := &mover{e: e, ctx: ctx, run: run, mutate: mutate}
	if err := e.getJSON(run, keyStart, &m.start); err != nil {
		if errors.Is(err, errMissing) {
			return Status{}, errNotStarted
		}
		return Status{}, err
	}
	for {
		if b, ok, err := e.store.Get(runKey(run, keyBlocked)); err != nil {
			return Status{}, err
		} else if ok {
			return Status{Kind: StatusBlocked, Reason: string(b)}, nil
		}
		if b, ok, err := e.store.Get(runKey(run, prefFrameEnd+rootFrame)); err != nil {
			return Status{}, err
		} else if ok {
			return Status{Kind: StatusDone, Outcome: string(b)}, nil
		}
		recs, err := e.loadRecords(run)
		if err != nil {
			return Status{}, err
		}
		m.recs = recs
		st, moved, err := m.step(rootFrame)
		if err != nil {
			return Status{}, err
		}
		if !moved {
			return st, nil
		}
	}
}

// step makes at most one move in frameID, descending into a running call.
// moved=true means the records changed and the caller reloads them.
func (m *mover) step(frameID string) (Status, bool, error) {
	var fr frame
	if err := m.e.getJSON(m.run, prefFrame+frameID, &fr); err != nil {
		return Status{}, false, err
	}
	w := m.e.set.Workflows[fr.Workflow]
	if w == nil {
		return Status{}, false, fmt.Errorf("engine: workflow %s is not loaded", fr.Workflow)
	}
	cur := m.recs.last(frameID)
	if cur == nil {
		return m.enter(&fr, w, w.Start, fr.Feedback)
	}
	n := w.Nodes[cur.Node]
	if n == nil {
		return Status{}, false, fmt.Errorf("engine: %s has no node %s", w.Path, cur.Node)
	}
	if res := m.recs.results[cur.ID]; res != nil {
		return m.transition(&fr, w, cur, n, res)
	}
	if cur.Exhausted {
		return m.finish(cur, OutcomeExhausted, "", nil)
	}
	switch n.Type {
	case NodeAgent:
		return Status{Kind: StatusAgent, Occurrence: cur.ID, Task: m.e.task(m.run, cur, n)}, false, nil
	case NodeExec:
		return m.exec(&fr, cur, n)
	case NodeQuestion:
		if n.Role != "" {
			return Status{Kind: StatusAgent, Occurrence: cur.ID, Task: m.e.task(m.run, cur, n)}, false, nil
		}
		return m.question(cur, n)
	case NodeApproval:
		return m.approval(&fr, cur, n)
	case NodeWorkflow:
		return m.call(&fr, cur, n)
	case NodeDiscard:
		if err := m.need(); err != nil {
			return Status{}, false, err
		}
		if err := m.e.runner.Discard(m.ctx, m.run, n.Export); err != nil {
			return Status{}, false, err
		}
		return m.finish(cur, OutcomeDone, "", nil)
	}
	return Status{}, false, fmt.Errorf("%s: node %s (type %s): %w", w.Path, n.ID, n.Type, ErrNotImplemented)
}

// enter records an entry into node id, or an exhausted entry when the node's
// max is reached. Entries are counted since the frame's last decided
// approval: a human looking at the work restarts the budget.
func (m *mover) enter(fr *frame, w *Workflow, id, feedback string) (Status, bool, error) {
	if err := m.need(); err != nil {
		return Status{}, false, err
	}
	if len(m.recs.occs) >= Fuse {
		return m.block("", w.Path, id, fmt.Sprintf("fuse: the run reached %d occurrences", Fuse))
	}
	n := w.Nodes[id]
	if n == nil {
		return Status{}, false, fmt.Errorf("engine: %s has no node %s", w.Path, id)
	}
	occ := &occurrence{ID: m.recs.nextID(), Frame: fr.ID, Workflow: w.Path, Node: id, Feedback: feedback}
	if max := effectiveMax(n); max > 0 {
		count := 0
		for _, o := range m.recs.byFrame[fr.ID] {
			if o.Node == id && !o.Exhausted {
				count++
			}
			if w.Nodes[o.Node] != nil && w.Nodes[o.Node].Type == NodeApproval && m.recs.results[o.ID] != nil {
				count = 0
			}
		}
		occ.Exhausted = count >= max
	}
	if n.Role != "" && !occ.Exhausted {
		if err := m.prepareAgent(fr, n, occ); err != nil {
			return Status{}, false, err
		}
	}
	applied, err := m.e.create(m.run, prefOcc+occ.ID, occ)
	if err != nil || !applied {
		return Status{}, true, err
	}
	detail := ""
	if occ.Exhausted {
		detail = "max reached; not run"
	}
	m.e.log(m.run, Event{Kind: "enter", Occurrence: occ.ID, Workflow: w.Path, Node: id, Detail: detail})
	return Status{}, true, nil
}

// prepareAgent fixes what the agent task reads and writes, and switches the
// sandbox policy before the task can be handed out.
func (m *mover) prepareAgent(fr *frame, n *Node, occ *occurrence) error {
	a := m.e.set.agentFor(n.Role)
	if a == nil {
		return fmt.Errorf("engine: %s: no agent %s", n.ID, n.Role)
	}
	occ.Inputs = map[string]DataRef{}
	for k, v := range fr.Inputs {
		occ.Inputs[k] = v
	}
	for _, name := range slices.Concat(n.Inputs, a.Inputs) {
		if _, ok := occ.Inputs[name]; ok {
			continue
		}
		ref, err := m.resolve(fr, name, occ.ID)
		if err != nil {
			return fmt.Errorf("%s: node %s: %w", occ.Workflow, n.ID, err)
		}
		occ.Inputs[name] = ref
	}
	// A question node's output holds the human's answers, which the engine
	// stores from Answer; the agent only asks.
	nodeOutputs := n.Outputs
	if n.Type == NodeQuestion {
		nodeOutputs = nil
	}
	for _, name := range slices.Concat(nodeOutputs, a.Outputs) {
		if !slices.Contains(occ.Outputs, name) {
			occ.Outputs = append(occ.Outputs, name)
		}
	}
	p := Policy{Egress: n.Egress, Secrets: n.Secrets}
	occ.Policy = &p
	return m.setPolicy(occ, p)
}

func (m *mover) setPolicy(occ *occurrence, p Policy) error {
	if err := m.e.runner.SetPolicy(m.ctx, m.run, p); err != nil {
		return err
	}
	m.e.log(m.run, Event{Kind: "policy", Occurrence: occ.ID, Workflow: occ.Workflow, Node: occ.Node,
		Detail: fmt.Sprintf("egress=%v secrets=%v", p.Egress, p.Secrets)})
	return nil
}

// snapshot records the worktree at the end of an agent or exec occurrence,
// so later diffs and deviation checks can start from that boundary.
func (e *Engine) snapshot(ctx context.Context, run RunID, o *occurrence) (SnapshotRef, error) {
	ref, err := e.runner.Snapshot(ctx, run, o.ID)
	if err != nil {
		return "", err
	}
	e.log(run, Event{Kind: "snapshot", Occurrence: o.ID, Workflow: o.Workflow, Node: o.Node, Detail: string(ref)})
	return ref, nil
}

func (e *Engine) nodeOf(o *occurrence) (*Node, error) {
	if w := e.set.Workflows[o.Workflow]; w != nil {
		if n := w.Nodes[o.Node]; n != nil {
			return n, nil
		}
	}
	return nil, fmt.Errorf("engine: %s has no node %s", o.Workflow, o.Node)
}

func (e *Engine) status(ctx context.Context, run RunID) (Status, error) {
	st, err := e.walk(ctx, run, false)
	if errors.Is(err, errPending) {
		return Status{Kind: StatusPending}, nil
	}
	return st, err
}

// resolve finds the value of data name as seen from fr: the frame's bound
// inputs, then the latest value written in the run, then the run's own
// inputs. Engine data is computed into a value of occ.
func (m *mover) resolve(fr *frame, name, occ string) (DataRef, error) {
	if ref, ok := fr.Inputs[name]; ok {
		return ref, nil
	}
	if o, ok := m.recs.latest[name]; ok {
		return DataRef{Name: name, Occurrence: o}, nil
	}
	switch name {
	case string(DiffFromBase), string(DiffFromHead):
		ref := DataRef{Name: name, Occurrence: occ}
		if err := m.e.runner.Diff(m.ctx, m.run, DiffKind(name), "", ref); err != nil {
			return DataRef{}, err
		}
		return ref, nil
	case string(DiffFromRef):
		return DataRef{}, fmt.Errorf("data %q: %w", name, ErrNotImplemented)
	}
	if slices.Contains(m.start.Inputs, name) {
		return DataRef{Name: name}, nil
	}
	return DataRef{}, fmt.Errorf("data %q is not available", name)
}

func (e *Engine) task(run RunID, o *occurrence, n *Node) *AgentTask {
	t := &AgentTask{
		Run: run, Occurrence: o.ID, Workflow: o.Workflow, Node: o.Node,
		Agent: e.set.agentFor(n.Role), Inputs: o.Inputs, Outputs: o.Outputs, Feedback: o.Feedback,
	}
	if o.Policy != nil {
		t.Policy = *o.Policy
	}
	return t
}

func (m *mover) finish(o *occurrence, outcome, feedback string, outputs []string) (Status, bool, error) {
	if err := m.need(); err != nil {
		return Status{}, false, err
	}
	// Not applied means a concurrent call finished it first; reload either way.
	_, err := m.e.putResult(m.run, o, result{Outcome: outcome, Feedback: feedback, Outputs: outputs})
	return Status{}, true, err
}

func (e *Engine) putResult(run RunID, o *occurrence, res result) (bool, error) {
	applied, err := e.create(run, prefResult+o.ID, res)
	if err != nil || !applied {
		return applied, err
	}
	e.log(run, Event{Kind: "finish", Occurrence: o.ID, Workflow: o.Workflow, Node: o.Node, Outcome: res.Outcome})
	return true, nil
}

// record stores a result reported from outside (an agent, a human); losing
// the race to another report is an error the reporter must see.
func (e *Engine) record(run RunID, o *occurrence, res result) error {
	applied, err := e.putResult(run, o, res)
	if err == nil && !applied {
		err = fmt.Errorf("engine: occurrence %s has already finished", o.ID)
	}
	return err
}

func (m *mover) block(occ, wf, node, reason string) (Status, bool, error) {
	if err := m.need(); err != nil {
		return Status{}, false, err
	}
	applied, err := m.e.createRaw(m.run, keyBlocked, []byte(reason))
	if err != nil || !applied {
		return Status{}, true, err
	}
	m.e.log(m.run, Event{Kind: "blocked", Occurrence: occ, Workflow: wf, Node: node, Detail: reason})
	return Status{}, true, nil
}

// transition follows a finished occurrence's outcome: into the next node,
// out of the frame, or to a stop when nothing is routed.
func (m *mover) transition(fr *frame, w *Workflow, cur *occurrence, n *Node, res *result) (Status, bool, error) {
	if res.Invalid {
		return m.enter(fr, w, n.ID, res.Feedback)
	}
	t, ok := n.Next[res.Outcome]
	if !ok {
		return m.block(cur.ID, w.Path, n.ID, fmt.Sprintf("%s: node %s finished %q, which has no destination", w.Path, n.ID, res.Outcome))
	}
	if !t.End {
		return m.enter(fr, w, t.Node, res.Feedback)
	}
	if err := m.need(); err != nil {
		return Status{}, false, err
	}
	out := endOutcome(t)
	applied, err := m.e.createRaw(m.run, prefFrameEnd+fr.ID, []byte(out))
	if err != nil || !applied {
		return Status{}, true, err
	}
	m.e.log(m.run, Event{Kind: "end", Workflow: w.Path, Node: n.ID, Outcome: out, Detail: fr.ID})
	return Status{}, true, nil
}

// call runs a workflow node: its frame is named after the occurrence, so a
// re-entry of the node gets a fresh frame.
func (m *mover) call(fr *frame, cur *occurrence, n *Node) (Status, bool, error) {
	child := cur.ID
	if b, ok, err := m.e.store.Get(runKey(m.run, prefFrameEnd+child)); err != nil {
		return Status{}, false, err
	} else if ok {
		return m.finish(cur, string(b), "", nil)
	}
	if _, ok, err := m.e.store.Get(runKey(m.run, prefFrame+child)); err != nil {
		return Status{}, false, err
	} else if ok {
		return m.step(child)
	}
	if err := m.need(); err != nil {
		return Status{}, false, err
	}
	callee := m.e.set.Workflows[n.Workflow]
	if callee == nil {
		return Status{}, false, fmt.Errorf("engine: workflow %s is not loaded", n.Workflow)
	}
	cf := frame{ID: child, Workflow: callee.Path, Inputs: map[string]DataRef{}, Feedback: cur.Feedback}
	for _, in := range callee.Inputs {
		name := in
		if b, ok := n.With[in]; ok {
			name = b
		}
		ref, err := m.resolve(fr, name, cur.ID)
		if err != nil {
			return Status{}, false, fmt.Errorf("%s: node %s: input %q: %w", fr.Workflow, n.ID, in, err)
		}
		cf.Inputs[in] = ref
	}
	_, err := m.e.create(m.run, prefFrame+child, cf)
	return Status{}, true, err
}

// approval opens the gate once and then waits. The request is stored before
// anything else looks at it, so a repeated Advance returns the same hash
// instead of hashing content that may have moved on.
func (m *mover) approval(fr *frame, cur *occurrence, n *Node) (Status, bool, error) {
	var req GateRequest
	err := m.e.getJSON(m.run, prefGate+cur.ID, &req)
	if err == nil {
		return Status{Kind: StatusGate, Occurrence: cur.ID, Gate: &req}, false, nil
	}
	if !errors.Is(err, errMissing) {
		return Status{}, false, err
	}
	if err := m.need(); err != nil {
		return Status{}, false, err
	}
	var ref DataRef
	if n.Target == string(DiffFromBase) {
		ref = DataRef{Name: n.Target, Occurrence: cur.ID}
		if err := m.e.runner.Diff(m.ctx, m.run, DiffFromBase, "", ref); err != nil {
			return Status{}, false, err
		}
	} else if ref, err = m.resolve(fr, n.Target, cur.ID); err != nil {
		return Status{}, false, fmt.Errorf("%s: node %s: target: %w", fr.Workflow, n.ID, err)
	}
	content, err := m.e.runner.GetData(m.ctx, m.run, ref)
	if err != nil {
		return Status{}, false, err
	}
	sum := sha256.Sum256(content)
	req = GateRequest{Run: m.run, Occurrence: cur.ID, Gate: n.Gate, Target: n.Target, TargetHash: hex.EncodeToString(sum[:]), Subject: content}
	applied, err := m.e.create(m.run, prefGate+cur.ID, req)
	if err != nil || !applied {
		return Status{}, true, err
	}
	if err := m.e.runner.OpenGate(m.ctx, req); err != nil {
		return Status{}, false, err
	}
	m.e.log(m.run, Event{Kind: "gate-open", Occurrence: cur.ID, Workflow: cur.Workflow, Node: cur.Node, Detail: n.Gate})
	return Status{Kind: StatusGate, Occurrence: cur.ID, Gate: &req}, false, nil
}

// waiting returns the occurrence the run is waiting on, if it is of kind.
func (e *Engine) waiting(ctx context.Context, run RunID, id string, kind StatusKind) (Status, *occurrence, error) {
	st, err := e.walk(ctx, run, false)
	if err != nil {
		return Status{}, nil, fmt.Errorf("engine: occurrence %s is not waiting: %w", id, err)
	}
	if st.Kind != kind || st.Occurrence != id {
		return Status{}, nil, fmt.Errorf("engine: occurrence %s is not waiting for %s (run is at %s %s)", id, kind, st.Kind, st.Occurrence)
	}
	var o occurrence
	if err := e.getJSON(run, prefOcc+id, &o); err != nil {
		return Status{}, nil, err
	}
	return st, &o, nil
}

func (e *Engine) reportResult(ctx context.Context, run RunID, occ, outcome, feedback string) error {
	st, o, err := e.waiting(ctx, run, occ, StatusAgent)
	if err != nil {
		return err
	}
	n, err := e.nodeOf(o)
	if err != nil {
		return err
	}
	if n.Type == NodeQuestion {
		// The ask_human path (Answer during the task, then this report) is
		// not settled yet; see HANDOFF.md.
		return fmt.Errorf("engine: question node %s with a role: %w", n.ID, ErrNotImplemented)
	}
	a := st.Task.Agent
	if _, ok := a.Outcomes[outcome]; !ok {
		e.log(run, Event{Kind: "invalid", Occurrence: occ, Workflow: o.Workflow, Node: o.Node, Outcome: outcome, Detail: "undeclared outcome"})
		return fmt.Errorf("engine: %s does not declare outcome %q", a.Name, outcome)
	}
	res := result{Outcome: outcome, Feedback: feedback}
	if outcome == OutcomeDone {
		var reasons []string
		got := map[string][]byte{}
		for _, name := range o.Outputs {
			c, ok, err := e.runner.ReadOutput(ctx, run, occ, name)
			if err != nil {
				return err
			}
			if !ok {
				reasons = append(reasons, fmt.Sprintf("宣言した出力%qが書かれていない", name))
				continue
			}
			if err := e.validateData(name, c); err != nil {
				reasons = append(reasons, fmt.Sprintf("出力%qが不正: %v", name, err))
				continue
			}
			got[name] = c
		}
		if len(reasons) > 0 {
			detail := strings.Join(reasons, "; ")
			e.log(run, Event{Kind: "invalid", Occurrence: occ, Workflow: o.Workflow, Node: o.Node, Outcome: outcome, Detail: detail})
			snap, err := e.snapshot(ctx, run, o)
			if err != nil {
				return err
			}
			return e.record(run, o, result{Outcome: outcome, Invalid: true, Feedback: "前回の報告は受け付けられなかった: " + detail, Snapshot: snap})
		}
		for _, name := range o.Outputs {
			if err := e.runner.PutData(ctx, run, DataRef{Name: name, Occurrence: occ}, got[name]); err != nil {
				return err
			}
		}
		res.Outputs = o.Outputs
	}
	if res.Snapshot, err = e.snapshot(ctx, run, o); err != nil {
		return err
	}
	return e.record(run, o, res)
}

func (e *Engine) decide(ctx context.Context, run RunID, occ string, d Decision) error {
	st, o, err := e.waiting(ctx, run, occ, StatusGate)
	if err != nil {
		return err
	}
	switch d.Outcome {
	case OutcomeApproved:
		if d.TargetHash != st.Gate.TargetHash {
			return fmt.Errorf("engine: approval for %s was made against %q, but the gate is on %q", occ, d.TargetHash, st.Gate.TargetHash)
		}
	case OutcomeRejected:
	default:
		return fmt.Errorf("engine: gate %s: decision %q: %w", st.Gate.Gate, d.Outcome, ErrNotImplemented)
	}
	if err := e.record(run, o, result{Outcome: d.Outcome, Feedback: d.Comment}); err != nil {
		return err
	}
	e.log(run, Event{Kind: "decision", Occurrence: occ, Workflow: o.Workflow, Node: o.Node, Outcome: d.Outcome, Detail: d.Comment})
	return nil
}
