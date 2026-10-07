// Package contract holds the engine's contract tests: the definition of done
// for each work order in docs/work-orders.md. They drive the engine only
// through engine/api.go, with an in-memory Store and a recording Runner.
//
// Owned by the supervisor. Implementers do not edit assertions; if a test is
// wrong, say so in HANDOFF.md.
package contract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/TadahiroYamamura/masuda-engine/engine"
)

// ---------------------------------------------------------------------------
// Test doubles
// ---------------------------------------------------------------------------

type memStore struct {
	mu sync.Mutex
	m  map[string][]byte
}

func newStore() *memStore { return &memStore{m: map[string][]byte{}} }

func (s *memStore) Get(k string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[k]
	return v, ok, nil
}
func (s *memStore) Put(k string, v []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[k] = append([]byte(nil), v...)
	return nil
}
func (s *memStore) Delete(k string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, k)
	return nil
}
func (s *memStore) List(prefix string) ([]engine.KV, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []engine.KV
	for k, v := range s.m {
		if strings.HasPrefix(k, prefix) {
			out = append(out, engine.KV{Key: k, Value: v})
		}
	}
	return out, nil
}
func (s *memStore) Apply(ops []engine.Op) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, op := range ops {
		if op.Kind == engine.OpCheck {
			cur, ok := s.m[op.Key]
			if (op.Value == nil) != !ok || (ok && string(cur) != string(op.Value)) {
				return false, nil
			}
		}
	}
	for _, op := range ops {
		switch op.Kind {
		case engine.OpPut:
			s.m[op.Key] = append([]byte(nil), op.Value...)
		case engine.OpDelete:
			delete(s.m, op.Key)
		}
	}
	return true, nil
}

// stub records every Runner call and answers from queues the test fills.
type stub struct {
	mu       sync.Mutex
	calls    []string // "Method(detail)"
	data     map[string][]byte
	outputs  map[string][]byte // "<occ>/<name>" -> content the agent "wrote"
	cmdQ     []engine.CommandResult
	items    map[string][]engine.Item
	changed  []string // what ChangedSince reports
	commitQ  []engine.CommitResult
	gates    []engine.GateRequest
	qs       []engine.QuestionRequest
	publish  []engine.PublishRequest
	policies []engine.Policy
	events   []engine.Event
	snapN    int
}

func newStub() *stub {
	return &stub{data: map[string][]byte{}, outputs: map[string][]byte{}, items: map[string][]engine.Item{}}
}

func (s *stub) rec(f string, a ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, fmt.Sprintf(f, a...))
}
func (s *stub) key(r engine.DataRef) string { return r.Occurrence + "/" + r.Name }

func (s *stub) SetPolicy(_ context.Context, _ engine.RunID, p engine.Policy) error {
	s.rec("SetPolicy(%v,%v)", p.Egress, p.Secrets)
	s.mu.Lock()
	s.policies = append(s.policies, p)
	s.mu.Unlock()
	return nil
}
func (s *stub) RunCommand(_ context.Context, t engine.CommandTask) (engine.CommandResult, error) {
	s.rec("RunCommand(%s,%v)", t.Node, t.Command)
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.cmdQ) == 0 {
		return engine.CommandResult{ExitCode: 0}, nil
	}
	r := s.cmdQ[0]
	s.cmdQ = s.cmdQ[1:]
	return r, nil
}
func (s *stub) RunPrivileged(_ context.Context, t engine.PrivilegedTask) (engine.CommandResult, error) {
	s.rec("RunPrivileged(%s,%s)", t.Node, t.Name)
	return engine.CommandResult{}, nil
}
func (s *stub) ReadOutput(_ context.Context, _ engine.RunID, occ, name string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.outputs[occ+"/"+name]
	if !ok {
		v, ok = s.outputs["*/"+name] // "whatever the next agent writes"
	}
	return v, ok, nil
}
func (s *stub) PutData(_ context.Context, _ engine.RunID, ref engine.DataRef, c []byte) error {
	s.rec("PutData(%s)", ref.Name)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[s.key(ref)] = c
	s.data["latest/"+ref.Name] = c
	return nil
}
func (s *stub) GetData(_ context.Context, _ engine.RunID, ref engine.DataRef) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.data[s.key(ref)]; ok {
		return v, nil
	}
	if v, ok := s.data["latest/"+ref.Name]; ok {
		return v, nil
	}
	return nil, errors.New("no data " + ref.Name)
}
func (s *stub) Snapshot(_ context.Context, _ engine.RunID, occ string) (engine.SnapshotRef, error) {
	s.rec("Snapshot(%s)", occ)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapN++
	return engine.SnapshotRef(fmt.Sprintf("snap-%d", s.snapN)), nil
}
func (s *stub) Diff(_ context.Context, _ engine.RunID, kind engine.DiffKind, from engine.SnapshotRef, into engine.DataRef) error {
	s.rec("Diff(%s)", kind)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[s.key(into)] = []byte("--- a\n+++ b\n")
	s.data["latest/"+into.Name] = s.data[s.key(into)]
	return nil
}
func (s *stub) ChangedSince(_ context.Context, _ engine.RunID, from engine.SnapshotRef) ([]string, string, error) {
	s.rec("ChangedSince(%s)", from)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.changed, "hash-" + strings.Join(s.changed, ","), nil
}
func (s *stub) Items(_ context.Context, _ engine.RunID, over string, from engine.DataRef) ([]engine.Item, error) {
	s.rec("Items(%s)", over)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.items[over], nil
}
func (s *stub) OpenGate(_ context.Context, g engine.GateRequest) error {
	s.rec("OpenGate(%s)", g.Gate)
	s.mu.Lock()
	s.gates = append(s.gates, g)
	s.mu.Unlock()
	return nil
}
func (s *stub) OpenQuestion(_ context.Context, q engine.QuestionRequest) error {
	s.rec("OpenQuestion(%d)", len(q.Questions))
	s.mu.Lock()
	s.qs = append(s.qs, q)
	s.mu.Unlock()
	return nil
}
func (s *stub) Commit(_ context.Context, c engine.CommitRequest) (engine.CommitResult, error) {
	s.rec("Commit(%s,%v)", c.Scope, c.Allowed)
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.commitQ) == 0 {
		return engine.CommitResult{Commit: "c0ffee"}, nil
	}
	r := s.commitQ[0]
	s.commitQ = s.commitQ[1:]
	return r, nil
}
func (s *stub) Publish(_ context.Context, p engine.PublishRequest) error {
	s.rec("Publish(%s,%s)", p.Target, p.Commit)
	s.mu.Lock()
	s.publish = append(s.publish, p)
	s.mu.Unlock()
	return nil
}
func (s *stub) Discard(_ context.Context, _ engine.RunID, export []string) error {
	s.rec("Discard(%v)", export)
	return nil
}
func (s *stub) Log(e engine.Event) {
	s.mu.Lock()
	s.events = append(s.events, e)
	s.mu.Unlock()
}

