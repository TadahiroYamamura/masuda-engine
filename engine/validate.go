package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// validateData checks a value received from the sandbox: against its schema
// when the data name has one, otherwise only that it is not empty. Schemas
// are compiled per call because Engine has no room for a cache in its
// contract-fixed fields, and outputs arrive at agent pace, not in bulk.
func (e *Engine) validateData(name string, content []byte) error {
	if e.set.accumulates(name) {
		if _, err := jsonArray(content); err != nil {
			return err
		}
	}
	raw, ok := e.set.Schemas[name]
	if !ok {
		if len(bytes.TrimSpace(content)) == 0 {
			return errors.New("空である")
		}
		return nil
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("schema %s: %w", name, err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	url := "mem:///schemas/" + name + ".json"
	if err := c.AddResource(url, doc); err != nil {
		return fmt.Errorf("schema %s: %w", name, err)
	}
	sch, err := c.Compile(url)
	if err != nil {
		return fmt.Errorf("schema %s: %w", name, err)
	}
	var v any
	if stringSchema(raw) {
		v = string(content)
	} else if v, err = jsonschema.UnmarshalJSON(bytes.NewReader(content)); err != nil {
		return fmt.Errorf("JSONとして読めない: %w", err)
	}
	return sch.Validate(v)
}

// stringSchema reports whether a schema's top level is type string: such
// data is the file's content as one string, not JSON.
func stringSchema(raw []byte) bool {
	var s struct {
		Type any `json:"type"`
	}
	if json.Unmarshal(raw, &s) != nil {
		return false
	}
	return s.Type == "string"
}
