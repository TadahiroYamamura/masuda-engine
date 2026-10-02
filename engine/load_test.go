package engine

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func mapFS(files map[string]string) fstest.MapFS {
	m := fstest.MapFS{}
	for k, v := range files {
		m[k] = &fstest.MapFile{Data: []byte(v)}
	}
	return m
}

const testAgent = "---\nname: a\ndescription: d\ntools: Read\noutputs: [out]\noutcomes:\n  done: d\n  redo: r\n---\nbody\n"

func TestLoadAcceptsEveryNodeType(t *testing.T) {
	wf := `version: 1
inputs: [instructions]
start: a
nodes:
  a: {type: agent, role: agents/a, inputs: [instructions], outputs: [out], max: 2, egress: [api.example.com, "*.example.org:443"], secrets: [API_KEY], next: {done: x, redo: a, exhausted: end:gave_up}}
  x: {type: exec, command: ["/usr/bin/true", "rel/arg"], timeout: 2m, next: {done: q, failed: x}}
  q: {type: question, questions: [{id: scope, text: "ok?", options: [yes, split]}], outputs: [answers], next: {answered: r}}
  r: {type: question, role: agents/a, outputs: [more], next: {answered: g}}
  g: {type: approval, gate: plan, target: plan, next: {approved: f, rejected: a}}
  f: {type: foreach, over: "perspectives(from=a)", body: workflows/y, with: {item: out}, next: {done: f2, incomplete: f2}}
  f2: {type: foreach, over: "out[]", body: workflows/y, on_incomplete: continue, max: 5, next: w}
  w: {type: workflow, workflow: workflows/y, next: c}
  c: {type: commit, scope: plan, next: {done: p, rejected: a}}
  p: {type: publish, export: [out], next: end}
`
	set, err := Load(mapFS(map[string]string{"workflows/x.yaml": wf, "agents/a.md": testAgent,
		"workflows/y.yaml": "version: 1\nstart: d\nnodes:\n  d: {type: discard, next: end}\n"}), Bundled())
	if err != nil {
		t.Fatal(err)
	}
	w := set.Workflows["workflows/x"]
	if strings.Join(w.Order, ",") != "a,x,q,r,g,f,f2,w,c,p" {
		t.Fatalf("order %v", w.Order)
	}
	if got := w.Nodes["a"].Next["exhausted"]; !got.End || got.Label != "gave_up" {
		t.Fatalf("end label: %+v", got)
	}
	if w.Nodes["p"].PublishTarget != "local" || w.Nodes["f"].OnIncomplete != "stop" || w.Nodes["g"].Target != "plan" {
		t.Fatalf("defaults/targets: %+v %+v %+v", w.Nodes["p"], w.Nodes["f"], w.Nodes["g"])
	}
	if w.Nodes["x"].Timeout.Minutes() != 2 || w.Nodes["q"].Questions[0].Options[0] != "yes" {
		t.Fatalf("exec/question: %+v %+v", w.Nodes["x"], w.Nodes["q"])
	}
	if set.Origins["workflows/smoke"] != OriginBundled || set.Origins["workflows/x"] != OriginRepo || set.Origins["schemas/plan"] != OriginBundled {
		t.Fatalf("origins %v", set.Origins)
	}
}

func TestLoadRejectionReasons(t *testing.T) {
	node := func(body string) map[string]string {
		return map[string]string{"workflows/x.yaml": "version: 1\nstart: a\nnodes:\n  a: " + body + "\n"}
	}
	cases := map[string]struct {
		files map[string]string
		want  string
	}{
		"unknown key on approval": {node("{type: approval, gate: g, target: plan, role: nope, next: {approved: end, rejected: end}}"), `cannot have "role"`},
		"relative command":        {node("{type: exec, command: [python3, x.py], next: end}"), "absolute path"},
		"agent without done":      {map[string]string{"agents/bad.md": "---\nname: bad\ndescription: d\noutcomes:\n  ok: fine\n---\nbody\n"}, `must declare "done"`},
		"reserved outcome":        {map[string]string{"agents/bad.md": "---\nname: bad\ndescription: d\noutcomes:\n  done: d\n  exhausted: no\n---\nbody\n"}, "reserved"},
		"engine data output":      {map[string]string{"agents/bad.md": "---\nname: bad\ndescription: d\noutputs: [diff]\noutcomes:\n  done: d\n---\nbody\n"}, "computed by the engine"},
		"bad node name":           {map[string]string{"workflows/x.yaml": "version: 1\nstart: A_1\nnodes:\n  A_1: {type: discard, next: end}\n"}, "node name must match"},
		"end node name":           {map[string]string{"workflows/x.yaml": "version: 1\nstart: end\nnodes:\n  end: {type: discard, next: end}\n"}, "node name must match"},
		"bad next shape":          {node("{type: discard, next: [end]}"), "next:"},
		"next to missing node":    {node("{type: discard, next: nowhere}"), `no node "nowhere"`},
		"end:done":                {node("{type: discard, next: end:done}"), "reserved"},
		"max zero":                {node("{type: agent, role: agents/a, max: 0, next: end}"), "at least 1"},
		"bad data name":           {node("{type: discard, export: [Plan], next: end}"), "data name"},
		"bad timeout":             {node("{type: exec, command: [/bin/true], timeout: soon, next: end}"), "timeout"},
		"question two outputs":    {node("{type: question, questions: [{id: a, text: b}], outputs: [x, y], next: end}"), "exactly one output"},
		"question role and fixed": {node("{type: question, role: agents/a, questions: [{id: a, text: b}], outputs: [x], next: end}"), "exactly one of"},
		"bad over":                {node("{type: foreach, over: everything, body: workflows/y, next: end}"), "over:"},
		"missing next":            {node("{type: discard}"), `needs "next"`},
		"unknown type":            {node("{type: investigate, next: end}"), "unknown type"},
		"role not a ref":          {node("{type: agent, role: planner, next: end}"), "reference"},
		"version 2":               {map[string]string{"workflows/x.yaml": "version: 2\nstart: a\nnodes:\n  a: {type: discard, next: end}\n"}, "not supported"},
		"name mismatch":           {map[string]string{"agents/b.md": strings.Replace(testAgent, "name: a", "name: c", 1)}, "must equal"},
		"wrong extension":         {map[string]string{"workflows/x.yml": "x"}, "only .yaml"},
		"bad schema":              {map[string]string{"schemas/x.json": `{"type": 3}`}, "schema"},
		"old draft":               {map[string]string{"schemas/x.json": `{"$schema": "http://json-schema.org/draft-07/schema#"}`}, "2020-12"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Load(mapFS(c.files), Bundled())
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to mention %q", err, c.want)
			}
		})
	}
}

