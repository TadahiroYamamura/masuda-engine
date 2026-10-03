package engine

import (
	"context"
	"slices"
	"strings"
	"testing"
)

type bundledRunner struct {
	e5Runner
	published int
}

func (r *bundledRunner) RunCommand(context.Context, CommandTask) (CommandResult, error) {
	return CommandResult{}, nil
}
func (r *bundledRunner) Publish(context.Context, PublishRequest) error { r.published++; return nil }

// checkContinues は、fixerが直前に終わったimplementer（developの最終の修正では
// 最後のステップかreworkのもの）に続けて渡され、同梱の他の役は誰も続けない
// ことを確かめる。
func checkContinues(t *testing.T, i int, task *AgentTask, lastImpl string) {
	t.Helper()
	want := ""
	if task.Agent.Name == "fixer" {
		want = lastImpl
		if want == "" {
			t.Fatalf("step %d: a fixer ran before any implementer", i)
		}
	}
	if task.Continues != want {
		t.Fatalf("step %d: %s/%s Continues = %q, want %q", i, task.Workflow, task.Node, task.Continues, want)
	}
}

func checkCommentManifest(t *testing.T, i int, task *AgentTask) {
	t.Helper()
	switch task.Agent.Name {
	case "implementer", "fixer":
		if !slices.Contains(task.Outputs, "comment-manifest") {
			t.Fatalf("step %d: the %s must write comment-manifest, got %v", i, task.Agent.Name, task.Outputs)
		}
	case "reviewer", "review-checker":
		if _, ok := task.Inputs["comment-manifest"]; !ok {
			t.Fatalf("step %d: the %s must read comment-manifest, got %+v", i, task.Agent.Name, task.Inputs)
		}
	}
}