func (s *stub) has(prefix string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func (s *stub) count(prefix string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func agentMD(name string, tools string, outputs []string, outcomes ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "---\nname: %s\ndescription: test agent %s\n", name, name)
	if tools != "" {
		fmt.Fprintf(&b, "tools: %s\n", tools)
	}
	if len(outputs) > 0 {
		fmt.Fprintf(&b, "outputs: [%s]\n", strings.Join(outputs, ", "))
	}
	b.WriteString("outcomes:\n  done: finished\n")
	for _, o := range outcomes {
		fmt.Fprintf(&b, "  %s: %s\n", o, o)
	}
	b.WriteString("---\nDo the thing.\n")
	return b.String()
}

func repoFS(files map[string]string) fstest.MapFS {
	m := fstest.MapFS{}
	for k, v := range files {
		m[k] = &fstest.MapFile{Data: []byte(v)}
	}
	return m
}

func mustLoad(t *testing.T, repo fstest.MapFS) *engine.Set {
	t.Helper()
	set, err := engine.Load(repo, engine.Bundled())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return set
}

func mustCheck(t *testing.T, set *engine.Set, root string) {
	t.Helper()
	if p := set.Check(root); len(p) != 0 {
		t.Fatalf("Check(%s): %+v", root, p)
	}
}

func newEngine(t *testing.T, set *engine.Set, st *stub) (*engine.Engine, *memStore) {
	t.Helper()
	store := newStore()
	clock := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	e := engine.New(set, store, st, engine.Options{Now: func() time.Time { clock = clock.Add(time.Second); return clock }})
	return e, store
}

func advance(t *testing.T, e *engine.Engine, run engine.RunID) engine.Status {
	t.Helper()
	s, err := e.Advance(context.Background(), run)
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	return s
}

func wantAgent(t *testing.T, s engine.Status, role string) *engine.AgentTask {
	t.Helper()
	if s.Kind != engine.StatusAgent || s.Task == nil {
		t.Fatalf("want agent task, got %+v", s)
	}
	if s.Task.Agent == nil || s.Task.Agent.Name != role {
		t.Fatalf("want role %s, got %+v", role, s.Task.Agent)
	}
	return s.Task
}

// ---------------------------------------------------------------------------
// C-E1: loading
// ---------------------------------------------------------------------------

func TestCE1_LoadBundledAndOverride(t *testing.T) {
	set := mustLoad(t, repoFS(nil))
	if len(set.Workflows) == 0 || len(set.Agents) == 0 {
		t.Fatalf("bundled set is empty: %d workflows, %d agents", len(set.Workflows), len(set.Agents))
	}
	for p, o := range set.Origins {
		if o != engine.OriginBundled {
			t.Fatalf("%s origin = %s, want bundled", p, o)
		}
	}
	// A repo file at the same path replaces the bundled one whole.
	var anyAgent string
	for name := range set.Agents {
		anyAgent = name
		break
	}
	repo := repoFS(map[string]string{"agents/" + anyAgent + ".md": agentMD(anyAgent, "Read", nil, "custom")})
	set2 := mustLoad(t, repo)
	if set2.Origins["agents/"+anyAgent] != engine.OriginRepo {
		t.Fatalf("override not recorded as repo origin")
	}
	if _, ok := set2.Agents[anyAgent].Outcomes["custom"]; !ok {
		t.Fatalf("override did not replace the agent definition")
	}
}

func TestCE1_RejectsMalformedDefinitions(t *testing.T) {
	cases := map[string]map[string]string{
		"unknown key on approval": {"workflows/x.yaml": "version: 1\nstart: a\nnodes:\n  a: {type: approval, gate: g, target: plan, role: nope, next: {approved: end, rejected: end}}\n"},
		"relative exec command":   {"workflows/x.yaml": "version: 1\nstart: a\nnodes:\n  a: {type: exec, command: [python3, x.py], next: {done: end, failed: end}}\n"},
		"agent without done":      {"agents/bad.md": "---\nname: bad\ndescription: d\noutcomes:\n  ok: fine\n---\nbody\n"},
		"reserved outcome":        {"agents/bad.md": "---\nname: bad\ndescription: d\noutcomes:\n  done: d\n  exhausted: no\n---\nbody\n"},
		"engine data as output":   {"agents/bad.md": "---\nname: bad\ndescription: d\noutputs: [diff]\noutcomes:\n  done: d\n---\nbody\n"},
		"bad node name":           {"workflows/x.yaml": "version: 1\nstart: A_1\nnodes:\n  A_1: {type: discard, next: end}\n"},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := engine.Load(repoFS(files), engine.Bundled()); err == nil {
				t.Fatalf("Load accepted a malformed definition")
			}
		})
	}
}

func TestCE1_WriteCapableIsDecidedFromTools(t *testing.T) {
	repo := repoFS(map[string]string{
		"agents/ro.md":  agentMD("ro", "Read, Grep, Glob", nil),
		"agents/rw.md":  agentMD("rw", "Read, Edit", nil),
		"agents/all.md": agentMD("all", "", nil),
	})
	set := mustLoad(t, repo)
	if set.Agents["ro"].WriteCapable() || !set.Agents["rw"].WriteCapable() || !set.Agents["all"].WriteCapable() {
		t.Fatalf("WriteCapable: ro=%v rw=%v all=%v", set.Agents["ro"].WriteCapable(), set.Agents["rw"].WriteCapable(), set.Agents["all"].WriteCapable())
	}
}

