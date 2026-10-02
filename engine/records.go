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
//	frame/<fid>       one running workflow file (root, a called workflow
//	                  whose fid is the calling occurrence's id, or one
//	                  foreach iteration, fid "<occ>.<n>")
//	frame-end/<fid>   the outcome the frame finished with
//	gate/<occ>        the gate request an approval occurrence opened
//	question/<occ>    the question request a question occurrence opened
//	deviation/<occ>/<n>  the n-th deviation gate an occurrence opened
//	decision/<occ>/<n>   the human's decision on that gate
//	concern/<occ>/<n>      the n-th concern reported from an occurrence
//	triage-gate/<occ>/<n>  the triage gate opened for it
//	triage/<occ>/<n>       the human's decision on that gate
//	answer/<occ>/<n>       the n-th answer to a role question's ask_human
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
	prefDevGate  = "deviation/"
	prefDevDec   = "decision/"
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
	// Base is the snapshot the worktree is compared against: for agent and
	// exec, where the node started; for commit, where the work being
	// committed started. BaseHash is ChangedSince(Base) at entry, kept for
	// agents that cannot write.
	Base     SnapshotRef `json:"base,omitempty"`
	BaseHash string      `json:"base_hash,omitempty"`
	// Items are what a foreach iterates over, fixed at entry.
	Items []Item `json:"items,omitempty"`
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
	// Deviation lists what an agent that cannot write changed; the run
	// waits on a deviation gate before following the outcome.
	Deviation     []string `json:"deviation,omitempty"`
	DeviationHash string   `json:"deviation_hash,omitempty"`
	// Commit is the hash a commit occurrence made.
	Commit string `json:"commit,omitempty"`
	// ApprovedCommit is the latest commit when an approval of the diff was
	// given: what publish is allowed to land.
	ApprovedCommit string `json:"approved_commit,omitempty"`
}

// devGate is one deviation gate and, once decided, its decision.
type devGate struct {
	N        int         `json:"n"`
	Request  GateRequest `json:"request"`
	Files    []string    `json:"files"`
	Decision *Decision   `json:"-"`
}

type frame struct {
	ID       string             `json:"id"`
	Workflow string             `json:"workflow"`
	Inputs   map[string]DataRef `json:"inputs,omitempty"`
	// Feedback is what was sent back to the calling node; the frame's first
	// node gets it, since the calling node is not an agent that could read
	// it.
	Feedback string `json:"feedback,omitempty"`
	// Parent is the frame this one was called from ("" for root).
	Parent string `json:"parent,omitempty"`
	// A foreach iteration remembers what it iterates and which item it is.
	Over    string  `json:"over,omitempty"`
	Item    string  `json:"item,omitempty"`
	ItemRef DataRef `json:"item_ref,omitempty"`
	// Tree is the worktree when a findings iteration began (fix-diff's
	// starting point).
	Tree SnapshotRef `json:"tree,omitempty"`
}

// records is every occurrence and result of a run, loaded once per move.
type records struct {
	occs    []*occurrence
	byFrame map[string][]*occurrence
	results map[string]*result
	// latest is, per data name, the occurrence that last wrote it.
	latest map[string]string
	maxID  int
	// devs are the deviation gates per opening occurrence, in order.
	devs map[string][]*devGate
	// concerns are every reported concern, in key order. triaged are the
	// occurrences a decided triage gate interrupted; reenter are those of
	// them the run enters again, with the feedback for the new entry.
	concerns []*concern
	triaged  map[string]bool
	reenter  map[string]string
}

func runKey(run RunID, k string) string { return string(run) + "/" + k }

func (e *Engine) loadRecords(run RunID) (*records, error) {
	r := &records{byFrame: map[string][]*occurrence{}, results: map[string]*result{}, latest: map[string]string{}, devs: map[string][]*devGate{},
		triaged: map[string]bool{}, reenter: map[string]string{}}
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
	if err := e.loadDeviations(run, r); err != nil {
		return nil, err
	}
	if err := e.loadConcerns(run, r); err != nil {
		return nil, err
	}
	return r, nil
}

func (e *Engine) loadDeviations(run RunID, r *records) error {
	kvs, err := e.store.List(runKey(run, prefDevGate))
	if err != nil {
		return err
	}
	byKey := map[string]*devGate{}
	for _, kv := range kvs {
		var g devGate
		if err := json.Unmarshal(kv.Value, &g); err != nil {
			return fmt.Errorf("%s: %w", kv.Key, err)
		}
		occ := g.Request.Occurrence
		r.devs[occ] = append(r.devs[occ], &g)
		byKey[devKey(occ, g.N)] = &g
	}
	for _, gs := range r.devs {
		sort.Slice(gs, func(i, j int) bool { return gs[i].N < gs[j].N })
	}
	kvs, err = e.store.List(runKey(run, prefDevDec))
	if err != nil {
		return err
	}
	for _, kv := range kvs {
		k := kv.Key[len(runKey(run, prefDevDec)):]
		g := byKey[k]
		if g == nil {
			continue
		}
		var d Decision
		if err := json.Unmarshal(kv.Value, &d); err != nil {
			return fmt.Errorf("%s: %w", kv.Key, err)
		}
		g.Decision = &d
	}
	return nil
}

func devKey(occ string, n int) string { return fmt.Sprintf("%s/%03d", occ, n) }

// decided reports whether a human decided anything on occurrence o: an
// approval node's result, a deviation gate it opened, or a triage gate that
// interrupted it.
func (r *records) decided(o *occurrence, n *Node) bool {
	if n != nil && n.Type == NodeApproval && r.results[o.ID] != nil {
		return true
	}
	if r.triaged[o.ID] {
		return true
	}
	for _, g := range r.devs[o.ID] {
		if g.Decision != nil {
			return true
		}
	}
	return false
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