// Test同梱のdevelopは役ごとに1セッションで進み却下を直前の役へ戻す は、
// 3ステップのdevelopを歩かせる。planゲートの却下は計画を書き直さず、
// plan-reviserが人間の却下理由をfeedbackで受けて直前の計画を直す。各ステップは
// implementerからテストを経てそのままコミットする。最終レビューのcleanも
// checkerを通り、一度差し戻される。fixerのcannot_fixで残りをレポートへ渡す。
// reworkの後は最終レビューをやり直す。implementerはreworkも含め調査結果を読む。
func Test同梱のdevelopは役ごとに1セッションで進み却下を直前の役へ戻す(t *testing.T) {
	set, err := Load(nil, Bundled())
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"workflows/fix-finding", "workflows/review/perspective-review", "workflows/implement/interim-review"} {
		if _, ok := set.Workflows[gone]; ok {
			t.Fatalf("%s is no longer bundled", gone)
		}
	}
	if _, ok := set.Agents["agents/trigger-matcher"]; ok {
		t.Fatal("agents/trigger-matcher is no longer bundled")
	}
	if p := set.Check("workflows/develop"); len(p) != 0 {
		t.Fatalf("Check develop: %+v", p)
	}
	if p := set.Check("workflows/review"); len(p) != 0 {
		t.Fatalf("Check review: %+v", p)
	}

	plan := `{"goal":"g","summary":"s","steps":[{"number":1,"title":"t1","description":"a","tests":[],"files":["a.go"]},{"number":2,"title":"t2","description":"b","tests":[],"files":["a.go"]},{"number":3,"title":"t3","description":"c","tests":[],"files":["a.go"]}],"alternatives":[],"risks":[],"expected_byproducts":[],"checks":[]}`
	r := &bundledRunner{e5Runner: e5Runner{
		fakeRunner: fakeRunner{data: map[string][]byte{"/instructions": []byte("x")}, outputs: map[string][]byte{
			"investigation":            []byte("i"),
			"plan":                     []byte(plan),
			"plan-checklist":           []byte(emptyChecklist),
			"findings":                 []byte(`[]`),
			"cross-cutting-candidates": []byte(`[]`),
			"report":                   []byte("# report"),
			"commit-message":           []byte("feat: x"),
			"comment-manifest":         []byte(`[]`),
		}},
		items: []Item{
			{Key: "1", Input: "step", Content: []byte(`{"number":1,"title":"t1","description":"a","tests":[],"files":["a.go"]}`)},
			{Key: "2", Input: "step", Content: []byte(`{"number":2,"title":"t2","description":"b","tests":[],"files":["a.go"]}`)},
			{Key: "3", Input: "step", Content: []byte(`{"number":3,"title":"t3","description":"c","tests":[],"files":["a.go"]}`)},
		},
		changed: []string{"a.go"},
	}}
	e := New(set, &kvStore{m: map[string][]byte{}}, r, Options{})
	ctx := context.Background()
	if err := e.Start(ctx, "r", "workflows/develop", []string{"instructions"}); err != nil {
		t.Fatal(err)
	}

	type step struct{ at, outcome string }
	var lastImpl, questionsOcc, reviseOcc string
	script := []step{
		{"workflows/develop/investigate", "done"},
		{"workflows/develop/plan", "done"},
		{"workflows/develop/questions", "done"},
		{"workflows/develop/revise", "done"},
		{"gate:plan", "rejected"},
		{"workflows/develop/revise-rejected", "done"},
		{"gate:plan", "approved"},
		{"workflows/implement/build-step/implement", "done"},
		{"workflows/implement/build-step/implement", "done"},
		{"workflows/implement/build-step/implement", "done"},
		{"workflows/review/perspectives/review", "clean"},
		{"workflows/review/perspectives/check-review", "inaccurate"},
		{"workflows/review/perspectives/review", "done"},
		{"workflows/review/perspectives/check-review", "done"},
		{"workflows/review/cross-cutting/explore", "none_found"},
		{"workflows/develop/fix", "done"},
		{"workflows/develop/recheck", "unresolved"},
		{"workflows/develop/fix", "cannot_fix"},
		{"workflows/develop/report", "done"},
		{"gate:review", "rejected"},
		{"workflows/develop/rework", "done"},
		{"workflows/review/perspectives/review", "clean"},
		{"workflows/review/perspectives/check-review", "clean"},
		{"workflows/review/cross-cutting/explore", "none_found"},
		{"workflows/develop/fix", "nothing_to_fix"},
		{"workflows/develop/report", "done"},
		{"gate:review", "approved"},
	}
	for i, want := range script {
		s := mustAdvance(t, e)
		var at string
		switch s.Kind {
		case StatusAgent:
			at = s.Task.Workflow + "/" + s.Task.Node
		case StatusGate:
			at = "gate:" + s.Gate.Gate
		default:
			t.Fatalf("step %d: want %s, got %+v", i, want.at, s)
		}
		if at != want.at {
			t.Fatalf("step %d: want %s, got %s", i, want.at, at)
		}
		if s.Kind == StatusGate {
			d := Decision{Outcome: want.outcome, TargetHash: s.Gate.TargetHash}
			if want.outcome == "rejected" {
				d.Comment = "人間の却下理由: " + s.Gate.Gate
			}
			if err := e.Decide(ctx, "r", s.Occurrence, d); err != nil {
				t.Fatalf("step %d: %v", i, err)
			}
			continue
		}
		if s.Task.Agent.Name == "implementer" {
			if _, ok := s.Task.Inputs["investigation"]; !ok {
				t.Fatalf("step %d: the implementer must read the investigation, got %+v", i, s.Task.Inputs)
			}
		}
		switch s.Task.Node {
		case "questions":
			questionsOcc = s.Occurrence
		case "revise":
			reviseOcc = s.Occurrence
		case "revise-rejected":
			if s.Task.Agent.Name != "plan-reviser" {
				t.Fatalf("step %d: the plan gate's rejection must go to plan-reviser, got %s", i, s.Task.Agent.Name)
			}
			if s.Task.Feedback != "人間の却下理由: plan" {
				t.Fatalf("step %d: plan-reviser must receive the human's rejection as feedback, got %q", i, s.Task.Feedback)
			}
			if in := s.Task.Inputs["plan"]; in.Occurrence != reviseOcc {
				t.Fatalf("step %d: plan-reviser must read the rejected plan, got %+v", i, in)
			}
			if in := s.Task.Inputs["plan-checklist"]; in.Occurrence != questionsOcc {
				t.Fatalf("step %d: plan-reviser must read the checklist, got %+v", i, in)
			}
		}
		checkContinues(t, i, s.Task, lastImpl)
		checkCommentManifest(t, i, s.Task)
		if s.Task.Agent.Name == "implementer" {
			lastImpl = s.Occurrence
		}
		if err := e.ReportResult(ctx, "r", s.Occurrence, want.outcome, ""); err != nil {
			t.Fatalf("step %d (%s): %v", i, want.at, err)
		}
	}
	if s := mustAdvance(t, e); s.Kind != StatusDone || r.published != 1 {
		t.Fatalf("want published and done, got %+v published=%d", s, r.published)
	}
	var scopes []string
	for _, c := range r.commits {
		scopes = append(scopes, c.Scope)
	}
	if !slices.Equal(scopes, []string{"step", "step", "step", "plan", "plan", "plan"}) {
		t.Fatalf("want three step commits, the review commit, the rework commit and the second review commit, got %v", scopes)
	}
}