// ---------------------------------------------------------------------------
// C-E2: checking
// ---------------------------------------------------------------------------

func TestCE2_CheckFindsCrossFileProblems(t *testing.T) {
	cases := map[string]map[string]string{
		"unrouted outcome": {
			"agents/a.md":      agentMD("a", "Read", nil, "retry"),
			"workflows/x.yaml": "version: 1\nstart: a\nnodes:\n  a: {type: agent, role: agents/a, next: {done: end}}\n",
		},
		"route for undeclared outcome": {
			"agents/a.md":      agentMD("a", "Read", nil),
			"workflows/x.yaml": "version: 1\nstart: a\nnodes:\n  a: {type: agent, role: agents/a, next: {done: end, nope: end}}\n",
		},
		"missing role": {
			"workflows/x.yaml": "version: 1\nstart: a\nnodes:\n  a: {type: agent, role: agents/missing, next: end}\n",
		},
		"unbounded cycle": {
			"workflows/x.yaml": "version: 1\nstart: a\nnodes:\n  a: {type: workflow, workflow: workflows/y, next: b}\n  b: {type: workflow, workflow: workflows/y, next: a}\n",
			"workflows/y.yaml": "version: 1\nstart: d\nnodes:\n  d: {type: discard, next: end}\n",
		},
		"write before plan approval": {
			"agents/w.md":      agentMD("w", "Read, Edit, Bash", nil),
			"workflows/x.yaml": "version: 1\nstart: w\nnodes:\n  w: {type: agent, role: agents/w, next: end}\n",
		},
		"input never provided": {
			"agents/a.md":      agentMD("a", "Read", nil),
			"workflows/x.yaml": "version: 1\nstart: a\nnodes:\n  a: {type: agent, role: agents/a, inputs: [ghost], next: end}\n",
		},
		"reserved gate name": {
			"workflows/x.yaml": "version: 1\nstart: g\nnodes:\n  g: {type: approval, gate: triage, target: plan, next: {approved: end, rejected: end}}\n",
		},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			set, err := engine.Load(repoFS(files), engine.Bundled())
			if err != nil {
				t.Fatalf("Load: %v (shape errors belong to C-E1)", err)
			}
			if p := set.Check("workflows/x"); len(p) == 0 {
				t.Fatalf("Check found nothing")
			}
		})
	}
}

func TestCE2_ReachableAndMermaid(t *testing.T) {
	repo := repoFS(map[string]string{
		"agents/a.md":      agentMD("a", "Read", []string{"out"}),
		"workflows/x.yaml": "version: 1\nstart: a\nnodes:\n  a: {type: agent, role: agents/a, next: g}\n  g: {type: approval, gate: review, target: out, next: {approved: y, rejected: a}}\n  y: {type: workflow, workflow: workflows/y, next: end}\n",
		"workflows/y.yaml": "version: 1\nstart: d\nnodes:\n  d: {type: discard, next: end}\n",
	})
	set := mustLoad(t, repo)
	mustCheck(t, set, "workflows/x")
	reach, err := set.Reachable("workflows/x")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"workflows/x": true, "workflows/y": true, "agents/a": true}
	for _, r := range reach {
		delete(want, r)
	}
	if len(want) != 0 {
		t.Fatalf("Reachable missing %v (got %v)", want, reach)
	}
	mm, err := set.Mermaid("workflows/x")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"triage", "deviation", "approval", "review"} {
		if !strings.Contains(mm, s) {
			t.Fatalf("Mermaid lacks %q:\n%s", s, mm)
		}
	}
}

// ---------------------------------------------------------------------------
// C-E3: core loop
// ---------------------------------------------------------------------------

const linearWF = `version: 1
inputs: [instructions]
start: plan
nodes:
  plan:
    type: agent
    role: agents/planner
    inputs: [instructions]
    outputs: [plan]
    max: 2
    next: {done: approve, redo: plan}
  approve:
    type: approval
    gate: plan
    target: plan
    next: {approved: finish, rejected: plan}
  finish:
    type: discard
    export: [plan]
    next: end
`

func linearSet(t *testing.T) *engine.Set {
	set := mustLoad(t, repoFS(map[string]string{
		"agents/planner.md": agentMD("planner", "Read, Grep", []string{"plan"}, "redo"),
		"workflows/x.yaml":  linearWF,
	}))
	mustCheck(t, set, "workflows/x")
	return set
}

const validPlan = `{"goal":"g","summary":"do it","steps":[{"number":1,"title":"t1","description":"one","tests":[],"files":["a.go"]}],"alternatives":[],"risks":[],"expected_byproducts":[],"checks":[]}`

