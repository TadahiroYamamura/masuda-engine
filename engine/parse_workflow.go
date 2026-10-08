package engine

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

var (
	egressRe = regexp.MustCompile(`^(\*\.)?[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)*$`)
	secretRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	// privilegedNameRe is the shape of a name in masuda's settings.json
	// `privilegedCommands`, checked here like egress so that a typo fails at
	// load rather than when the node is reached. Whether the name is declared
	// and approved is the host's to say at run time.
	privilegedNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// allowedKeys is, per node type, every key besides `type` and `next` that the
// node may carry (docs/workflow-schema.md "種類ごとのキー").
var allowedKeys = map[NodeType]map[string]bool{
	NodeAgent:    keySet("role", "continues", "max", "inputs", "outputs", "egress", "secrets"),
	NodeExec:     keySet("command", "inputs", "outputs", "max", "egress", "secrets", "timeout"),
	NodeApproval: keySet("gate", "target"),
	NodeQuestion: keySet("role", "questions", "outputs"),
	NodeForeach:  keySet("over", "body", "on_incomplete", "with", "max"),
	NodeWorkflow: keySet("workflow", "with", "max"),
	NodeCommit:   keySet("scope"),
	NodePublish:  keySet("target", "export"),
	NodeDiscard:  keySet("export"),

	// The host's declaration fixes what a privileged command reads, writes,
	// reaches and how long it may take, so the node carries none of them.
	NodePrivileged: keySet("name", "max"),
}

var requiredKeys = map[NodeType][]string{
	NodeAgent:    {"role"},
	NodeExec:     {"command"},
	NodeApproval: {"gate", "target"},
	NodeQuestion: {"outputs"},
	NodeForeach:  {"over", "body"},
	NodeWorkflow: {"workflow"},
	NodeCommit:   {"scope"},

	NodePrivileged: {"name"},
}

// reservedEndLabels cannot follow `end:`. `done` is spelled as plain `end`;
// the others are outcomes only the engine produces.
var reservedEndLabels = map[string]bool{
	OutcomeDone: true, OutcomeExhausted: true, OutcomeBlocked: true, OutcomeFailed: true,
}

func keySet(keys ...string) map[string]bool {
	m := map[string]bool{}
	for _, k := range keys {
		m[k] = true
	}
	return m
}

func parseWorkflow(ref string, data []byte, le *loadError) *Workflow {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		le.add(ref, "", "yaml: %v", err)
		return nil
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 {
		le.add(ref, "", "must be one YAML document")
		return nil
	}
	top, err := mappingPairs(doc.Content[0])
	if err != nil {
		le.add(ref, "", "workflow %v", err)
		return nil
	}
	wf := &Workflow{Path: ref, Nodes: map[string]*Node{}, UserInvocable: true}
	before := len(le.problems)
	var nodesNode *yaml.Node
	seen := map[string]bool{}
	for _, p := range top {
		seen[p.key] = true
		switch p.key {
		case "version":
			v, err := intValue(p.val)
			if err != nil {
				le.add(ref, "", "version %v", err)
			} else if v != 1 {
				le.add(ref, "", "version %d is not supported (want 1)", v)
			}
			wf.Version = v
		case "inputs":
			wf.Inputs, err = dataNames(p.val)
			if err != nil {
				le.add(ref, "", "inputs: %v", err)
			}
		case "start":
			wf.Start, err = nonEmptyScalar(p.val)
			if err != nil {
				le.add(ref, "", "start %v", err)
			}
		case "nodes":
			nodesNode = p.val
		case "user_invocable":
			wf.UserInvocable, err = boolValue(p.val)
			if err != nil {
				le.add(ref, "", "user_invocable %v", err)
			}
		default:
			le.add(ref, "", "unknown key %q", p.key)
		}
	}
	for _, k := range []string{"version", "start", "nodes"} {
		if !seen[k] {
			le.add(ref, "", "missing %q", k)
		}
	}
	if nodesNode != nil {
		pairs, err := mappingPairs(nodesNode)
		if err != nil {
			le.add(ref, "", "nodes %v", err)
		} else if len(pairs) == 0 {
			le.add(ref, "", "nodes must not be empty")
		}
		for _, p := range pairs {
			if p.key == "end" || !nodeNameRe.MatchString(p.key) {
				le.add(ref, p.key, "node name must match %s and not be \"end\"", nodeNameRe)
				continue
			}
			wf.Order = append(wf.Order, p.key)
			if n := parseNode(ref, p.key, p.val, le); n != nil {
				wf.Nodes[p.key] = n
			}
		}
	}
	if len(le.problems) != before {
		return nil
	}

	// References inside the file are shape: they need nothing but this file.
	if _, ok := wf.Nodes[wf.Start]; !ok {
		le.add(ref, "", "start %q is not a node", wf.Start)
	}
	for _, id := range wf.Order {
		n := wf.Nodes[id]
		for outcome, t := range n.Next {
			if !t.End {
				if _, ok := wf.Nodes[t.Node]; !ok {
					le.add(ref, id, "next.%s: no node %q", outcome, t.Node)
				}
			}
		}
		if from := overFrom(n.Over); from != "" {
			if _, ok := wf.Nodes[from]; !ok {
				le.add(ref, id, "over: no node %q", from)
			}
		}
	}
	if len(le.problems) != before {
		return nil
	}
	return wf
}

func parseNode(ref, id string, v *yaml.Node, le *loadError) *Node {
	pairs, err := mappingPairs(v)
	if err != nil {
		le.add(ref, id, "node %v", err)
		return nil
	}
	n := &Node{ID: id}
	vals := map[string]*yaml.Node{}
	for _, p := range pairs {
		vals[p.key] = p.val
	}
	tv, ok := vals["type"]
	if !ok {
		le.add(ref, id, "missing \"type\"")
		return nil
	}
	ts, _ := scalar(tv)
	n.Type = NodeType(ts)
	allowed, ok := allowedKeys[n.Type]
	if !ok {
		le.add(ref, id, "unknown type %q", ts)
		return nil
	}
	before := len(le.problems)
	for _, p := range pairs {
		if p.key != "type" && p.key != "next" && !allowed[p.key] {
			le.add(ref, id, "a type: %s node cannot have %q", n.Type, p.key)
		}
	}
	for _, k := range append([]string{"next"}, requiredKeys[n.Type]...) {
		if _, ok := vals[k]; !ok {
			le.add(ref, id, "a type: %s node needs %q", n.Type, k)
		}
	}
	if len(le.problems) != before {
		return nil
	}

	fail := func(key string, err error) {
		le.add(ref, id, "%s: %v", key, err)
	}
	for _, p := range pairs {
		val := p.val
		err = nil
		switch p.key {
		case "type":
		case "next":
			n.Next, err = parseNext(val)
		case "max":
			n.Max, err = intValue(val)
			if err == nil && n.Max < 1 {
				err = fmt.Errorf("must be at least 1")
			}
		case "role":
			n.Role, err = refValue(val, "agents")
		case "continues":
			n.Continues, err = refValue(val, "agents")
		case "command":
			n.Command, err = scalarList(val)
			if err == nil && len(n.Command) == 0 {
				err = fmt.Errorf("must not be empty")
			} else if err == nil && !path.IsAbs(n.Command[0]) {
				err = fmt.Errorf("%q must be an absolute path (argv, no shell)", n.Command[0])
			}
		case "name":
			n.PrivilegedName, err = nonEmptyScalar(val)
			if err == nil && !privilegedNameRe.MatchString(n.PrivilegedName) {
				err = fmt.Errorf("%q must match %s (a name in privilegedCommands)", n.PrivilegedName, privilegedNameRe)
			}
		case "timeout":
			var s string
			if s, err = nonEmptyScalar(val); err == nil {
				n.Timeout, err = time.ParseDuration(s)
				if err == nil && n.Timeout <= 0 {
					err = fmt.Errorf("must be positive")
				}
			}
		case "inputs":
			n.Inputs, err = dataNames(val)
		case "outputs":
			n.Outputs, err = dataNames(val)
			for _, o := range n.Outputs {
				if err == nil && engineData[o] {
					err = fmt.Errorf("%q is computed by the engine", o)
				}
			}
			if err == nil && n.Type == NodeQuestion && len(n.Outputs) != 1 {
				err = fmt.Errorf("a question node writes exactly one output")
			}
		case "egress":
			var hosts []string
			if hosts, err = scalarList(val); err == nil {
				for _, h := range hosts {
					if err == nil && strings.Contains(h, ":") {
						err = fmt.Errorf("egress %q: a port cannot be written (masuda's settings.json egress takes hosts only)", h)
					}
				}
			}
			if err == nil {
				n.Egress, err = matchingList(val, egressRe, "host")
			}
		case "secrets":
			n.Secrets, err = matchingList(val, secretRe, "secret name")
		case "gate":
			n.Gate, err = nonEmptyScalar(val)
			if err == nil && !nodeNameRe.MatchString(n.Gate) {
				err = fmt.Errorf("must match %s", nodeNameRe)
			}
		case "target":
			var s string
			s, err = nonEmptyScalar(val)
			if n.Type == NodePublish {
				if err == nil && s != "local" && s != "remote" {
					err = fmt.Errorf("must be local or remote")
				}
				n.PublishTarget = s
			} else {
				if err == nil && !dataNameRe.MatchString(s) {
					err = fmt.Errorf("must be plan, diff, step-diff, or a data name")
				}
				n.Target = s
			}
		case "questions":
			n.Questions, err = parseQuestions(val)
		case "over":
			n.Over, err = nonEmptyScalar(val)
			if err == nil && !validOver(n.Over) {
				err = fmt.Errorf("must be steps, perspectives, perspectives(from=<node>), findings, <data>[] or <data>[<field>=<value>]")
			}
		case "body":
			n.Body, err = refValue(val, "workflows")
		case "workflow":
			n.Workflow, err = refValue(val, "workflows")
		case "on_incomplete":
			n.OnIncomplete, err = nonEmptyScalar(val)
			if err == nil && n.OnIncomplete != "stop" && n.OnIncomplete != "continue" {
				err = fmt.Errorf("must be stop or continue")
			}
		case "with":
			n.With, err = parseWith(val)
		case "scope":
			n.Scope, err = nonEmptyScalar(val)
			if err == nil && n.Scope != "step" && n.Scope != "plan" {
				err = fmt.Errorf("must be step or plan")
			}
		case "export":
			n.Export, err = dataNames(val)
		}
		if err != nil {
			fail(p.key, err)
		}
	}
	if n.Type == NodeQuestion {
		_, hasRole := vals["role"]
		_, hasQ := vals["questions"]
		if hasRole == hasQ {
			le.add(ref, id, "a question node needs exactly one of \"role\" and \"questions\"")
		}
	}
	if n.Type == NodeForeach && n.OnIncomplete == "" {
		n.OnIncomplete = "stop"
	}
	if n.Type == NodePublish && n.PublishTarget == "" {
		n.PublishTarget = "local"
	}
	if len(le.problems) != before {
		return nil
	}
	return n
}

// parseNext reads `next`: a single target is shorthand for {done: target}.
func parseNext(v *yaml.Node) (map[string]Target, error) {
	if v.Kind == yaml.ScalarNode {
		s, err := nonEmptyScalar(v)
		if err != nil {
			return nil, err
		}
		t, err := parseTarget(s)
		if err != nil {
			return nil, err
		}
		return map[string]Target{OutcomeDone: t}, nil
	}
	pairs, err := mappingPairs(v)
	if err != nil {
		return nil, fmt.Errorf("must be a target or a mapping of outcome to target: %v", err)
	}
	if len(pairs) == 0 {
		return nil, fmt.Errorf("must not be empty")
	}
	out := map[string]Target{}
	for _, p := range pairs {
		if !outcomeRe.MatchString(p.key) {
			return nil, fmt.Errorf("outcome %q must match %s", p.key, outcomeRe)
		}
		s, err := nonEmptyScalar(p.val)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", p.key, err)
		}
		if out[p.key], err = parseTarget(s); err != nil {
			return nil, fmt.Errorf("%s: %v", p.key, err)
		}
	}
	return out, nil
}