// TestBundledFixPlansInOneSessionAndPublishes walks the bundled fix with one
// step: the first final review goes through the fixer and the rechecker, the
// review gate sends it back to a rework, and the second review is clean and
// goes straight to the review commit.
func TestBundledFixPlansInOneSessionAndPublishes(t *testing.T) {
	set, err := Load(nil, Bundled())
	if err != nil {
		t.Fatal(err)
	}
	called := map[string]bool{}
	for path := range set.Workflows {
		reach, err := set.Reachable(path)
		if err != nil {
			t.Fatalf("Reachable(%s): %v", path, err)
		}
		for _, r := range reach {
			if r != path {
				called[r] = true
			}
		}
	}
	if _, ok := set.Workflows["workflows/fix"]; !ok || called["workflows/fix"] {
		t.Fatal("workflows/fix must be a bundled root")
	}
	if !called["workflows/fix/build-step"] {
		t.Fatal("workflows/fix/build-step must be called from workflows/fix")
	}
	if p := set.Check("workflows/fix"); len(p) != 0 {
		t.Fatalf("Check fix: %+v", p)
	}

	plan := `{"goal":"g","summary":"s","steps":[{"number":1,"title":"t1","description":"a","tests":["x"],"files":["a.go"]}],"alternatives":[],"risks":[],"expected_byproducts":[],"checks":[]}`
	r := &bundledRunner{e5Runner: e5Runner{
		fakeRunner: fakeRunner{data: map[string][]byte{"/instructions": []byte("x")}, outputs: map[string][]byte{
			"investigation":    []byte("i"),
			"plan":             []byte(plan),
			"findings":         []byte(`[]`),
			"commit-message":   []byte("fix: x"),
			"comment-manifest": []byte(`[]`),
		}},
		items: []Item{
			{Key: "1", Input: "step", Content: []byte(`{"number":1,"title":"t1","description":"a","tests":["x"],"files":["a.go"]}`)},
		},
		changed: []string{"a.go"},
	}}
	e := New(set, &kvStore{m: map[string][]byte{}}, r, Options{})
	ctx := context.Background()
	if err := e.Start(ctx, "r", "workflows/fix", []string{"instructions"}); err != nil {
		t.Fatal(err)
	}

	type step struct{ at, outcome string }
	var lastImpl string
	script := []step{
		{"workflows/fix/plan", "done"},
		{"gate:plan", "approved"},
		{"workflows/fix/build-step/implement", "done"},
		{"workflows/review/perspectives/review", "done"},
		{"workflows/review/perspectives/check-review", "done"},
		{"workflows/fix/fix", "done"},
		{"workflows/fix/recheck", "done"},
		{"gate:review", "rejected"},
		{"workflows/fix/rework", "done"},
		{"workflows/review/perspectives/review", "clean"},
		{"workflows/review/perspectives/check-review", "clean"},
		{"gate:review", "approved"},
	}
	for i, want := range script {
		s := mustAdvance(t, e)
		var at string
		switch s.Kind {
		case StatusAgent:
			at = s.Task.Workflow + "/" + s.Task.Node
		case StatusGate:
			at = "gate:" + s.Gate.Gate
		default:
			t.Fatalf("step %d: want %s, got %+v", i, want.at, s)
		}
		if at != want.at {
			t.Fatalf("step %d: want %s, got %s", i, want.at, at)
		}
		if s.Kind == StatusGate {
			if err := e.Decide(ctx, "r", s.Occurrence, Decision{Outcome: want.outcome, TargetHash: s.Gate.TargetHash}); err != nil {
				t.Fatalf("step %d: %v", i, err)
			}
			continue
		}
		if s.Task.Agent.Name == "implementer" {
			if _, ok := s.Task.Inputs["investigation"]; !ok {
				t.Fatalf("step %d: the implementer must read the investigation, got %+v", i, s.Task.Inputs)
			}
		}
		checkContinues(t, i, s.Task, lastImpl)
		checkCommentManifest(t, i, s.Task)
		if s.Task.Agent.Name == "implementer" {
			lastImpl = s.Occurrence
		}
		if err := e.ReportResult(ctx, "r", s.Occurrence, want.outcome, ""); err != nil {
			t.Fatalf("step %d (%s): %v", i, want.at, err)
		}
	}
	if s := mustAdvance(t, e); s.Kind != StatusDone || r.published != 1 {
		t.Fatalf("want published and done, got %+v published=%d", s, r.published)
	}
	var scopes []string
	for _, c := range r.commits {
		scopes = append(scopes, c.Scope)
	}
	if !slices.Equal(scopes, []string{"step", "plan", "plan", "plan"}) {
		t.Fatalf("want the step commit, the review commit, the rework commit and the second review commit, got %v", scopes)
	}
}