func startLinear(t *testing.T) (*engine.Engine, *stub, engine.RunID) {
	set := linearSet(t)
	st := newStub()
	e, _ := newEngine(t, set, st)
	run := engine.RunID("ws1")
	ctx := context.Background()
	if err := st.PutData(ctx, run, engine.DataRef{Name: "instructions"}, []byte("build x")); err != nil {
		t.Fatal(err)
	}
	if err := e.Start(ctx, run, "workflows/x", []string{"instructions"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return e, st, run
}

func TestCE3_AgentTaskThenApprovalThenEnd(t *testing.T) {
	e, st, run := startLinear(t)
	ctx := context.Background()

	s := advance(t, e, run)
	task := wantAgent(t, s, "planner")
	if task.Inputs["instructions"].Name != "instructions" || len(task.Outputs) != 1 || task.Outputs[0] != "plan" {
		t.Fatalf("task wiring: %+v", task)
	}
	if !st.has("SetPolicy(") {
		t.Fatalf("SetPolicy must be called before an agent task")
	}
	// Idempotent while waiting.
	if s2 := advance(t, e, run); s2.Occurrence != s.Occurrence || s2.Kind != engine.StatusAgent {
		t.Fatalf("Advance not idempotent: %+v vs %+v", s, s2)
	}

	st.outputs[task.Occurrence+"/plan"] = []byte(validPlan)
	if err := e.ReportResult(ctx, run, task.Occurrence, "done", ""); err != nil {
		t.Fatalf("ReportResult: %v", err)
	}
	if !st.has("PutData(plan)") {
		t.Fatalf("accepted output must be stored via PutData")
	}

	s = advance(t, e, run)
	if s.Kind != engine.StatusGate || s.Gate == nil || s.Gate.Gate != "plan" || s.Gate.TargetHash == "" {
		t.Fatalf("want plan gate with hash, got %+v", s)
	}
	gateOcc := s.Occurrence

	// Wrong hash: refused, still waiting.
	if err := e.Decide(ctx, run, gateOcc, engine.Decision{Outcome: "approved", TargetHash: "nope"}); err == nil {
		t.Fatalf("approval with wrong hash was accepted")
	}
	if err := e.Decide(ctx, run, gateOcc, engine.Decision{Outcome: "approved", TargetHash: s.Gate.TargetHash}); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	s = advance(t, e, run)
	if s.Kind != engine.StatusDone || s.Outcome != "done" {
		t.Fatalf("want done, got %+v", s)
	}
	if !st.has("Discard([plan])") {
		t.Fatalf("discard node must call Runner.Discard with export")
	}
}

func TestCE3_ReportResultValidation(t *testing.T) {
	e, st, run := startLinear(t)
	ctx := context.Background()
	task := wantAgent(t, advance(t, e, run), "planner")

	if err := e.ReportResult(ctx, run, task.Occurrence, "banana", ""); err == nil {
		t.Fatalf("undeclared outcome accepted")
	}
	if err := e.ReportResult(ctx, run, "9999", "done", ""); err == nil {
		t.Fatalf("result for a non-waiting occurrence accepted")
	}
	// done without the declared output: re-enter the same node with feedback.
	if err := e.ReportResult(ctx, run, task.Occurrence, "done", ""); err != nil {
		t.Fatalf("missing output must not be a hard error (it re-enters): %v", err)
	}
	s := advance(t, e, run)
	t2 := wantAgent(t, s, "planner")
	if t2.Occurrence == task.Occurrence || t2.Feedback == "" {
		t.Fatalf("expected re-entry with feedback, got %+v", t2)
	}
	// Output that fails the schema: also re-enter.
	st.outputs[t2.Occurrence+"/plan"] = []byte(`{"not":"a plan"}`)
	_ = e.ReportResult(ctx, run, t2.Occurrence, "done", "")
	s = advance(t, e, run)
	// max: 2 → the third entry is exhausted, and exhausted has no route → blocked.
	if s.Kind != engine.StatusBlocked {
		t.Fatalf("want blocked after exhausting max, got %+v", s)
	}
	if st.count("SetPolicy(") < 2 {
		t.Fatalf("SetPolicy once per agent entry")
	}
}

func TestCE3_RejectionCarriesFeedbackAndCountsEntries(t *testing.T) {
	e, st, run := startLinear(t)
	ctx := context.Background()
	task := wantAgent(t, advance(t, e, run), "planner")
	st.outputs["*/plan"] = []byte(validPlan)
	_ = e.ReportResult(ctx, run, task.Occurrence, "done", "")
	s := advance(t, e, run)
	if err := e.Decide(ctx, run, s.Occurrence, engine.Decision{Outcome: "rejected", Comment: "split step 1"}); err != nil {
		t.Fatal(err)
	}
	t2 := wantAgent(t, advance(t, e, run), "planner")
	if !strings.Contains(t2.Feedback, "split step 1") {
		t.Fatalf("rejection comment must reach the next task as feedback: %q", t2.Feedback)
	}
	_ = e.ReportResult(ctx, run, t2.Occurrence, "done", "")
	s = advance(t, e, run)
	_ = e.Decide(ctx, run, s.Occurrence, engine.Decision{Outcome: "rejected"})
	// Entry count resets after a human decision (ADR-0067 semantics kept):
	// the planner may run again instead of being exhausted.
	if s3 := advance(t, e, run); s3.Kind != engine.StatusAgent {
		t.Fatalf("after a gate decision the counter restarts; got %+v", s3)
	}
}

// ---------------------------------------------------------------------------
// C-E4: exec, question, policy, snapshot
// ---------------------------------------------------------------------------

const execWF = `version: 1
inputs: [instructions]
start: fetch
nodes:
  fetch:
    type: exec
    command: ["/usr/bin/python3", "/workspace/fetch.py"]
    outputs: [task]
    egress: [api.linear.app]
    secrets: [LINEAR_API_KEY]
    timeout: 2m
    max: 2
    next: {done: ask, failed: fetch, exhausted: end:fetch_failed}
  ask:
    type: question
    questions:
      - {id: scope, text: "proceed?", options: [yes, split]}
    outputs: [answers]
    next: {answered: finish}
  finish:
    type: discard
    export: [task, answers]
    next: end
`

func TestCE4_ExecNodePolicyOutputsAndFailure(t *testing.T) {
	set := mustLoad(t, repoFS(map[string]string{"workflows/x.yaml": execWF}))
	mustCheck(t, set, "workflows/x")
	st := newStub()
	e, _ := newEngine(t, set, st)
	run := engine.RunID("ws2")
	ctx := context.Background()
	_ = st.PutData(ctx, run, engine.DataRef{Name: "instructions"}, []byte("x"))
	if err := e.Start(ctx, run, "workflows/x", []string{"instructions"}); err != nil {
		t.Fatal(err)
	}
	st.cmdQ = []engine.CommandResult{
		{ExitCode: 1, LogTail: "boom"},
		{ExitCode: 0, Outputs: map[string][]byte{"task": []byte(`{"id":"LIN-1"}`)}},
	}
	s := advance(t, e, run)
	if s.Kind != engine.StatusQuestion || s.Question == nil || len(s.Question.Questions) != 1 || s.Question.Questions[0].ID != "scope" {
		t.Fatalf("want question after exec retry, got %+v (calls %v)", s, st.calls)
	}
	if st.count("RunCommand(fetch") != 2 {
		t.Fatalf("exec failed once then succeeded: want 2 RunCommand, got %d", st.count("RunCommand(fetch"))
	}
	if len(st.policies) < 2 || st.policies[0].Egress[0] != "api.linear.app" || st.policies[0].Secrets[0] != "LINEAR_API_KEY" {
		t.Fatalf("exec node policy must be set from egress/secrets: %+v", st.policies)
	}
	if string(st.data["latest/task"]) != `{"id":"LIN-1"}` {
		t.Fatalf("exec output not stored")
	}
	if err := e.Answer(ctx, run, s.Occurrence, engine.Answer{Answers: map[string]string{"scope": "yes"}}); err != nil {
		t.Fatal(err)
	}
	s = advance(t, e, run)
	if s.Kind != engine.StatusDone {
		t.Fatalf("want done, got %+v", s)
	}
	var ans map[string]string
	if err := json.Unmarshal(st.data["latest/answers"], &ans); err != nil || ans["scope"] != "yes" {
		t.Fatalf("answers must be stored as JSON id->answer: %s", st.data["latest/answers"])
	}
	if !st.has("Snapshot(") {
		t.Fatalf("a snapshot is taken at node boundaries")
	}
}

// ---------------------------------------------------------------------------
// C-E5: foreach, commit, deviation, publish
// ---------------------------------------------------------------------------

const buildWF = `version: 1
inputs: [instructions]
start: plan
nodes:
  plan: {type: agent, role: agents/planner, inputs: [instructions], outputs: [plan], next: approve}
  approve: {type: approval, gate: plan, target: plan, next: {approved: steps, rejected: plan}}
  steps:
    type: foreach
    over: steps
    body: workflows/step
    on_incomplete: continue
    next: {done: review, incomplete: review}
  review: {type: agent, role: agents/reviewer, outputs: [findings], next: approve-review}
  approve-review: {type: approval, gate: review, target: diff, next: {approved: publish, rejected: plan}}
  publish: {type: publish, target: local, export: [findings], next: end}
`

const stepWF = `version: 1
inputs: [step]
start: implement
nodes:
  implement: {type: agent, role: agents/implementer, inputs: [step], outputs: [commit-message], next: approve-step}
  approve-step: {type: approval, gate: interim, target: step-diff, next: {approved: commit, rejected: implement}}
  commit: {type: commit, scope: step, next: {done: end, rejected: implement}}
`

func buildSet(t *testing.T) *engine.Set {
	set := mustLoad(t, repoFS(map[string]string{
		"agents/planner.md":     agentMD("planner", "Read", []string{"plan"}),
		"agents/implementer.md": agentMD("implementer", "Read, Edit, Bash", []string{"commit-message"}),
		"agents/reviewer.md":    agentMD("reviewer", "Read, Grep", []string{"findings"}),
		"workflows/x.yaml":      buildWF,
		"workflows/step.yaml":   stepWF,
	}))
	mustCheck(t, set, "workflows/x")
	return set
}

func TestCE5_ForeachStepsCommitDeviationPublish(t *testing.T) {
	set := buildSet(t)
	st := newStub()
	e, _ := newEngine(t, set, st)
	run := engine.RunID("ws3")
	ctx := context.Background()
	_ = st.PutData(ctx, run, engine.DataRef{Name: "instructions"}, []byte("x"))
	if err := e.Start(ctx, run, "workflows/x", []string{"instructions"}); err != nil {
		t.Fatal(err)
	}
	plan := `{"goal":"g","summary":"s","steps":[{"number":1,"title":"t1","description":"a","tests":[],"files":["a.go"]},{"number":2,"title":"t2","description":"b","tests":[],"files":["b.go"]}],"alternatives":[],"risks":[],"expected_byproducts":["go.sum"],"checks":[]}`
	st.outputs["*/plan"] = []byte(plan)
	st.outputs["*/commit-message"] = []byte("feat: step")
	st.outputs["*/findings"] = []byte(`[]`)
	st.items["steps"] = []engine.Item{
		{Key: "1", Input: "step", Content: []byte(`{"number":1,"title":"t1","description":"a","tests":[],"files":["a.go"]}`)},
		{Key: "2", Input: "step", Content: []byte(`{"number":2,"title":"t2","description":"b","tests":[],"files":["b.go"]}`)},
	}

	task := wantAgent(t, advance(t, e, run), "planner")
	_ = e.ReportResult(ctx, run, task.Occurrence, "done", "")
	s := advance(t, e, run)
	_ = e.Decide(ctx, run, s.Occurrence, engine.Decision{Outcome: "approved", TargetHash: s.Gate.TargetHash})

	// Step 1: implementer, then the interim gate decides on what the commit
	// will contain (step-diff), then commit with a deviation (c.go).
	t1 := wantAgent(t, advance(t, e, run), "implementer")
	if string(mustGet(t, st, t1.Inputs["step"])) == "" {
		t.Fatalf("step item must be passed as input")
	}
	st.changed = []string{"a.go", "c.go", "go.sum"}
	_ = e.ReportResult(ctx, run, t1.Occurrence, "done", "")
	s = advance(t, e, run)
	if s.Kind != engine.StatusGate || s.Gate.Gate != "interim" || s.Gate.Target != "step-diff" {
		t.Fatalf("want interim gate on step-diff, got %+v", s)
	}
	if !st.has("Diff(step-diff)") || strings.Contains(string(s.Gate.Subject), "publishされない") {
		t.Fatalf("a target: step-diff approval decides on HEAD..worktree with no unpublished list; calls=%v subject=%s", st.calls, s.Gate.Subject)
	}
	_ = e.Decide(ctx, run, s.Occurrence, engine.Decision{Outcome: "approved", TargetHash: s.Gate.TargetHash})
	s = advance(t, e, run)
	if s.Kind != engine.StatusGate || s.Gate.Gate != engine.GateDeviation {
		t.Fatalf("want deviation gate for c.go, got %+v", s)
	}
	if strings.Contains(string(s.Gate.Subject), "go.sum") || !strings.Contains(string(s.Gate.Subject), "c.go") {
		t.Fatalf("byproducts are not deviations; subject=%s", s.Gate.Subject)
	}
	_ = e.Decide(ctx, run, s.Occurrence, engine.Decision{Outcome: "approved", TargetHash: s.Gate.TargetHash, ApprovedFiles: []string{"c.go"}})
	// Step 2: no deviation; c.go is now allowed.
	st.changed = []string{"b.go", "c.go"}
	t2 := wantAgent(t, advance(t, e, run), "implementer")
	_ = e.ReportResult(ctx, run, t2.Occurrence, "done", "")
	s = advance(t, e, run)
	if s.Kind != engine.StatusGate || s.Gate.Gate != "interim" {
		t.Fatalf("want interim gate for step 2, got %+v", s)
	}
	_ = e.Decide(ctx, run, s.Occurrence, engine.Decision{Outcome: "approved", TargetHash: s.Gate.TargetHash})
	rv := wantAgent(t, advance(t, e, run), "reviewer")
	if st.count("Commit(step") != 2 {
		t.Fatalf("want 2 step commits, got %d (calls %v)", st.count("Commit(step"), st.calls)
	}
	_ = e.ReportResult(ctx, run, rv.Occurrence, "done", "")
	// Before the review gate opens, leave something uncommitted in the tree:
	// the gate must decide on the committed diff and merely list this.
	st.changed = []string{"scratch.txt"}
	s = advance(t, e, run)
	if s.Kind != engine.StatusGate || s.Gate.Gate != "review" || s.Gate.Target != "diff" {
		t.Fatalf("want review gate on diff, got %+v", s)
	}
	if !st.has("Diff(committed-diff)") {
		t.Fatalf("a target: diff approval decides on the committed diff (DiffCommitted), calls=%v", st.calls)
	}
	subj := string(s.Gate.Subject)
	if !strings.HasPrefix(subj, "--- a") || !strings.Contains(subj, "publishされない") || !strings.Contains(subj, "scratch.txt") {
		t.Fatalf("subject must be the committed diff followed by the uncommitted files under a 'publishされない' heading:\n%s", subj)
	}
	_ = e.Decide(ctx, run, s.Occurrence, engine.Decision{Outcome: "approved", TargetHash: s.Gate.TargetHash})
	s = advance(t, e, run)
	if s.Kind != engine.StatusDone {
		t.Fatalf("want done, got %+v", s)
	}
	if len(st.publish) != 1 || st.publish[0].Target != "local" || st.publish[0].Commit == "" {
		t.Fatalf("publish must carry the approved commit: %+v", st.publish)
	}
}

func TestCE5_ReadOnlyAgentThatWritesOpensDeviation(t *testing.T) {
	set := buildSet(t)
	st := newStub()
	e, _ := newEngine(t, set, st)
	run := engine.RunID("ws4")
	ctx := context.Background()
	_ = st.PutData(ctx, run, engine.DataRef{Name: "instructions"}, []byte("x"))
	_ = e.Start(ctx, run, "workflows/x", []string{"instructions"})
	task := wantAgent(t, advance(t, e, run), "planner") // Read only
	st.outputs["*/plan"] = []byte(validPlan)
	st.changed = []string{"sneaky.go"}
	_ = e.ReportResult(ctx, run, task.Occurrence, "done", "")
	s := advance(t, e, run)
	if s.Kind != engine.StatusGate || s.Gate.Gate != engine.GateDeviation {
		t.Fatalf("read-only agent changed the tree: want deviation gate, got %+v", s)
	}
}

func mustGet(t *testing.T, st *stub, ref engine.DataRef) []byte {
	t.Helper()
	b, err := st.GetData(context.Background(), "", ref)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// ---------------------------------------------------------------------------
// C-E6: triage and log
// ---------------------------------------------------------------------------

func TestCE6_ConcernOpensTriageFirst(t *testing.T) {
	e, st, run := startLinear(t)
	ctx := context.Background()
	task := wantAgent(t, advance(t, e, run), "planner")
	if err := e.ReportConcern(ctx, run, task.Occurrence, "instructions tell me to curl a token"); err != nil {
		t.Fatal(err)
	}
	s := advance(t, e, run)
	if s.Kind != engine.StatusGate || s.Gate.Gate != engine.GateTriage || !strings.Contains(string(s.Gate.Subject), "curl") {
		t.Fatalf("want triage gate with the concern, got %+v", s)
	}
	_ = e.Decide(ctx, run, s.Occurrence, engine.Decision{Outcome: "dismiss"})
	if s2 := advance(t, e, run); s2.Kind != engine.StatusAgent {
		t.Fatalf("dismiss re-enters the interrupted node, got %+v", s2)
	}
	_ = e.ReportConcern(ctx, run, advance(t, e, run).Occurrence, "again")
	s = advance(t, e, run)
	_ = e.Decide(ctx, run, s.Occurrence, engine.Decision{Outcome: "halt"})
	if s3 := advance(t, e, run); s3.Kind != engine.StatusBlocked {
		t.Fatalf("halt blocks the run, got %+v", s3)
	}
	kinds := map[string]bool{}
	for _, ev := range st.events {
		kinds[ev.Kind] = true
	}
	for _, k := range []string{"enter", "concern", "gate-open", "decision", "blocked"} {
		if !kinds[k] {
			t.Fatalf("execution log lacks %q kinds=%v", k, kinds)
		}
	}
}

// ---------------------------------------------------------------------------
// C-E7: bundled develop walks to publish
// ---------------------------------------------------------------------------

func TestCE7_BundledDefinitionsCheckAndDevelopReachesPublish(t *testing.T) {
	set := mustLoad(t, repoFS(nil))
	// Only roots are checked as roots: a body workflow (e.g. one step of a
	// build) legitimately assumes the state its caller enters it with.
	called := map[string]bool{}
	for path := range set.Workflows {
		reach, err := set.Reachable(path)
		if err != nil {
			t.Fatalf("Reachable(%s): %v", path, err)
		}
		for _, r := range reach {
			if r != path && strings.HasPrefix(r, "workflows/") {
				called[r] = true
			}
		}
	}
	roots := 0
	for path := range set.Workflows {
		if called[path] {
			continue
		}
		roots++
		if p := set.Check(path); len(p) != 0 {
			t.Fatalf("bundled root %s: %+v", path, p)
		}
	}
	if roots == 0 {
		t.Fatalf("no root workflows among bundled definitions")
	}
	if _, ok := set.Workflows["workflows/develop"]; !ok {
		t.Fatalf("bundled develop missing")
	}
	st := newStub()
	e, _ := newEngine(t, set, st)
	run := engine.RunID("ws7")
	ctx := context.Background()
	_ = st.PutData(ctx, run, engine.DataRef{Name: "instructions"}, []byte("add a flag"))
	if err := e.Start(ctx, run, "workflows/develop", []string{"instructions"}); err != nil {
		t.Fatal(err)
	}
	st.outputs["*/investigation"] = []byte("# findings\nnothing special\n")
	st.outputs["*/plan"] = []byte(validPlan)
	st.outputs["*/commit-message"] = []byte("feat: flag")
	st.outputs["*/comment-manifest"] = []byte(`[]`)
	st.outputs["*/selected-perspectives"] = []byte(`[]`)
	st.outputs["*/findings"] = []byte(`[]`)
	st.outputs["*/cross-cutting-candidates"] = []byte(`[]`)
	st.outputs["*/report"] = []byte("# report\nclean\n")
	st.outputs["*/plan-checklist"] = []byte(`{"claims":[],"sets":[],"items":[],"not_covered":[]}`)
	st.items["steps"] = []engine.Item{{Key: "1", Input: "step", Content: []byte(`{"number":1,"title":"t1","description":"one","tests":[],"files":["a.go"]}`)}}
	st.changed = []string{"a.go"}

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		s := advance(t, e, run)
		switch s.Kind {
		case engine.StatusAgent:
			outcome := "done"
			if s.Task.Agent.Name == "cross-cutting-explorer" {
				outcome = "none_found"
			}
			if err := e.ReportResult(ctx, run, s.Occurrence, outcome, ""); err != nil {
				t.Fatalf("ReportResult(%s): %v", s.Task.Agent.Name, err)
			}
		case engine.StatusGate:
			_ = e.Decide(ctx, run, s.Occurrence, engine.Decision{Outcome: "approved", TargetHash: s.Gate.TargetHash})
		case engine.StatusQuestion:
			_ = e.Answer(ctx, run, s.Occurrence, engine.Answer{Answers: map[string]string{}})
		case engine.StatusDone:
			if len(st.publish) != 1 {
				t.Fatalf("develop must publish exactly once: %+v", st.publish)
			}
			return
		case engine.StatusBlocked:
			t.Fatalf("develop blocked: %s (calls %v)", s.Reason, st.calls)
		}
	}
	t.Fatalf("develop did not finish; calls=%v", st.calls)
}

// ---------------------------------------------------------------------------
// C-E8: continues
// ---------------------------------------------------------------------------

func TestCE8_ContinuesPointsAtTheRolesLatestFinishedOccurrence(t *testing.T) {
	set := mustLoad(t, repoFS(map[string]string{
		"agents/a.md": agentMD("a", "Read, Grep, Glob", nil),
		"agents/b.md": agentMD("b", "Read, Grep", nil),
		"workflows/x.yaml": "version: 1\nstart: first-b\nnodes:\n" +
			"  first-b: {type: agent, role: agents/b, continues: agents/a, next: call}\n" +
			"  call: {type: workflow, workflow: workflows/y, next: second-b}\n" +
			"  second-b: {type: agent, role: agents/b, continues: agents/a, next: plain}\n" +
			"  plain: {type: agent, role: agents/b, next: end}\n",
		"workflows/y.yaml": "version: 1\nstart: a1\nnodes:\n" +
			"  a1: {type: agent, role: agents/a, next: a2}\n" +
			"  a2: {type: agent, role: agents/a, next: end}\n",
	}))
	mustCheck(t, set, "workflows/x")
	st := newStub()
	e, _ := newEngine(t, set, st)
	run := engine.RunID("ws8")
	ctx := context.Background()
	if err := e.Start(ctx, run, "workflows/x", nil); err != nil {
		t.Fatal(err)
	}
	report := func(task *engine.AgentTask) {
		t.Helper()
		if err := e.ReportResult(ctx, run, task.Occurrence, "done", ""); err != nil {
			t.Fatal(err)
		}
	}

	first := wantAgent(t, advance(t, e, run), "b")
	if first.Continues != "" {
		t.Fatalf("no occurrence of agents/a yet, but Continues = %q", first.Continues)
	}
	report(first)
	a1 := wantAgent(t, advance(t, e, run), "a")
	report(a1)
	a2 := wantAgent(t, advance(t, e, run), "a")
	report(a2)
	second := wantAgent(t, advance(t, e, run), "b")
	if second.Continues != a2.Occurrence {
		t.Fatalf("Continues = %q, want the latest finished agents/a occurrence %q (a1 was %q)", second.Continues, a2.Occurrence, a1.Occurrence)
	}
	if again := wantAgent(t, advance(t, e, run), "b"); again.Continues != second.Continues {
		t.Fatalf("a repeated Advance must hand out the same Continues, got %q", again.Continues)
	}
	report(second)
	if plain := wantAgent(t, advance(t, e, run), "b"); plain.Continues != "" {
		t.Fatalf("a node without continues got Continues = %q", plain.Continues)
	}
}

func TestCE8_CheckRejectsUnsafeOrUnreachableContinues(t *testing.T) {
	cases := map[string]map[string]string{
		"tools not a subset": {
			"agents/r.md":      agentMD("r", "Read, Grep", nil),
			"agents/v.md":      agentMD("v", "Read, Grep, Glob", nil),
			"workflows/x.yaml": "version: 1\nstart: r\nnodes:\n  r: {type: agent, role: agents/r, next: v}\n  v: {type: agent, role: agents/v, continues: agents/r, next: end}\n",
		},
		"all tools continuing limited tools": {
			"agents/r.md":      agentMD("r", "Read, Grep", nil),
			"agents/all.md":    agentMD("all", "", nil),
			"workflows/x.yaml": "version: 1\nstart: g\nnodes:\n  g: {type: approval, gate: plan, target: plan, next: {approved: r, rejected: end}}\n  r: {type: agent, role: agents/r, next: v}\n  v: {type: agent, role: agents/all, continues: agents/r, next: end}\n",
		},
		"continued role not reachable": {
			"agents/r.md":      agentMD("r", "Read, Grep", nil),
			"agents/v.md":      agentMD("v", "Read", nil),
			"workflows/x.yaml": "version: 1\nstart: v\nnodes:\n  v: {type: agent, role: agents/v, continues: agents/r, next: end}\n",
		},
		"continued role missing": {
			"agents/v.md":      agentMD("v", "Read", nil),
			"workflows/x.yaml": "version: 1\nstart: v\nnodes:\n  v: {type: agent, role: agents/v, continues: agents/ghost, next: end}\n",
		},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			set, err := engine.Load(repoFS(files), engine.Bundled())
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			p := set.Check("workflows/x")
			if len(p) == 0 {
				t.Fatalf("Check found nothing")
			}
			for _, x := range p {
				if strings.Contains(x.Message, "continues") {
					return
				}
			}
			t.Fatalf("Check did not name continues: %+v", p)
		})
	}
	set := mustLoad(t, repoFS(map[string]string{
		"agents/r.md":      agentMD("r", "Read, Grep, Glob", nil),
		"agents/v.md":      agentMD("v", "Read, Grep", nil),
		"workflows/x.yaml": "version: 1\nstart: r\nnodes:\n  r: {type: agent, role: agents/r, next: v}\n  v: {type: agent, role: agents/v, continues: agents/r, next: end}\n",
	}))
	mustCheck(t, set, "workflows/x")
	if _, err := engine.Load(repoFS(map[string]string{
		"workflows/x.yaml": "version: 1\nstart: e\nnodes:\n  e: {type: exec, command: [/bin/true], continues: agents/r, next: {done: end, failed: end}}\n",
	}), engine.Bundled()); err == nil {
		t.Fatal("Load accepted continues on an exec node")
	}
}

