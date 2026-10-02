package engine

import (
	"bytes"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const draft2020 = "https://json-schema.org/draft/2020-12/schema"

// checkSchema verifies a schema file is a draft 2020-12 JSON Schema that
// compiles. The Set keeps the raw bytes; validation compiles them again
// where data is received.
func checkSchema(ref string, data []byte, le *loadError) bool {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		le.add(ref, "", "json: %v", err)
		return false
	}
	if m, ok := doc.(map[string]any); ok {
		if s, ok := m["$schema"]; ok && s != draft2020 {
			le.add(ref, "", "$schema must be %s", draft2020)
			return false
		}
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	url := "mem:///" + ref + ".json"
	if err := c.AddResource(url, doc); err != nil {
		le.add(ref, "", "schema: %v", err)
		return false
	}
	if _, err := c.Compile(url); err != nil {
		le.add(ref, "", "schema: %v", err)
		return false
	}
	return true
}
