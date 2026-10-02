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
		if st, handled, moved, err := m.triage(); err != nil {
			return Status{}, err
		} else if handled {
			if !moved {
				return st, nil
			}
			continue
		}
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
	if fb, ok := m.recs.reenter[cur.ID]; ok {
		if err := m.need(); err != nil {
			return Status{}, false, err
		}
		if err := m.e.supersede(m.run, m.recs, cur.ID); err != nil {
			return Status{}, false, err
		}
		return m.enter(&fr, w, n.ID, fb)
	}
	if res := m.recs.results[cur.ID]; res != nil {
		if len(res.Deviation) > 0 {
			st, wait, moved, err := m.readOnlyDeviation(cur, res)
			if err != nil || wait || moved {
				return st, moved, err
			}
		}
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
	case NodeForeach:
		return m.foreach(&fr, cur, n)
	case NodeCommit:
		return m.commit(&fr, cur, n)
	case NodePublish:
		return m.publish(cur, n)
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
// max is reached. Entries are counted since the frame's last human decision
// (an approval, a deviation gate, or a triage gate): a human looking at the
// work restarts the budget.
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
			if m.recs.decided(o, w.Nodes[o.Node]) {
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
	if !occ.Exhausted {
		if err := m.prepareBase(fr, n, occ); err != nil {
			return Status{}, false, err
		}
		if n.Type == NodeForeach {
			if err := m.prepareItems(fr, n, occ); err != nil {
				var bad *errItems
				if errors.As(err, &bad) {
					return m.block("", w.Path, id, bad.msg)
				}
				return Status{}, false, err
			}
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
	var err error
	if occ.Inputs, err = m.frameInputs(fr, occ.ID); err != nil {
		return err
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
//
// Accumulated data is read run-wide whatever the frame bound: its value is
// everything written so far, and an empty array when nothing was.
func (m *mover) resolve(fr *frame, name, occ string) (DataRef, error) {
	if m.e.set.accumulates(name) {
		if o, ok := m.recs.latest[name]; ok {
			return DataRef{Name: name, Occurrence: o}, nil
		}
		ref := DataRef{Name: name, Occurrence: occ}
		if err := m.e.runner.PutData(m.ctx, m.run, ref, []byte("[]")); err != nil {
			return DataRef{}, err
		}
		return ref, nil
	}
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
		tree, err := m.iterationTree(fr)
		if err != nil {
			return DataRef{}, err
		}
		ref := DataRef{Name: name, Occurrence: occ}
		if err := m.e.runner.Diff(m.ctx, m.run, DiffFromRef, tree, ref); err != nil {
			return DataRef{}, err
		}
		return ref, nil
	}
	if slices.Contains(m.start.Inputs, name) {
		return DataRef{Name: name}, nil
	}
	return DataRef{}, fmt.Errorf("data %q is not available", name)
}

// frameInputs is what an agent or exec in fr receives from the frame's
// bindings. A binding of accumulated data to its own name is read again: it
// was fixed when the frame began, and writes since then belong in it too.
func (m *mover) frameInputs(fr *frame, occ string) (map[string]DataRef, error) {
	out := map[string]DataRef{}
	for k, v := range fr.Inputs {
		if v.Name == k && m.e.set.accumulates(k) {
			ref, err := m.resolve(fr, k, occ)
			if err != nil {
				return nil, err
			}
			v = ref
		}
		out[k] = v
	}
	return out, nil
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
	_, err := m.e.putResult(m.run, o, result{Outcome: outcome, Feedback: feedback, Outputs: outputs}, "")
	return Status{}, true, err
}

// putResult stores how o finished; detail goes into the finish event.
func (e *Engine) putResult(run RunID, o *occurrence, res result, detail string) (bool, error) {
	applied, err := e.create(run, prefResult+o.ID, res)
	if err != nil || !applied {
		return applied, err
	}
	e.log(run, Event{Kind: "finish", Occurrence: o.ID, Workflow: o.Workflow, Node: o.Node, Outcome: res.Outcome, Detail: detail})
	return true, nil
}

// feedbackLimit is how many characters of an agent's feedback the finish
// event carries: enough to tell why it ended, without copying a report
// into every log line.
const feedbackLimit = 200

func feedbackDetail(fb string) string {
	if r := []rune(fb); len(r) > feedbackLimit {
		return string(r[:feedbackLimit]) + "…"
	}
	return fb
}

// record stores a result reported from outside (an agent, a human); losing
// the race to another report is an error the reporter must see. detail goes
// into the finish event.
func (e *Engine) record(run RunID, o *occurrence, res result, detail string) error {
	applied, err := e.putResult(run, o, res, detail)
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
	cf := frame{ID: child, Workflow: callee.Path, Inputs: map[string]DataRef{}, Feedback: cur.Feedback, Parent: fr.ID}
	if err := m.bindInputs(fr, n, callee, cur.ID, &cf, ""); err != nil {
		return Status{}, false, err
	}
	_, err := m.e.create(m.run, prefFrame+child, cf)
	return Status{}, true, err
}

// bindInputs binds callee's inputs in cf from fr, through n's `with`. The
// input named skip is already bound (a foreach item).
func (m *mover) bindInputs(fr *frame, n *Node, callee *Workflow, occ string, cf *frame, skip string) error {
	for _, in := range callee.Inputs {
		if in == skip {
			continue
		}
		name := in
		if b, ok := n.With[in]; ok {
			name = b
		}
		ref, err := m.resolve(fr, name, occ)
		if err != nil {
			return fmt.Errorf("%s: node %s: input %q: %w", fr.Workflow, n.ID, in, err)
		}
		cf.Inputs[in] = ref
	}
	return nil
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
		ref = DataRef{Name: string(DiffCommitted), Occurrence: cur.ID}
		if err := m.e.runner.Diff(m.ctx, m.run, DiffCommitted, "", ref); err != nil {
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
	subject := content
	if n.Target == string(DiffFromBase) {
		if subject, err = m.withUnpublished(content); err != nil {
			return Status{}, false, err
		}
	}
	req = GateRequest{Run: m.run, Occurrence: cur.ID, Gate: n.Gate, Target: n.Target, TargetHash: hex.EncodeToString(sum[:]), Subject: subject}
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

// withUnpublished appends to a committed diff the files the worktree still
// changes, which publish will not land. The hash stays the committed diff's:
// the human approves what lands, and the list is only shown.
func (m *mover) withUnpublished(diff []byte) ([]byte, error) {
	files, _, err := m.e.runner.ChangedSince(m.ctx, m.run, "")
	if err != nil || len(files) == 0 {
		return diff, err
	}
	var b strings.Builder
	b.Write(diff)
	if len(diff) > 0 && diff[len(diff)-1] != '\n' {
		b.WriteByte('\n')
	}
	b.WriteString("\n## publishされない変更（未コミット）\n\n")
	for _, f := range files {
		b.WriteString(f + "\n")
	}
	return []byte(b.String()), nil
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
		var answers []byte
		if n.Type == NodeQuestion {
			var reason string
			if answers, reason, err = e.collectedAnswers(run, occ, n.Outputs[0]); err != nil {
				return err
			}
			if reason != "" {
				reasons = append(reasons, reason)
			}
		}
		if len(reasons) > 0 {
			detail := strings.Join(reasons, "; ")
			e.log(run, Event{Kind: "invalid", Occurrence: occ, Workflow: o.Workflow, Node: o.Node, Outcome: outcome, Detail: detail})
			res := result{Outcome: outcome, Invalid: true, Feedback: "前回の報告は受け付けられなかった: " + detail}
			if err := e.finishAgent(ctx, run, o, a, &res); err != nil {
				return err
			}
			return e.record(run, o, res, feedbackDetail(feedback))
		}
		recs, err := e.loadRecords(run)
		if err != nil {
			return err
		}
		if err := e.putOutputs(ctx, run, recs, occ, o.Outputs, got); err != nil {
			return err
		}
		res.Outputs = o.Outputs
		if n.Type == NodeQuestion {
			name := n.Outputs[0]
			if err := e.runner.PutData(ctx, run, DataRef{Name: name, Occurrence: occ}, answers); err != nil {
				return err
			}
			res.Outcome = OutcomeAnswered
			res.Outputs = append(slices.Clone(o.Outputs), name)
		}
	}
	if err := e.finishAgent(ctx, run, o, a, &res); err != nil {
		return err
	}
	return e.record(run, o, res, feedbackDetail(feedback))
}

// finishAgent takes the end-of-occurrence snapshot and, for an agent that
// cannot write, compares the worktree with how it was at entry.
func (e *Engine) finishAgent(ctx context.Context, run RunID, o *occurrence, a *Agent, res *result) error {
	if !a.WriteCapable() {
		files, hash, err := e.runner.ChangedSince(ctx, run, o.Base)
		if err != nil {
			return err
		}
		if hash != o.BaseHash && len(files) > 0 {
			res.Deviation, res.DeviationHash = files, hash
		}
	}
	snap, err := e.snapshot(ctx, run, o)
	res.Snapshot = snap
	return err
}

func (e *Engine) decide(ctx context.Context, run RunID, occ string, d Decision) error {
	st, o, err := e.waiting(ctx, run, occ, StatusGate)
	if err != nil {
		return err
	}
	switch st.Gate.Gate {
	case GateTriage:
		return e.decideTriage(run, occ, d)
	case GateDeviation:
		return e.decideDeviation(run, o, st.Gate, d)
	}
	res := result{Outcome: d.Outcome, Feedback: d.Comment}
	switch d.Outcome {
	case OutcomeApproved:
		if d.TargetHash != st.Gate.TargetHash {
			return fmt.Errorf("engine: approval for %s was made against %q, but the gate is on %q", occ, d.TargetHash, st.Gate.TargetHash)
		}
		if st.Gate.Target == string(DiffFromBase) {
			recs, err := e.loadRecords(run)
			if err != nil {
				return err
			}
			if id := recs.lastCommit(); id != "" {
				res.ApprovedCommit = recs.results[id].Commit
			}
		}
	case OutcomeRejected:
	default:
		return fmt.Errorf("engine: gate %s takes approved or rejected, not %q", st.Gate.Gate, d.Outcome)
	}
	if err := e.record(run, o, res, ""); err != nil {
		return err
	}
	e.log(run, Event{Kind: "decision", Occurrence: occ, Workflow: o.Workflow, Node: o.Node, Outcome: d.Outcome, Detail: d.Comment})
	return nil
}