func parseTarget(s string) (Target, error) {
	if s == "end" {
		return Target{End: true}, nil
	}
	if label, ok := strings.CutPrefix(s, "end:"); ok {
		if !outcomeRe.MatchString(label) {
			return Target{}, fmt.Errorf("end label %q must match %s", label, outcomeRe)
		}
		if reservedEndLabels[label] {
			return Target{}, fmt.Errorf("end label %q is reserved", label)
		}
		return Target{End: true, Label: label}, nil
	}
	if !nodeNameRe.MatchString(s) {
		return Target{}, fmt.Errorf("target %q must be a node name, end, or end:<label>", s)
	}
	return Target{Node: s}, nil
}

func refValue(v *yaml.Node, dir string) (string, error) {
	s, err := nonEmptyScalar(v)
	if err != nil {
		return "", err
	}
	if !isRef(s, dir) {
		return "", fmt.Errorf("%q must be a reference like %s/<name> (no extension)", s, dir)
	}
	return s, nil
}

func matchingList(v *yaml.Node, re *regexp.Regexp, what string) ([]string, error) {
	items, err := scalarList(v)
	if err != nil {
		return nil, err
	}
	for _, s := range items {
		if !re.MatchString(s) {
			return nil, fmt.Errorf("%s %q must match %s", what, s, re)
		}
	}
	return items, nil
}

