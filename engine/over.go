package engine

import (
	"regexp"
	"strings"
)

var (
	fromOverRe = regexp.MustCompile(`^perspectives\(from=([a-z0-9-]+)\)$`)
	dataOverRe = regexp.MustCompile(`^([a-z][a-z0-9-]*)\[(?:([A-Za-z_][A-Za-z0-9_-]*)=([^\]]+))?\]$`)
)

// dataOver is a foreach over the elements of a JSON array data value, which
// the engine turns into items itself (docs/workflow-schema.md "foreach.over").
type dataOver struct {
	Data   string
	Filter bool
	Field  string
	Value  string // as written; compared by valueMatches
}

// parseDataOver reads `<data>[]`, `<data>[<field>=<value>]` and `findings`
// (short for `findings[]`). ok=false for the forms Runner.Items resolves.
func parseDataOver(over string) (dataOver, bool) {
	if over == "findings" {
		return dataOver{Data: "findings"}, true
	}
	m := dataOverRe.FindStringSubmatch(over)
	if m == nil {
		return dataOver{}, false
	}
	return dataOver{Data: m[1], Filter: m[2] != "", Field: m[2], Value: m[3]}, true
}

func validOver(over string) bool {
	if over == "steps" || over == "perspectives" || fromOverRe.MatchString(over) {
		return true
	}
	_, ok := parseDataOver(over)
	return ok
}

func overFrom(over string) string {
	if m := fromOverRe.FindStringSubmatch(over); m != nil {
		return m[1]
	}
	return ""
}

// overSource is the data a foreach's items are taken from, and itemInput the
// input name each item is handed to the body under.
func overSource(over string) string {
	if d, ok := parseDataOver(over); ok {
		return d.Data
	}
	switch {
	case over == "steps":
		return dataPlan
	case overFrom(over) != "":
		return dataSelectedPerspectives
	}
	return ""
}

func itemInput(over string) string {
	if d, ok := parseDataOver(over); ok {
		return singular(d.Data)
	}
	switch {
	case over == "steps":
		return "step"
	case strings.HasPrefix(over, "perspectives"):
		return "perspective"
	}
	return ""
}