// Test同梱のdevelopの最終の修正で反論が続くと3回でレポートへ進む は、最終
// レビューの2件の指摘にfixerが反論し、recheckerが1件（f1）の反論を認めて
// 取り下げつつ、もう1件（f2）を維持してunresolvedで返す経路を歩かせる。
// 取り下げはunresolvedの報告でも保存され、次のfixerの台帳からf1が消えている。
// fixerがf2に反論し続け、recheckerが維持し続けるので、3回の修正の後、4回目の
// 進入がexhaustedになりreview-commitを経てレポートへ渡る。どの修正も最後の
// ステップのimplementerに続けて渡され、反論（disputed・response）が指摘の
// 台帳に届く。
func Test同梱のdevelopの最終の修正で反論が続くと3回でレポートへ進む(t *testing.T) {
	set, err := Load(nil, Bundled())
	if err != nil {
		t.Fatal(err)
	}
	if p := set.Check("workflows/develop"); len(p) != 0 {
		t.Fatalf("Check develop: %+v", p)
	}
	plan := `{"goal":"g","summary":"s","steps":[{"number":1,"title":"t1","description":"a","tests":[],"files":["a.go"]}],"alternatives":[],"risks":[],"expected_byproducts":[],"checks":[]}`
	finding := func(id, extra string) string {
		return `{"id":"` + id + `","file":"a.go","line":3,"severity":"中","autofix":true,"message":"m"` + extra + `}`
	}
	const disputed = `,"disputed":true,"response":"計画のステップ1でこの形にすると決めている"`
	r := &bundledRunner{e5Runner: e5Runner{
		fakeRunner: fakeRunner{data: map[string][]byte{"/instructions": []byte("x")}, outputs: map[string][]byte{
			"investigation":    []byte("i"),
			"plan":             []byte(plan),
			"plan-checklist":   []byte(emptyChecklist),
			"findings":         []byte("[" + finding("f1", "") + "," + finding("f2", "") + "]"),
			"commit-message":   []byte("feat: x"),
			"report":           []byte("# report"),
			"comment-manifest": []byte(`[]`),
		}},
		items:   []Item{{Key: "1", Input: "step", Content: []byte(`{"number":1,"title":"t1","description":"a","tests":[],"files":["a.go"]}`)}},
		changed: []string{"a.go"},
	}}
	e := New(set, &kvStore{m: map[string][]byte{}}, r, Options{})
	ctx := context.Background()
	if err := e.Start(ctx, "r", "workflows/develop", []string{"instructions"}); err != nil {
		t.Fatal(err)
	}
	type step struct{ at, outcome string }
	script := []step{
		{"workflows/develop/investigate", "done"},
		{"workflows/develop/plan", "done"},
		{"workflows/develop/questions", "done"},
		{"workflows/develop/revise", "done"},
		{"gate:plan", "approved"},
		{"workflows/implement/build-step/implement", "done"},
		{"workflows/review/perspectives/review", "done"},
		{"workflows/review/perspectives/check-review", "done"},
		{"workflows/review/cross-cutting/explore", "none_found"},
		{"workflows/develop/fix", "done"},
		{"workflows/develop/recheck", "unresolved"},
		{"workflows/develop/fix", "done"},
		{"workflows/develop/recheck", "unresolved"},
		{"workflows/develop/fix", "done"},
		{"workflows/develop/recheck", "unresolved"},
		{"workflows/develop/report", "done"},
	}
	var lastImpl, fixOcc string
	fixes, rechecks := 0, 0
	for i, want := range script {
		s := mustAdvance(t, e)
		var at string
		switch s.Kind {
		case StatusAgent:
			at = s.Task.Workflow + "/" + s.Task.Node
		case StatusGate:
			at = "gate:" + s.Gate.Gate
		default:
			t.Fatalf("step %d: want %s, got %+v", i, want.at, s)
		}
		if at != want.at {
			t.Fatalf("step %d: want %s, got %s", i, want.at, at)
		}
		if s.Kind == StatusGate {
			if err := e.Decide(ctx, "r", s.Occurrence, Decision{Outcome: want.outcome, TargetHash: s.Gate.TargetHash}); err != nil {
				t.Fatalf("step %d: %v", i, err)
			}
			continue
		}
		checkContinues(t, i, s.Task, lastImpl)
		switch s.Task.Agent.Name {
		case "implementer":
			lastImpl = s.Occurrence
		case "fixer":
			in := string(r.data[s.Task.Inputs["findings"].Occurrence+"/findings"])
			if !strings.Contains(in, `"f2"`) {
				t.Fatalf("step %d: the fixer's ledger lost f2: %s", i, in)
			}
			if fixes == 0 {
				if !strings.Contains(in, `"f1"`) {
					t.Fatalf("step %d: the first fixer's ledger must hold f1: %s", i, in)
				}
				r.outputs["findings"] = []byte("[" + finding("f1", disputed) + "," + finding("f2", disputed) + "]")
			} else {
				if strings.Contains(in, `"f1"`) {
					t.Fatalf("step %d: f1 was withdrawn on unresolved, but the fixer's ledger still holds it: %s", i, in)
				}
				r.outputs["findings"] = []byte("[" + finding("f2", disputed) + "]")
			}
			fixes++
			fixOcc = s.Occurrence
		case "rechecker":
			if in := s.Task.Inputs["findings"]; in.Occurrence != fixOcc {
				t.Fatalf("step %d: the rechecker must read the fixer's findings, got %+v", i, in)
			}
			got := string(r.data[fixOcc+"/findings"])
			if !strings.Contains(got, `"disputed":true`) || !strings.Contains(got, `"response"`) {
				t.Fatalf("step %d: the dispute did not reach the ledger: %s", i, got)
			}
			if rechecks == 0 {
				r.outputs["findings"] = []byte("[" + finding("f1", disputed+`,"withdrawn":true`) + "]")
			} else {
				r.outputs["findings"] = []byte(`[]`)
			}
			rechecks++
		}
		if err := e.ReportResult(ctx, "r", s.Occurrence, want.outcome, ""); err != nil {
			t.Fatalf("step %d (%s): %v", i, want.at, err)
		}
	}
	if fixes != 3 || rechecks != 3 {
		t.Fatalf("want three fixes and three rechecks, got %d and %d", fixes, rechecks)
	}
	if g := mustAdvance(t, e); g.Kind != StatusGate || g.Gate.Gate != "review" {
		t.Fatalf("want the review gate, got %+v", g)
	}
}

