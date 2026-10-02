package engine

import (
	"fmt"

	"go.yaml.in/yaml/v3"
)

// pair is one key/value of a YAML mapping, kept in file order.
type pair struct {
	key string
	val *yaml.Node
}

// mappingPairs returns the pairs of a mapping node, rejecting duplicate and
// non-scalar keys (decoding into yaml.Node does not reject duplicates).
func mappingPairs(n *yaml.Node) ([]pair, error) {
	if n.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("must be a mapping (line %d)", n.Line)
	}
	seen := map[string]bool{}
	out := make([]pair, 0, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i]
		if k.Kind != yaml.ScalarNode {
			return nil, fmt.Errorf("keys must be scalars (line %d)", k.Line)
		}
		if seen[k.Value] {
			return nil, fmt.Errorf("duplicate key %q (line %d)", k.Value, k.Line)
		}
		seen[k.Value] = true
		out = append(out, pair{key: k.Value, val: n.Content[i+1]})
	}
	return out, nil
}

func scalar(n *yaml.Node) (string, error) {
	if n.Kind != yaml.ScalarNode || n.Tag == "!!null" {
		return "", fmt.Errorf("must be a scalar value (line %d)", n.Line)
	}
	return n.Value, nil
}

func nonEmptyScalar(n *yaml.Node) (string, error) {
	s, err := scalar(n)
	if err == nil && s == "" {
		err = fmt.Errorf("must not be empty (line %d)", n.Line)
	}
	return s, err
}

func scalarList(n *yaml.Node) ([]string, error) {
	if n.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("must be a list (line %d)", n.Line)
	}
	out := make([]string, 0, len(n.Content))
	for _, c := range n.Content {
		s, err := nonEmptyScalar(c)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func intValue(n *yaml.Node) (int, error) {
	var v int
	if n.Kind != yaml.ScalarNode || n.Tag != "!!int" {
		return 0, fmt.Errorf("must be an integer (line %d)", n.Line)
	}
	if err := n.Decode(&v); err != nil {
		return 0, err
	}
	return v, nil
}

// dataNames parses a list of data names, rejecting malformed and repeated
// names.
func dataNames(n *yaml.Node) ([]string, error) {
	names, err := scalarList(n)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, d := range names {
		if !dataNameRe.MatchString(d) {
			return nil, fmt.Errorf("data name %q must match %s", d, dataNameRe)
		}
		if seen[d] {
			return nil, fmt.Errorf("data name %q listed twice", d)
		}
		seen[d] = true
	}
	return names, nil
}
