package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
)

var (
	nodeNameRe = regexp.MustCompile(`^[a-z0-9-]+$`)
	dataNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	outcomeRe  = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	// Reference paths are restricted to the same alphabet as names so that
	// every file a repo can hold can also be referred to from a definition.
	refSegmentRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
)

// engineData are the data items the engine computes itself; no agent or node
// may claim to write them.
var engineData = map[string]bool{"diff": true, "step-diff": true, "fix-diff": true}

// loadError collects every shape problem found while loading so that one
// Load reports them all instead of stopping at the first.
type loadError struct {
	problems []Problem
}

func (l *loadError) add(ref, node, format string, args ...any) {
	l.problems = append(l.problems, Problem{Path: ref, Node: node, Message: fmt.Sprintf(format, args...)})
}

func (l *loadError) err() error {
	if len(l.problems) == 0 {
		return nil
	}
	errs := make([]error, len(l.problems))
	for i, p := range l.problems {
		if p.Node != "" {
			errs[i] = fmt.Errorf("%s: node %s: %s", p.Path, p.Node, p.Message)
		} else {
			errs[i] = fmt.Errorf("%s: %s", p.Path, p.Message)
		}
	}
	return errors.Join(errs...)
}

type defFile struct {
	data   []byte
	origin Origin
}

// kinds lists each definition directory with the file extension it holds.
var kinds = []struct{ dir, ext string }{
	{"agents", ".md"},
	{"schemas", ".json"},
	{"workflows", ".yaml"},
}

func load(repo fs.FS, bundled fs.FS) (*Set, error) {
	le := &loadError{}
	files := map[string]defFile{}
	if bundled != nil {
		collect(bundled, OriginBundled, files, le)
	}
	if repo != nil {
		collect(repo, OriginRepo, files, le)
	}
	if err := le.err(); err != nil {
		return nil, err
	}

	set := &Set{
		Workflows: map[string]*Workflow{},
		Agents:    map[string]*Agent{},
		Schemas:   map[string][]byte{},
		Origins:   map[string]Origin{},
	}
	refs := make([]string, 0, len(files))
	for ref := range files {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	for _, ref := range refs {
		f := files[ref]
		set.Origins[ref] = f.origin
		dir, rest, _ := strings.Cut(ref, "/")
		switch dir {
		case "workflows":
			if wf := parseWorkflow(ref, f.data, le); wf != nil {
				set.Workflows[ref] = wf
			}
		case "agents":
			if a := parseAgent(ref, rest, f.data, le); a != nil {
				set.Agents[rest] = a
			}
		case "schemas":
			if strings.Contains(rest, "/") || !dataNameRe.MatchString(rest) {
				le.add(ref, "", "schema file name must be a data name (%s)", dataNameRe)
				continue
			}
			if checkSchema(ref, f.data, le) {
				set.Schemas[rest] = f.data
			}
		}
	}
	if err := le.err(); err != nil {
		return nil, err
	}
	return set, nil
}

// collect reads the definition directories of fsys into files, keyed by
// reference path. A later call overwrites entries of an earlier one, which is
// how a repo file replaces the bundled file at the same path.
func collect(fsys fs.FS, origin Origin, files map[string]defFile, le *loadError) {
	for _, k := range kinds {
		dir, ext := k.dir, k.ext
		if _, err := fs.Stat(fsys, dir); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		err := fs.WalkDir(fsys, dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if strings.HasPrefix(d.Name(), ".") {
				if d.IsDir() && p != dir {
					return fs.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			if path.Ext(p) != ext {
				le.add(p, "", "%s/ holds only %s files", dir, ext)
				return nil
			}
			ref := strings.TrimSuffix(p, ext)
			for _, seg := range strings.Split(strings.TrimPrefix(ref, dir+"/"), "/") {
				if !refSegmentRe.MatchString(seg) {
					le.add(p, "", "path segment %q must match %s", seg, refSegmentRe)
					return nil
				}
			}
			data, err := fs.ReadFile(fsys, p)
			if err != nil {
				le.add(p, "", "read: %v", err)
				return nil
			}
			files[ref] = defFile{data: data, origin: origin}
			return nil
		})
		if err != nil {
			le.add(dir, "", "%s definitions: %v", origin, err)
		}
	}
}

func isRef(s, dir string) bool {
	rest, ok := strings.CutPrefix(s, dir+"/")
	if !ok {
		return false
	}
	for _, seg := range strings.Split(rest, "/") {
		if !refSegmentRe.MatchString(seg) {
			return false
		}
	}
	return true
}