// ---------------------------------------------------------------------------
// C-E9: outputs on outcomes other than done
// ---------------------------------------------------------------------------

// startOutputs は、seedが done で note を書いた後の w で止まった run を返す。
// Set.Check は出力を done の遷移でだけ用意済みとみなすので、r が読む note は
// seed が保証し、w が done 以外で書いた note がそれより新しい値になるかを見る。
func startOutputs(t *testing.T, run engine.RunID) (*engine.Engine, *stub) {
	set := mustLoad(t, repoFS(map[string]string{
		"agents/seed.md": agentMD("seed", "Read", []string{"note"}),
		"agents/w.md":    agentMD("w", "Read", []string{"note", "plan"}, "partial", "empty"),
		"agents/r.md":    agentMD("r", "Read", nil),
		"workflows/x.yaml": "version: 1\nstart: seed\nnodes:\n" +
			"  seed: {type: agent, role: agents/seed, next: w}\n" +
			"  w: {type: agent, role: agents/w, max: 5, next: {done: r, partial: r, empty: end:empty}}\n" +
			"  r: {type: agent, role: agents/r, inputs: [note], next: end}\n",
	}))
	mustCheck(t, set, "workflows/x")
	st := newStub()
	e, _ := newEngine(t, set, st)
	ctx := context.Background()
	if err := e.Start(ctx, run, "workflows/x", nil); err != nil {
		t.Fatal(err)
	}
	seed := wantAgent(t, advance(t, e, run), "seed")
	st.outputs[seed.Occurrence+"/note"] = []byte("n0")
	if err := e.ReportResult(ctx, run, seed.Occurrence, "done", ""); err != nil {
		t.Fatal(err)
	}
	return e, st
}