func TestToolsForms(t *testing.T) {
	for tools, want := range map[string]bool{"tools: [Read, Write]\n": true, "tools: Read, Grep\n": false, "tools: []\n": false, "": true} {
		md := "---\nname: a\ndescription: d\n" + tools + "outcomes:\n  done: d\n---\nbody\n"
		set, err := Load(mapFS(map[string]string{"agents/a.md": md}), nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := set.Agents["a"].WriteCapable(); got != want {
			t.Fatalf("%q: WriteCapable = %v", tools, got)
		}
	}
}

// TestBundledSchemas pins the shapes the contract tests feed the engine.
// Text data (commit-message) is validated as one JSON string.
func TestBundledSchemas(t *testing.T) {
	set, err := Load(nil, Bundled())
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		schema, data string
		text, ok     bool
	}{
		{"plan", `{"summary":"do it","steps":[{"number":1,"description":"one","files":["a.go"]}],"expected_byproducts":[]}`, false, true},
		{"plan", `{"summary":"s","steps":[{"number":1,"description":"a","files":["a.go"]},{"number":2,"description":"b","files":["dir/b.go"]}],"expected_byproducts":["go.sum"]}`, false, true},
		{"plan", `{"not":"a plan"}`, false, false},
		{"plan", `{"summary":"s","steps":[]}`, false, false},
		{"plan", `{"summary":"s","steps":[{"number":1,"description":"a","files":["../x"]}]}`, false, false},
		{"plan", `{"summary":"s","steps":[{"number":1,"description":"a","files":["/abs"]}]}`, false, false},
		{"findings", `[]`, false, true},
		{"findings", `[{"id":"sec-1","file":"a.go","line":3,"severity":"高","autofix":true,"message":"m"}]`, false, true},
		{"findings", `[{"file":"a.go","line":3,"severity":"高","autofix":true,"message":"m"}]`, false, false},
		{"findings", `[{"id":"a b","file":"a.go","line":3,"severity":"高","autofix":true,"message":"m"}]`, false, false},
		{"findings", `[{"id":"sec-1","file":"a.go","line":0,"severity":"高","autofix":true,"message":"m"}]`, false, false},
		{"findings", `[{"id":"sec-1","file":"a.go","line":1,"severity":"high","autofix":true,"message":"m"}]`, false, false},
		{"selected-perspectives", `[]`, false, true},
		{"selected-perspectives", `["security","security"]`, false, false},
		{"answers", `{"scope":"yes"}`, false, true},
		{"answers", `{"scope":1}`, false, false},
		{"commit-message", "feat: flag", true, true},
		{"commit-message", "  \n", true, false},
	}
	for _, c := range cases {
		c2 := jsonschema.NewCompiler()
		doc, _ := jsonschema.UnmarshalJSON(bytes.NewReader(set.Schemas[c.schema]))
		if err := c2.AddResource("mem:///s.json", doc); err != nil {
			t.Fatal(err)
		}
		sch, err := c2.Compile("mem:///s.json")
		if err != nil {
			t.Fatal(err)
		}
		var v any
		if c.text {
			v = c.data
		} else if err := json.Unmarshal([]byte(c.data), &v); err != nil {
			t.Fatal(err)
		}
		if err := sch.Validate(v); (err == nil) != c.ok {
			t.Errorf("%s %s: valid=%v want %v (%v)", c.schema, c.data, err == nil, c.ok, err)
		}
	}
}
