package engine

import (
	"fmt"
	"strings"
	"testing"
)

func agentDef(name, tools string, outputs []string, outcomes ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "---\nname: %s\ndescription: d\n", name)
	if tools != "" {
		fmt.Fprintf(&b, "tools: %s\n", tools)
	}
	if len(outputs) > 0 {
		fmt.Fprintf(&b, "outputs: [%s]\n", strings.Join(outputs, ", "))
	}
	b.WriteString("outcomes:\n  done: d\n")
	for _, o := range outcomes {
		fmt.Fprintf(&b, "  %s: %s\n", o, o)
	}
	b.WriteString("---\nbody\n")
	return b.String()
}

func checkOf(t *testing.T, files map[string]string) []Problem {
	t.Helper()
	set, err := Load(mapFS(files), Bundled())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return set.Check("workflows/x")
}

var (
	roPlanner   = agentDef("planner", "Read", []string{"plan"}, "redo")
	rwImpl      = agentDef("impl", "Read, Edit", []string{"commit-message"})
	planPrelude = "  plan: {type: agent, role: agents/planner, outputs: [plan], next: {done: ok, redo: plan}}\n" +
		"  ok: {type: approval, gate: plan, target: plan, next: {approved: %s, rejected: plan}}\n"
)

// Each case must be rejected for its own reason, not an incidental one.
func TestCheckRejectsWithTheIntendedReason(t *testing.T) {
	y := "version: 1\nstart: d\nnodes:\n  d: {type: discard, next: end}\n"
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"unrouted outcome", map[string]string{
			"agents/a.md":      agentDef("a", "Read", nil, "retry"),
			"workflows/x.yaml": "version: 1\nstart: a\nnodes:\n  a: {type: agent, role: agents/a, next: {done: end}}\n",
		}, `outcome "retry" has no destination`},
		{"route for undeclared outcome", map[string]string{
			"agents/a.md":      agentDef("a", "Read", nil),
			"workflows/x.yaml": "version: 1\nstart: a\nnodes:\n  a: {type: agent, role: agents/a, next: {done: end, nope: end}}\n",
		}, `never produces "nope"`},
		{"missing role", map[string]string{
			"workflows/x.yaml": "version: 1\nstart: a\nnodes:\n  a: {type: agent, role: agents/missing, next: end}\n",
		}, "no such agent"},
		{"missing callee", map[string]string{
			"workflows/x.yaml": "version: 1\nstart: a\nnodes:\n  a: {type: workflow, workflow: workflows/nope, next: end}\n",
		}, "no such workflow"},
		{"call cycle", map[string]string{
			"workflows/x.yaml": "version: 1\nstart: a\nnodes:\n  a: {type: workflow, workflow: workflows/y, next: end}\n",
			"workflows/y.yaml": "version: 1\nstart: a\nnodes:\n  a: {type: workflow, workflow: workflows/x, next: end}\n",
		}, "calls form a cycle"},
		{"unbounded cycle", map[string]string{
			"workflows/x.yaml": "version: 1\nstart: a\nnodes:\n  a: {type: workflow, workflow: workflows/y, next: b}\n  b: {type: workflow, workflow: workflows/y, next: a}\n",
			"workflows/y.yaml": y,
		}, "could loop forever"},
		{"exhausted route without max", map[string]string{
			"workflows/x.yaml": "version: 1\nstart: a\nnodes:\n  a: {type: workflow, workflow: workflows/y, next: {done: end, exhausted: end}}\n",
			"workflows/y.yaml": y,
		}, "never ends exhausted"},
		{"blocked route", map[string]string{
			"workflows/x.yaml": "version: 1\nstart: a\nnodes:\n  a: {type: discard, next: {done: end, blocked: end}}\n",
		}, "blocked is not an outcome"},
		{"continue needs incomplete", map[string]string{
			"workflows/x.yaml": "version: 1\nstart: f\nnodes:\n  f: {type: foreach, over: perspectives, body: workflows/y, on_incomplete: continue, next: end}\n",
			"workflows/y.yaml": y,
		}, `outcome "incomplete" has no destination`},
		{"stop needs body labels", map[string]string{
			"workflows/x.yaml": "version: 1\nstart: f\nnodes:\n  f: {type: foreach, over: perspectives, body: workflows/y, next: {done: end}}\n",
			"workflows/y.yaml": "version: 1\nstart: d\nnodes:\n  d: {type: discard, next: end:gave_up}\n",
		}, `outcome "gave_up" has no destination`},
		{"with names no input", map[string]string{
			"workflows/x.yaml": "version: 1\nstart: a\nnodes:\n  a: {type: workflow, workflow: workflows/y, with: {zzz: instructions}, next: end}\n",
			"workflows/y.yaml": y,
		}, "declares no input"},
		{"reserved gate name", map[string]string{
			"workflows/x.yaml": "version: 1\nstart: g\nnodes:\n  g: {type: approval, gate: deviation, target: diff, next: {approved: end, rejected: end}}\n",
		}, "reserved"},
		{"write before plan approval", map[string]string{
			"agents/w.md":      agentDef("w", "Read, Edit, Bash", nil),
			"workflows/x.yaml": "version: 1\nstart: w\nnodes:\n  w: {type: agent, role: agents/w, next: end}\n",
		}, "without an approved plan"},
		{"new plan voids approval", map[string]string{
			"agents/planner.md": roPlanner, "agents/impl.md": rwImpl,
			"workflows/x.yaml": "version: 1\nstart: plan\nnodes:\n" + fmt.Sprintf(planPrelude, "re") +
				"  re: {type: agent, role: agents/planner, next: {done: impl, redo: impl}}\n" +
				"  impl: {type: agent, role: agents/impl, next: end}\n",
		}, "without an approved plan"},
		{"commit before plan", map[string]string{
			"workflows/x.yaml": "version: 1\nstart: c\nnodes:\n  c: {type: commit, scope: plan, next: {done: end, rejected: end}}\n",
		}, "commit can be reached without an approved plan"},
		{"publish with uncommitted changes", map[string]string{
			"agents/planner.md": roPlanner, "agents/impl.md": rwImpl,
			"workflows/x.yaml": "version: 1\nstart: plan\nnodes:\n" + fmt.Sprintf(planPrelude, "impl") +
				"  impl: {type: agent, role: agents/impl, next: pub}\n  pub: {type: publish, next: end}\n",
		}, "uncommitted changes"},
		{"uncommitted through a rejected commit", map[string]string{
			"agents/planner.md": roPlanner, "agents/impl.md": rwImpl,
			"workflows/x.yaml": "version: 1\nstart: plan\nnodes:\n" + fmt.Sprintf(planPrelude, "impl") +
				"  impl: {type: agent, role: agents/impl, next: c}\n  c: {type: commit, scope: plan, next: {done: pub, rejected: pub}}\n  pub: {type: publish, next: end}\n",
		}, "uncommitted changes"},
		{"dirty out of a callee", map[string]string{
			"agents/planner.md": roPlanner, "agents/impl.md": rwImpl,
			"workflows/x.yaml": "version: 1\nstart: plan\nnodes:\n" + fmt.Sprintf(planPrelude, "w") +
				"  w: {type: workflow, workflow: workflows/y, next: pub}\n  pub: {type: publish, next: end}\n",
			"workflows/y.yaml": "version: 1\nstart: impl\nnodes:\n  impl: {type: agent, role: agents/impl, next: end}\n",
		}, "uncommitted changes"},
		{"input never provided", map[string]string{
			"agents/a.md":      agentDef("a", "Read", nil),
			"workflows/x.yaml": "version: 1\nstart: a\nnodes:\n  a: {type: agent, role: agents/a, inputs: [ghost], next: end}\n",
		}, `reads "ghost"`},
		{"input only on one path", map[string]string{
			"agents/a.md":      agentDef("a", "Read", []string{"out"}, "skip"),
			"agents/b.md":      agentDef("b", "Read", nil),
			"workflows/x.yaml": "version: 1\nstart: a\nnodes:\n  a: {type: agent, role: agents/a, next: {done: b, skip: b}}\n  b: {type: agent, role: agents/b, inputs: [out], next: end}\n",
		}, `reads "out"`},
		{"callee input missing", map[string]string{
			"workflows/x.yaml": "version: 1\nstart: a\nnodes:\n  a: {type: workflow, workflow: workflows/y, next: end}\n",
			"workflows/y.yaml": "version: 1\ninputs: [need]\nstart: d\nnodes:\n  d: {type: discard, next: end}\n",
		}, `needs the input "need"`},
		{"with bound to missing data", map[string]string{
			"workflows/x.yaml": "version: 1\ninputs: [have]\nstart: a\nnodes:\n  a: {type: workflow, workflow: workflows/y, with: {need: ghost}, next: end}\n",
			"workflows/y.yaml": "version: 1\ninputs: [need]\nstart: d\nnodes:\n  d: {type: discard, next: end}\n",
		}, `(bound to "ghost")`},
		{"approval target missing", map[string]string{
			"workflows/x.yaml": "version: 1\nstart: g\nnodes:\n  g: {type: approval, gate: g, target: report, next: {approved: end, rejected: end}}\n",
		}, `target "report"`},
		{"foreach source missing", map[string]string{
			"workflows/x.yaml": "version: 1\nstart: f\nnodes:\n  f: {type: foreach, over: \"items[]\", body: workflows/y, next: {done: end}}\n",
			"workflows/y.yaml": "version: 1\ninputs: [item]\nstart: d\nnodes:\n  d: {type: discard, next: end}\n",
		}, `reads "items"`},
		{"step commit outside a steps body", map[string]string{
			"agents/planner.md": roPlanner,
			"workflows/x.yaml": "version: 1\nstart: plan\nnodes:\n" + fmt.Sprintf(planPrelude, "c") +
				"  c: {type: commit, scope: step, next: {done: end, rejected: end}}\n",
		}, `scope: step needs the input "step"`},
		{"export nobody writes", map[string]string{
			"workflows/x.yaml": "version: 1\nstart: d\nnodes:\n  d: {type: discard, export: [ghost], next: end}\n",
		}, `export "ghost"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ps := checkOf(t, tc.files)
			for _, p := range ps {
				if strings.Contains(p.Message, tc.want) {
					return
				}
			}
			t.Fatalf("want a problem containing %q, got %+v", tc.want, ps)
		})
	}
}

// The workflows the later contract tests run must pass Check as they are.
func TestCheckAcceptsRunnableWorkflows(t *testing.T) {
	cases := map[string]map[string]string{
		"linear": {
			"agents/planner.md": roPlanner,
			"workflows/x.yaml": "version: 1\ninputs: [instructions]\nstart: plan\nnodes:\n" +
				"  plan: {type: agent, role: agents/planner, inputs: [instructions], outputs: [plan], max: 2, next: {done: approve, redo: plan}}\n" +
				"  approve: {type: approval, gate: plan, target: plan, next: {approved: finish, rejected: plan}}\n" +
				"  finish: {type: discard, export: [plan], next: end}\n",
		},
		"exec and question": {
			"workflows/x.yaml": "version: 1\ninputs: [instructions]\nstart: fetch\nnodes:\n" +
				"  fetch: {type: exec, command: [\"/usr/bin/python3\", \"/workspace/fetch.py\"], outputs: [task], max: 2, next: {done: ask, failed: fetch, exhausted: end:fetch_failed}}\n" +
				"  ask: {type: question, questions: [{id: scope, text: \"proceed?\", options: [yes, split]}], outputs: [answers], next: {answered: finish}}\n" +
				"  finish: {type: discard, export: [task, answers], next: end}\n",
		},
		"build": {
			"agents/planner.md":     agentDef("planner", "Read", []string{"plan"}),
			"agents/implementer.md": agentDef("implementer", "Read, Edit, Bash", []string{"commit-message"}),
			"agents/reviewer.md":    agentDef("reviewer", "Read, Grep", []string{"findings"}),
			"workflows/x.yaml": "version: 1\ninputs: [instructions]\nstart: plan\nnodes:\n" +
				"  plan: {type: agent, role: agents/planner, inputs: [instructions], outputs: [plan], next: approve}\n" +
				"  approve: {type: approval, gate: plan, target: plan, next: {approved: steps, rejected: plan}}\n" +
				"  steps: {type: foreach, over: steps, body: workflows/step, next: {done: review, incomplete: review}}\n" +
				"  review: {type: agent, role: agents/reviewer, outputs: [findings], next: approve-review}\n" +
				"  approve-review: {type: approval, gate: review, target: diff, next: {approved: publish, rejected: plan}}\n" +
				"  publish: {type: publish, target: local, export: [findings], next: end}\n",
			"workflows/step.yaml": "version: 1\ninputs: [step]\nstart: implement\nnodes:\n" +
				"  implement: {type: agent, role: agents/implementer, inputs: [step], outputs: [commit-message], next: commit}\n" +
				"  commit: {type: commit, scope: step, next: {done: end, rejected: implement}}\n",
		},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			if ps := checkOf(t, files); ps != nil {
				t.Fatalf("Check: %+v", ps)
			}
		})
	}
}

func TestReachableIncludesSchemasAndCallees(t *testing.T) {
	set, err := Load(mapFS(map[string]string{
		"agents/planner.md": roPlanner,
		"workflows/x.yaml": "version: 1\nstart: plan\nnodes:\n" + fmt.Sprintf(planPrelude, "f") +
			"  f: {type: foreach, over: steps, body: workflows/y, next: end}\n",
		"workflows/y.yaml": "version: 1\ninputs: [step]\nstart: d\nnodes:\n  d: {type: discard, next: end}\n",
	}), Bundled())
	if err != nil {
		t.Fatal(err)
	}
	got, err := set.Reachable("workflows/x")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "agents/planner,schemas/plan,workflows/x,workflows/y" {
		t.Fatalf("Reachable = %v", got)
	}
	if _, err := set.Reachable("workflows/none"); err == nil {
		t.Fatal("Reachable of a missing root must fail")
	}
}

func TestMermaidDrawsEngineInterrupts(t *testing.T) {
	set, err := Load(mapFS(map[string]string{
		"agents/planner.md": roPlanner, "agents/impl.md": rwImpl,
		"workflows/x.yaml": "version: 1\nstart: plan\nnodes:\n" + fmt.Sprintf(planPrelude, "w") +
			"  w: {type: workflow, workflow: workflows/y, next: end}\n",
		"workflows/y.yaml": "version: 1\nstart: impl\nnodes:\n  impl: {type: agent, role: agents/impl, next: c}\n  c: {type: commit, scope: plan, next: {done: end, rejected: impl}}\n",
	}), Bundled())
	if err != nil {
		t.Fatal(err)
	}
	mm, err := set.Mermaid("workflows/x")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"subgraph w1_graph[\"workflows/y\"]",
		"w1_c -. \"before commit\" .-> w1_c_deviation",
		"w0_plan -. \"after run\" .-> w0_plan_deviation", // read-only agent
		"w0_w -. \"workflow\" .-> w1_impl",
		"triage{{",
	} {
		if !strings.Contains(mm, want) {
			t.Fatalf("Mermaid lacks %q:\n%s", want, mm)
		}
	}
	if strings.Contains(mm, "w1_impl -. \"after run\"") {
		t.Fatalf("a write-capable agent gets no read-only deviation:\n%s", mm)
	}
}
