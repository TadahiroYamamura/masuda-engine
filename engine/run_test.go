package engine

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

type kvStore struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (s *kvStore) Get(k string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[k]
	return v, ok, nil
}
func (s *kvStore) Put(k string, v []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[k] = v
	return nil
}
func (s *kvStore) Delete(k string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, k)
	return nil
}
func (s *kvStore) List(prefix string) ([]KV, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []KV
	for k, v := range s.m {
		if strings.HasPrefix(k, prefix) {
			out = append(out, KV{k, v})
		}
	}
	return out, nil
}
func (s *kvStore) Apply(ops []Op) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, op := range ops {
		if op.Kind == OpCheck {
			cur, ok := s.m[op.Key]
			if (op.Value == nil) == ok || (ok && string(cur) != string(op.Value)) {
				return false, nil
			}
		}
	}
	for _, op := range ops {
		if op.Kind == OpPut {
			s.m[op.Key] = op.Value
		}
	}
	return true, nil
}

// fakeRunner implements only what E3 uses; the embedded nil interface makes
// any other call panic, which is what a test of E3 wants to notice.
type fakeRunner struct {
	Runner
	data    map[string][]byte
	outputs map[string][]byte
	gates   int
	events  []Event
}

func (r *fakeRunner) SetPolicy(context.Context, RunID, Policy) error { return nil }
func (r *fakeRunner) ReadOutput(_ context.Context, _ RunID, occ, name string) ([]byte, bool, error) {
	v, ok := r.outputs[name]
	return v, ok, nil
}
func (r *fakeRunner) PutData(_ context.Context, _ RunID, ref DataRef, c []byte) error {
	r.data[ref.Occurrence+"/"+ref.Name] = c
	return nil
}
func (r *fakeRunner) GetData(_ context.Context, _ RunID, ref DataRef) ([]byte, error) {
	if v, ok := r.data[ref.Occurrence+"/"+ref.Name]; ok {
		return v, nil
	}
	return nil, errors.New("no data")
}
func (r *fakeRunner) OpenGate(context.Context, GateRequest) error { r.gates++; return nil }
func (r *fakeRunner) Log(e Event)                                 { r.events = append(r.events, e) }

func TestRunCallPassesFeedbackAndEndsThroughFrames(t *testing.T) {
	set, err := Load(mapFS(map[string]string{
		"agents/planner.md": agentDef("planner", "Read", []string{"plan"}),
		"workflows/x.yaml": "version: 1\ninputs: [instructions]\nstart: sub\nnodes:\n" +
			"  sub: {type: workflow, workflow: workflows/sub, next: {done: ok, given_up: end:given_up}}\n" +
			"  ok: {type: approval, gate: plan, target: plan, next: {approved: end, rejected: sub}}\n",
		"workflows/sub.yaml": "version: 1\ninputs: [instructions]\nstart: plan\nnodes:\n" +
			"  plan: {type: agent, role: agents/planner, inputs: [instructions], next: {done: end, exhausted: end:given_up}}\n",
	}), Bundled())
	if err != nil {
		t.Fatal(err)
	}
	if p := set.Check("workflows/x"); len(p) != 0 {
		t.Fatalf("Check: %+v", p)
	}
	r := &fakeRunner{data: map[string][]byte{"/instructions": []byte("x")}, outputs: map[string][]byte{
		"plan": []byte(`{"summary":"s","steps":[{"number":1,"description":"d","files":["a"]}],"expected_byproducts":[]}`),
	}}
	e := New(set, &kvStore{m: map[string][]byte{}}, r, Options{})
	ctx := context.Background()
	if _, err := e.Status(ctx, "r"); !errors.Is(err, errNotStarted) {
		t.Fatalf("Status before Start: %v", err)
	}
	if err := e.Start(ctx, "r", "workflows/x", []string{"instructions"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Status(ctx, "r"); !errors.Is(err, errPending) {
		t.Fatalf("Status before the first Advance: %v", err)
	}
	s, err := e.Advance(ctx, "r")
	if err != nil || s.Kind != StatusAgent || s.Occurrence != "0000002" || s.Task.Workflow != "workflows/sub" {
		t.Fatalf("want planner inside sub as occurrence 0000002, got %+v %v", s, err)
	}
	if err := e.ReportResult(ctx, "r", s.Occurrence, "done", ""); err != nil {
		t.Fatal(err)
	}
	s, _ = e.Advance(ctx, "r")
	if s2, _ := e.Advance(ctx, "r"); s.Kind != StatusGate || s2.Occurrence != s.Occurrence || r.gates != 1 {
		t.Fatalf("gate must open once: %+v gates=%d", s, r.gates)
	}
	if err := e.Decide(ctx, "r", s.Occurrence, Decision{Outcome: "rejected", Comment: "smaller"}); err != nil {
		t.Fatal(err)
	}
	s, _ = e.Advance(ctx, "r")
	if s.Kind != StatusAgent || s.Task.Feedback != "smaller" {
		t.Fatalf("rejection feedback must reach the callee's first node: %+v", s.Task)
	}
	if st, _ := e.Status(ctx, "r"); st.Occurrence != s.Occurrence {
		t.Fatalf("Status differs from Advance: %+v", st)
	}
	_ = e.ReportResult(ctx, "r", s.Occurrence, "done", "")
	s, _ = e.Advance(ctx, "r")
	_ = e.Decide(ctx, "r", s.Occurrence, Decision{Outcome: "approved", TargetHash: s.Gate.TargetHash})
	if s, _ = e.Advance(ctx, "r"); s.Kind != StatusDone || s.Outcome != "done" {
		t.Fatalf("want done, got %+v", s)
	}
}