const emptyChecklist = `{"claims":[],"sets":[],"items":[],"not_covered":[]}`

// TestBundledDevelopAsksOpenChecksBeforeThePlanGate は、計画への問いに
// plan-reviserが答えきれずneeds_humanで返す経路を歩かせる。openの問いを
// 載せた計画がplan-interviewerに渡り、人間の答えがrevise-answeredにだけ
// 入力として届き、答えを反映した計画（checksがaddressed）がplanゲートに出る。
// 最初のreviseはanswersを読まない。
func TestBundledDevelopAsksOpenChecksBeforeThePlanGate(t *testing.T) {
	set, err := Load(nil, Bundled())
	if err != nil {
		t.Fatal(err)
	}
	if p := set.Check("workflows/develop"); len(p) != 0 {
		t.Fatalf("Check develop: %+v", p)
	}
	const steps = `"steps":[{"number":1,"title":"t1","description":"a","tests":[],"files":["a.go"]}],"alternatives":[],"risks":[],"expected_byproducts":[]`
	plan := `{"goal":"g","summary":"s",` + steps + `,"checks":[]}`
	openPlan := `{"goal":"g","summary":"s",` + steps + `,"checks":[{"id":"SPEC-1","category":"spec","question":"既存の設定ファイルも読めるか","answer":"旧形式を残すかを決められない","status":"open"},{"id":"DATA-1","category":"data","question":"移行で値が失われないか","answer":"ステップ1で旧値を写す","status":"addressed"}]}`
	answeredPlan := `{"goal":"g","summary":"s",` + steps + `,"checks":[{"id":"SPEC-1","category":"spec","question":"既存の設定ファイルも読めるか","answer":"人間の答え: 旧形式は読まない","status":"out_of_scope"},{"id":"DATA-1","category":"data","question":"移行で値が失われないか","answer":"ステップ1で旧値を写す","status":"addressed"}]}`
	checklist := `{"claims":[{"text":"g","source":"plan"}],"sets":[{"name":"設定ファイル","members":[{"target":"a.toml","handling":"ステップ1"}]}],"items":[{"id":"SPEC-1","category":"spec","question":"既存の設定ファイルも読めるか","reason":"a.goのloadが旧形式を読む","hint":"a.go:10"},{"id":"DATA-1","category":"data","question":"移行で値が失われないか","reason":"ステップ1が形式を変える","hint":"a.go:20"}],"not_covered":[]}`
	r := &bundledRunner{e5Runner: e5Runner{
		fakeRunner: fakeRunner{data: map[string][]byte{"/instructions": []byte("x")}, outputs: map[string][]byte{
			"investigation":  []byte("i"),
			"plan":           []byte(plan),
			"plan-checklist": []byte(checklist),
		}},
	}}
	e := New(set, &kvStore{m: map[string][]byte{}}, r, Options{})
	ctx := context.Background()
	if err := e.Start(ctx, "r", "workflows/develop", []string{"instructions"}); err != nil {
		t.Fatal(err)
	}
	type step struct{ at, outcome string }
	script := []step{
		{"workflows/develop/investigate", "done"},
		{"workflows/develop/plan", "done"},
		{"workflows/develop/questions", "done"},
		{"workflows/develop/revise", "needs_human"},
		{"workflows/develop/ask", "done"},
		{"workflows/develop/revise-answered", "done"},
	}
	var planOcc, reviseOcc, askOcc, answeredOcc string
	for i, want := range script {
		s := mustAdvance(t, e)
		if s.Kind != StatusAgent {
			t.Fatalf("step %d: want %s, got %+v", i, want.at, s)
		}
		if at := s.Task.Workflow + "/" + s.Task.Node; at != want.at {
			t.Fatalf("step %d: want %s, got %s", i, want.at, at)
		}
		_, hasAnswers := s.Task.Inputs["answers"]
		switch s.Task.Node {
		case "plan":
			planOcc = s.Occurrence
		case "questions":
			if s.Task.Agent.Name != "plan-questions" || s.Task.Inputs["plan"].Occurrence != planOcc {
				t.Fatalf("step %d: plan-questions must read the planner's plan, got %s %+v", i, s.Task.Agent.Name, s.Task.Inputs)
			}
		case "revise":
			if s.Task.Agent.Name != "plan-reviser" || hasAnswers {
				t.Fatalf("step %d: the first revise is plan-reviser without answers, got %s %+v", i, s.Task.Agent.Name, s.Task.Inputs)
			}
			if _, ok := s.Task.Inputs["plan-checklist"]; !ok {
				t.Fatalf("step %d: plan-reviser must read the checklist, got %+v", i, s.Task.Inputs)
			}
			reviseOcc = s.Occurrence
			r.outputs["plan"] = []byte(openPlan)
		case "ask":
			if s.Task.Agent.Name != "plan-interviewer" || s.Task.Inputs["plan"].Occurrence != reviseOcc {
				t.Fatalf("step %d: plan-interviewer must read the plan with open checks, got %s %+v", i, s.Task.Agent.Name, s.Task.Inputs)
			}
			askOcc = s.Occurrence
			if err := e.Answer(ctx, "r", s.Occurrence, Answer{Answers: map[string]string{"SPEC-1": "旧形式は読まない"}}); err != nil {
				t.Fatal(err)
			}
		case "revise-answered":
			if s.Task.Agent.Name != "plan-reviser" {
				t.Fatalf("step %d: want plan-reviser, got %s", i, s.Task.Agent.Name)
			}
			if in := s.Task.Inputs["answers"]; !hasAnswers || in.Occurrence != askOcc {
				t.Fatalf("step %d: revise-answered must read the human's answers, got %+v", i, s.Task.Inputs)
			}
			if in := s.Task.Inputs["plan"]; in.Occurrence != reviseOcc {
				t.Fatalf("step %d: revise-answered must read the plan with open checks, got %+v", i, in)
			}
			answeredOcc = s.Occurrence
			r.outputs["plan"] = []byte(answeredPlan)
		}
		if err := e.ReportResult(ctx, "r", s.Occurrence, want.outcome, ""); err != nil {
			t.Fatalf("step %d (%s): %v", i, want.at, err)
		}
	}
	if got := string(r.data[askOcc+"/answers"]); !strings.Contains(got, "旧形式は読まない") {
		t.Fatalf("the human's answers were not stored: %s", got)
	}
	g := mustAdvance(t, e)
	if g.Kind != StatusGate || g.Gate.Gate != "plan" {
		t.Fatalf("want the plan gate, got %+v", g)
	}
	if got := string(r.data[answeredOcc+"/plan"]); !strings.Contains(got, `"out_of_scope"`) || strings.Contains(got, `"open"`) {
		t.Fatalf("the plan at the gate must carry the answered checks: %s", got)
	}
}