func TestCE9_OutputsWrittenOnOtherOutcomesAreValidatedAndStored(t *testing.T) {
	run := engine.RunID("ws9")
	e, st := startOutputs(t, run)
	ctx := context.Background()

	// 不正な出力は done 以外でも差し戻す。
	w1 := wantAgent(t, advance(t, e, run), "w")
	st.outputs[w1.Occurrence+"/note"] = []byte("n1")
	st.outputs[w1.Occurrence+"/plan"] = []byte(`{"not":"a plan"}`)
	if err := e.ReportResult(ctx, run, w1.Occurrence, "partial", ""); err != nil {
		t.Fatalf("an invalid output must re-enter, not fail: %v", err)
	}
	w2 := wantAgent(t, advance(t, e, run), "w")
	if w2.Occurrence == w1.Occurrence || !strings.Contains(w2.Feedback, "plan") {
		t.Fatalf("want a re-entry naming the invalid output, got %+v", w2)
	}
	if _, ok := st.data[w1.Occurrence+"/note"]; ok {
		t.Fatalf("nothing of a refused report may be stored")
	}

	// done 以外で書かれた出力は保存され、次のノードの入力として解決される。
	// 書かれていない plan は done 以外なので問わない。
	st.outputs[w2.Occurrence+"/note"] = []byte("n2")
	if err := e.ReportResult(ctx, run, w2.Occurrence, "partial", ""); err != nil {
		t.Fatal(err)
	}
	r := wantAgent(t, advance(t, e, run), "r")
	if in := r.Inputs["note"]; in.Occurrence != w2.Occurrence {
		t.Fatalf("the note written on partial must be the latest value, got %+v", in)
	}
	if got := mustGet(t, st, r.Inputs["note"]); string(got) != "n2" {
		t.Fatalf("stored note = %q", got)
	}
	if _, ok := st.data[w2.Occurrence+"/plan"]; ok {
		t.Fatalf("an output that was not written must not be stored")
	}
}

func TestCE9_OutputsAreRequiredOnlyOnDone(t *testing.T) {
	run := engine.RunID("ws9b")
	e, st := startOutputs(t, run)
	ctx := context.Background()

	// done では従来どおり、書かれていない出力があれば差し戻す。
	w1 := wantAgent(t, advance(t, e, run), "w")
	st.outputs[w1.Occurrence+"/note"] = []byte("n1")
	if err := e.ReportResult(ctx, run, w1.Occurrence, "done", ""); err != nil {
		t.Fatal(err)
	}
	w2 := wantAgent(t, advance(t, e, run), "w")
	if w2.Occurrence == w1.Occurrence || !strings.Contains(w2.Feedback, "plan") {
		t.Fatalf("done without plan must re-enter naming it, got %+v", w2)
	}

	// done 以外では何も書かなくても結果は有効。
	if err := e.ReportResult(ctx, run, w2.Occurrence, "empty", ""); err != nil {
		t.Fatal(err)
	}
	if s := advance(t, e, run); s.Kind != engine.StatusDone || s.Outcome != "empty" {
		t.Fatalf("want the run to end with empty, got %+v", s)
	}
}