func parseWith(v *yaml.Node) (map[string]string, error) {
	pairs, err := mappingPairs(v)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, p := range pairs {
		s, err := nonEmptyScalar(p.val)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", p.key, err)
		}
		if !dataNameRe.MatchString(p.key) || !dataNameRe.MatchString(s) {
			return nil, fmt.Errorf("%s: %s: both sides must be data names (%s)", p.key, s, dataNameRe)
		}
		out[p.key] = s
	}
	return out, nil
}

func parseQuestions(v *yaml.Node) ([]Question, error) {
	if v.Kind != yaml.SequenceNode || len(v.Content) == 0 {
		return nil, fmt.Errorf("must be a non-empty list (line %d)", v.Line)
	}
	seen := map[string]bool{}
	var out []Question
	for _, c := range v.Content {
		pairs, err := mappingPairs(c)
		if err != nil {
			return nil, err
		}
		var q Question
		for _, p := range pairs {
			switch p.key {
			case "id":
				q.ID, err = nonEmptyScalar(p.val)
			case "text":
				q.Text, err = nonEmptyScalar(p.val)
			case "options":
				q.Options, err = scalarList(p.val)
			default:
				err = fmt.Errorf("unknown key %q", p.key)
			}
			if err != nil {
				return nil, fmt.Errorf("line %d: %s: %v", c.Line, p.key, err)
			}
		}
		if q.ID == "" || q.Text == "" {
			return nil, fmt.Errorf("line %d: each question needs id and text", c.Line)
		}
		if seen[q.ID] {
			return nil, fmt.Errorf("question id %q repeated", q.ID)
		}
		seen[q.ID] = true
		out = append(out, q)
	}
	return out, nil
}