// TestBundledPlanChecksSchemas は、計画のchecksと問いのplan-checklistの
// 同梱スキーマを確かめる。
func TestBundledPlanChecksSchemas(t *testing.T) {
	set, err := Load(nil, Bundled())
	if err != nil {
		t.Fatal(err)
	}
	e := New(set, &kvStore{m: map[string][]byte{}}, &fakeRunner{}, Options{})
	const steps = `"goal":"g","summary":"s","steps":[{"number":1,"title":"t1","description":"a","tests":[],"files":["a.go"]}],"alternatives":[],"risks":[]`
	item := `{"id":"SPEC-1","category":"spec","question":"q か","reason":"r","hint":"h"}`
	cases := []struct {
		name, data string
		ok         bool
	}{
		{"plan", `{` + steps + `,"checks":[]}`, true},
		{"plan", `{` + steps + `,"checks":[{"id":"SECURITY-2","category":"security","question":"q","answer":"ステップ1で扱う","status":"addressed"},{"id":"SPEC-1","category":"spec","question":"q","answer":"","status":"open"}]}`, true},
		{"plan", `{` + steps + `}`, false},
		{"plan", `{` + steps + `,"checks":[{"id":"SPEC-1","category":"edge","question":"q","answer":"a","status":"addressed"}]}`, false},
		{"plan", `{` + steps + `,"checks":[{"id":"SPEC-1","category":"spec","question":"q","answer":"a","status":"done"}]}`, false},
		{"plan", `{` + steps + `,"checks":[{"id":"SPEC-1","category":"spec","question":"q","answer":"","status":"addressed"}]}`, false},
		{"plan", `{` + steps + `,"checks":[{"id":"spec1","category":"spec","question":"q","answer":"a","status":"addressed"}]}`, false},
		{"plan-checklist", emptyChecklist, true},
		{"plan-checklist", `{"claims":[{"text":"c","source":"instructions"}],"sets":[{"name":"n","members":[{"target":"a","handling":"ステップ1"}]}],"items":[` + item + `],"not_covered":[{"scope":"s","reason":"r"}]}`, true},
		{"plan-checklist", `{"claims":[],"sets":[],"items":[{"id":"SPEC-1","category":"spec","question":"q か","reason":"r","hint":"h","answer":"a"}],"not_covered":[]}`, false},
		{"plan-checklist", `{"claims":[{"text":"c","source":"review"}],"sets":[],"items":[],"not_covered":[]}`, false},
		{"plan-checklist", `{"claims":[],"sets":[],"items":[]}`, false},
	}
	for _, c := range cases {
		if err := e.validateData(c.name, []byte(c.data)); (err == nil) != c.ok {
			t.Errorf("%s %s: valid=%v want %v (%v)", c.name, c.data, err == nil, c.ok, err)
		}
	}
}

