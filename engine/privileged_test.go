package engine

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

type privRunner struct {
	fakeRunner
	results   []CommandResult
	err       error
	tasks     []PrivilegedTask
	policies  int
	snapshots []string
}

func (r *privRunner) RunPrivileged(_ context.Context, t PrivilegedTask) (CommandResult, error) {
	r.tasks = append(r.tasks, t)
	if r.err != nil {
		return CommandResult{}, r.err
	}
	if len(r.results) == 0 {
		return CommandResult{}, nil
	}
	c := r.results[0]
	r.results = r.results[1:]
	return c, nil
}
func (r *privRunner) SetPolicy(context.Context, RunID, Policy) error { r.policies++; return nil }
func (r *privRunner) Snapshot(_ context.Context, _ RunID, occ string) (SnapshotRef, error) {
	r.snapshots = append(r.snapshots, occ)
	return SnapshotRef("snap-" + occ), nil
}

const privTestWF = `version: 1
start: verify
nodes:
  verify:
    type: privileged
    name: db-verify
    max: 2
    next: {done: end, failed: fix, exhausted: end:gave_up}
  fix:
    type: agent
    role: agents/fixer
    next: verify
`

func newPrivEngine(t *testing.T, r *privRunner) *Engine {
	t.Helper()
	set, err := Load(mapFS(map[string]string{
		"workflows/x.yaml": privTestWF,
		"agents/fixer.md":  agentDef("fixer", "Read", nil),
	}), Bundled())
	if err != nil {
		t.Fatal(err)
	}
	if p := set.Check("workflows/x"); len(p) != 0 {
		t.Fatalf("Check: %+v", p)
	}
	r.data = map[string][]byte{}
	e := New(set, &kvStore{m: map[string][]byte{}}, r, Options{})
	if err := e.Start(context.Background(), "r", "workflows/x", nil); err != nil {
		t.Fatal(err)
	}
	return e
}

func finishes(r *privRunner, node string) []Event {
	var out []Event
	for _, ev := range r.events {
		if ev.Kind == "finish" && ev.Node == node {
			out = append(out, ev)
		}
	}
	return out
}

func Test特権ノードは終了コード0でdoneへ進み方針の設定もスナップショットもしない(t *testing.T) {
	r := &privRunner{results: []CommandResult{{ExitCode: 0}}}
	e := newPrivEngine(t, r)
	s, err := e.Advance(context.Background(), "r")
	if err != nil || s.Kind != StatusDone || s.Outcome != OutcomeDone {
		t.Fatalf("want done, got %+v %v", s, err)
	}
	if len(r.tasks) != 1 || r.tasks[0].Name != "db-verify" || r.tasks[0].Node != "verify" || r.tasks[0].Run != "r" || r.tasks[0].Occurrence == "" {
		t.Fatalf("tasks = %+v", r.tasks)
	}
	if r.policies != 0 || len(r.snapshots) != 0 {
		t.Fatalf("SetPolicy=%d Snapshot=%v, want neither", r.policies, r.snapshots)
	}
}

func Test特権ノードは0以外の終了コードでfailedへ進みログの末尾をfeedbackにする(t *testing.T) {
	r := &privRunner{results: []CommandResult{{ExitCode: 2, LogTail: "FAIL TestDB"}}}
	e := newPrivEngine(t, r)
	s, err := e.Advance(context.Background(), "r")
	if err != nil || s.Kind != StatusAgent || s.Task.Node != "fix" {
		t.Fatalf("want the fix agent, got %+v %v", s, err)
	}
	if fb := s.Task.Feedback; !strings.Contains(fb, `"db-verify"`) || !strings.Contains(fb, "exit 2") || !strings.Contains(fb, "FAIL TestDB") {
		t.Fatalf("feedback = %q", fb)
	}
	if fs := finishes(r, "verify"); len(fs) != 1 || fs[0].Outcome != OutcomeFailed {
		t.Fatalf("finish events = %+v", fs)
	}
	if slices.Contains(r.snapshots, r.tasks[0].Occurrence) {
		t.Fatalf("a privileged occurrence must not be snapshotted: %v", r.snapshots)
	}
}

func Test特権ノードは終了コード0でも時間切れならfailedにする(t *testing.T) {
	r := &privRunner{results: []CommandResult{{ExitCode: 0, TimedOut: true}}}
	e := newPrivEngine(t, r)
	s, err := e.Advance(context.Background(), "r")
	if err != nil || s.Kind != StatusAgent || !strings.Contains(s.Task.Feedback, "タイムアウト") {
		t.Fatalf("want the fix agent with a timeout feedback, got %+v %v", s, err)
	}
}

func Test特権ノードはmaxを超えるとexhaustedで終わる(t *testing.T) {
	fail := CommandResult{ExitCode: 1}
	r := &privRunner{results: []CommandResult{fail, fail, fail}}
	e := newPrivEngine(t, r)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		s, err := e.Advance(ctx, "r")
		if err != nil || s.Kind != StatusAgent {
			t.Fatalf("round %d: want the fix agent, got %+v %v", i, s, err)
		}
		if err := e.ReportResult(ctx, "r", s.Occurrence, OutcomeDone, ""); err != nil {
			t.Fatal(err)
		}
	}
	s, err := e.Advance(ctx, "r")
	if err != nil || s.Kind != StatusDone || s.Outcome != "gave_up" || len(r.tasks) != 2 {
		t.Fatalf("want end:gave_up after 2 runs, got %+v %v runs=%d", s, err, len(r.tasks))
	}
}

func Test特権コマンドを実行できなければAdvanceはエラーを返し結果を記録せず次のAdvanceで再び実行する(t *testing.T) {
	r := &privRunner{err: errors.New(`privileged command "db-verify" is not approved`)}
	e := newPrivEngine(t, r)
	ctx := context.Background()
	if _, err := e.Advance(ctx, "r"); err == nil || !strings.Contains(err.Error(), "not approved") {
		t.Fatalf("want the Runner's error, got %v", err)
	}
	if fs := finishes(r, "verify"); len(fs) != 0 {
		t.Fatalf("nothing may be recorded, got %+v", fs)
	}
	r.err = nil
	s, err := e.Advance(ctx, "r")
	if err != nil || s.Kind != StatusDone || len(r.tasks) != 2 {
		t.Fatalf("want done on the retry, got %+v %v runs=%d", s, err, len(r.tasks))
	}
	if r.tasks[0].Occurrence != r.tasks[1].Occurrence {
		t.Fatalf("the retry must run the same occurrence: %+v", r.tasks)
	}
}
