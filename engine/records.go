package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
)

// The engine keeps no position in memory. What a run has done is recorded
// as one occurrence per node entry and one result per finished occurrence;
// where the run stands is recomputed from those on every call. Keys, all
// under "<run>/":
//
//	start             the root workflow and the run's inputs
//	occ/<id>          one node entry (id: zero-padded serial)
//	result/<id>       how that entry finished
//	frame/<fid>       one running workflow file (root, or a called workflow
//	                  whose fid is the calling occurrence's id)
//	frame-end/<fid>   the outcome the frame finished with
//	gate/<occ>        the gate request an approval occurrence opened
//	question/<occ>    the question request a question occurrence opened
//	concern/<occ>     (E6) a concern reported during an occurrence
//	blocked           why the run stopped
const (
	keyStart     = "start"
	keyBlocked   = "blocked"
	prefOcc      = "occ/"
	prefResult   = "result/"
	prefFrame    = "frame/"
	prefFrameEnd = "frame-end/"
	prefGate     = "gate/"
	prefQuestion = "question/"
	rootFrame    = "root"
	// Seven digits keep lexical and numeric order equal well past Fuse.
	idWidth = 7
)

type startRecord struct {
	Root   string   `json:"root"`
	Inputs []string `json:"inputs,omitempty"`
}

type occurrence struct {
	ID       string `json:"id"`
	Frame    string `json:"frame"`
	Workflow string `json:"workflow"`
	Node     string `json:"node"`
	Feedback string `json:"feedback,omitempty"`
	// Exhausted marks an entry beyond the node's max: the node does not run
	// and the occurrence finishes as exhausted at once.
	Exhausted bool `json:"exhausted,omitempty"`
	// Inputs, Outputs and Policy are fixed when an agent node is entered so
	// that a recomputed position hands out the same task.
	Inputs  map[string]DataRef `json:"inputs,omitempty"`
	Outputs []string           `json:"outputs,omitempty"`
	Policy  *Policy            `json:"policy,omitempty"`
}

type result struct {
	Outcome  string `json:"outcome"`
	Feedback string `json:"feedback,omitempty"`
	// Invalid marks an agent report the engine refused (missing or invalid
	// output). The same node is entered again.
	Invalid bool `json:"invalid,omitempty"`
	// Outputs are the data names accepted from this occurrence.
	Outputs []string `json:"outputs,omitempty"`
	// Snapshot is the worktree as an agent or exec occurrence left it.
	Snapshot SnapshotRef `json:"snapshot,omitempty"`
}

type frame struct {
	ID       string             `json:"id"`
	Workflow string             `json:"workflow"`
	Inputs   map[string]DataRef `json:"inputs,omitempty"`
	// Feedback is what was sent back to the calling node; the frame's first
	// node gets it, since the calling node is not an agent that could read
	// it.
	Feedback string `json:"feedback,omitempty"`
}

// records is every occurrence and result of a run, loaded once per move.
type records struct {
	occs    []*occurrence
	byFrame map[string][]*occurrence
	results map[string]*result
	// latest is, per data name, the occurrence that last wrote it.
	latest map[string]string
	maxID  int
}

func runKey(run RunID, k string) string { return string(run) + "/" + k }

func (e *Engine) loadRecords(run RunID) (*records, error) {
	r := &records{byFrame: map[string][]*occurrence{}, results: map[string]*result{}, latest: map[string]string{}}
	kvs, err := e.store.List(runKey(run, prefOcc))
	if err != nil {
		return nil, err
	}
	for _, kv := range kvs {
		var o occurrence
		if err := json.Unmarshal(kv.Value, &o); err != nil {
			return nil, fmt.Errorf("%s: %w", kv.Key, err)
		}
		r.occs = append(r.occs, &o)
		if n, err := strconv.Atoi(o.ID); err == nil && n > r.maxID {
			r.maxID = n
		}
	}
	sort.Slice(r.occs, func(i, j int) bool { return r.occs[i].ID < r.occs[j].ID })
	for _, o := range r.occs {
		r.byFrame[o.Frame] = append(r.byFrame[o.Frame], o)
	}
	kvs, err = e.store.List(runKey(run, prefResult))
	if err != nil {
		return nil, err
	}
	for _, kv := range kvs {
		var res result
		if err := json.Unmarshal(kv.Value, &res); err != nil {
			return nil, fmt.Errorf("%s: %w", kv.Key, err)
		}
		r.results[kv.Key[len(runKey(run, prefResult)):]] = &res
	}
	for _, o := range r.occs {
		if res := r.results[o.ID]; res != nil {
			for _, name := range res.Outputs {
				r.latest[name] = o.ID
			}
		}
	}
	return r, nil
}

func (r *records) last(frameID string) *occurrence {
	occs := r.byFrame[frameID]
	if len(occs) == 0 {
		return nil
	}
	return occs[len(occs)-1]
}

func (r *records) nextID() string { return fmt.Sprintf("%0*d", idWidth, r.maxID+1) }

var errMissing = errors.New("not found")

func (e *Engine) getJSON(run RunID, k string, v any) error {
	b, ok, err := e.store.Get(runKey(run, k))
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s: %w", runKey(run, k), errMissing)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", runKey(run, k), err)
	}
	return nil
}

// create writes v under k only if nothing is there yet. applied=false means
// another call got there first; the caller reloads and looks again.
func (e *Engine) create(run RunID, k string, v any) (bool, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return false, err
	}
	return e.createRaw(run, k, b)
}

func (e *Engine) createRaw(run RunID, k string, b []byte) (bool, error) {
	key := runKey(run, k)
	return e.store.Apply([]Op{{Kind: OpCheck, Key: key}, {Kind: OpPut, Key: key, Value: b}})
}