func TestBundledCommentManifestSchema(t *testing.T) {
	set, err := Load(nil, Bundled())
	if err != nil {
		t.Fatal(err)
	}
	if !set.accumulates("comment-manifest") {
		t.Fatal("comment-manifest must be accumulated data")
	}
	e := New(set, &kvStore{m: map[string][]byte{}}, &fakeRunner{}, Options{})
	cases := []struct {
		data string
		ok   bool
	}{
		{`[]`, true},
		{`[{"file":"a.go","line":3,"kind":"choice","note":"mapではなくsliceにした理由"},{"file":"pkg/b.go","line":10,"kind":"nolint","note":"errcheckを外す理由を同じ行に書いた"}]`, true},
		{`[{"file":"a.go","line":3,"kind":"history","note":"n"}]`, false},
		{`[{"file":"a.go","line":3,"kind":"summary","note":""}]`, false},
		{`[{"file":"a.go","line":3,"kind":"summary","note":"  "}]`, false},
		{`[{"file":"a.go","line":0,"kind":"summary","note":"n"}]`, false},
		{`[{"file":"/a.go","line":3,"kind":"summary","note":"n"}]`, false},
		{`[{"file":"a.go","line":3,"kind":"summary"}]`, false},
		{`[{"file":"a.go","line":3,"kind":"summary","note":"n","extra":1}]`, false},
		{`{"file":"a.go","line":3,"kind":"summary","note":"n"}`, false},
	}
	for _, c := range cases {
		if err := e.validateData("comment-manifest", []byte(c.data)); (err == nil) != c.ok {
			t.Errorf("%s: valid=%v want %v (%v)", c.data, err == nil, c.ok, err)
		}
	}
}
