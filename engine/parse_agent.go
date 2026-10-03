package engine

import (
	"bytes"
	"fmt"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// reservedAgentOutcomes are outcomes only the engine produces
// (docs/workflow-schema.md "エージェント定義").
var reservedAgentOutcomes = map[string]bool{
	OutcomeExhausted: true, OutcomeBlocked: true, OutcomeFailed: true,
}

// agentEfforts are the effort levels Claude Code accepts in a subagent's
// frontmatter. model is not checked: its vocabulary (aliases, full IDs,
// inherit) belongs to Claude Code and changes with each release, whereas
// effort is a short fixed set where a typo would otherwise be silently ignored.
var agentEfforts = []string{"low", "medium", "high", "xhigh", "max"}

// parseAgent reads agents/<key>.md. The Set keys agents by key (the reference
// path without "agents/"), and the frontmatter name must agree with it so a
// task's Agent.Name always says which file it came from.
func parseAgent(ref, key string, data []byte, le *loadError) *Agent {
	front, body, err := splitFrontmatter(data)
	if err != nil {
		le.add(ref, "", "%v", err)
		return nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(front, &doc); err != nil {
		le.add(ref, "", "frontmatter yaml: %v", err)
		return nil
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 {
		le.add(ref, "", "frontmatter must be one YAML mapping")
		return nil
	}
	pairs, err := mappingPairs(doc.Content[0])
	if err != nil {
		le.add(ref, "", "frontmatter %v", err)
		return nil
	}
	a := &Agent{Body: body}
	before := len(le.problems)
	seen := map[string]bool{}
	for _, p := range pairs {
		seen[p.key] = true
		err = nil
		switch p.key {
		case "name":
			a.Name, err = nonEmptyScalar(p.val)
			if err == nil && a.Name != key {
				err = fmt.Errorf("%q must equal the file's path under agents/ (%q)", a.Name, key)
			}
		case "description":
			a.Description, err = nonEmptyScalar(p.val)
		case "tools":
			a.Tools, err = parseTools(p.val)
		case "inputs":
			a.Inputs, err = dataNames(p.val)
		case "outputs":
			a.Outputs, err = dataNames(p.val)
			for _, o := range a.Outputs {
				if err == nil && engineData[o] {
					err = fmt.Errorf("%q is computed by the engine", o)
				}
			}
		case "outcomes":
			a.Outcomes, err = parseOutcomes(p.val)
		case "model":
			a.Model, err = nonEmptyScalar(p.val)
		case "effort":
			a.Effort, err = parseEffort(p.val)
		default:
			err = fmt.Errorf("unknown key")
		}
		if err != nil {
			le.add(ref, "", "%s: %v", p.key, err)
		}
	}
	for _, k := range []string{"name", "description", "outcomes"} {
		if !seen[k] {
			le.add(ref, "", "missing %q", k)
		}
	}
	if body == "" {
		le.add(ref, "", "the prompt body is empty")
	}
	if len(le.problems) != before {
		return nil
	}
	return a
}

// splitFrontmatter separates "---\n<yaml>\n---\n<body>". The body is trimmed.
func splitFrontmatter(data []byte) (front []byte, body string, err error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	rest, ok := bytes.CutPrefix(data, []byte("---\n"))
	if !ok {
		return nil, "", fmt.Errorf("must start with a --- frontmatter block")
	}
	if bytes.HasPrefix(rest, []byte("---\n")) || bytes.Equal(rest, []byte("---")) {
		return nil, "", fmt.Errorf("frontmatter is empty")
	}
	if i := bytes.Index(rest, []byte("\n---\n")); i >= 0 {
		return rest[:i+1], strings.TrimSpace(string(rest[i+5:])), nil
	}
	if bytes.HasSuffix(rest, []byte("\n---")) {
		return rest[:len(rest)-3], "", nil
	}
	return nil, "", fmt.Errorf("frontmatter is not closed by ---")
}

// parseTools accepts a list or a comma-separated string. Present but empty
// yields a non-nil empty slice (no tools), unlike an absent key (all tools).
func parseTools(v *yaml.Node) ([]string, error) {
	out := []string{}
	switch v.Kind {
	case yaml.SequenceNode:
		items, err := scalarList(v)
		if err != nil {
			return nil, err
		}
		for _, t := range items {
			if t = strings.TrimSpace(t); t != "" {
				out = append(out, t)
			}
		}
	case yaml.ScalarNode:
		if v.Tag == "!!null" {
			return out, nil
		}
		for _, t := range strings.Split(v.Value, ",") {
			if t = strings.TrimSpace(t); t != "" {
				out = append(out, t)
			}
		}
	default:
		return nil, fmt.Errorf("must be a list or a comma-separated string (line %d)", v.Line)
	}
	return out, nil
}

func parseEffort(v *yaml.Node) (string, error) {
	s, err := scalar(v)
	if err != nil || !slices.Contains(agentEfforts, s) {
		return "", fmt.Errorf("must be one of %s (line %d)", strings.Join(agentEfforts, ", "), v.Line)
	}
	return s, nil
}

func parseOutcomes(v *yaml.Node) (map[string]string, error) {
	pairs, err := mappingPairs(v)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, p := range pairs {
		if !outcomeRe.MatchString(p.key) {
			return nil, fmt.Errorf("outcome %q must match %s", p.key, outcomeRe)
		}
		if reservedAgentOutcomes[p.key] {
			return nil, fmt.Errorf("outcome %q is reserved for the engine", p.key)
		}
		desc, err := nonEmptyScalar(p.val)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", p.key, err)
		}
		out[p.key] = desc
	}
	if _, ok := out[OutcomeDone]; !ok {
		return nil, fmt.Errorf("must declare %q", OutcomeDone)
	}
	return out, nil
}
