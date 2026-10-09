// Package engine is masuda's workflow engine: it loads workflow and agent
// definitions, checks them, and decides what happens next from a durable
// record of what happened so far.
//
// This file is the engine API contract (see docs/design/contracts.md in the
// masuda repository). Everything the engine does outside its own records goes
// through Runner; everything it remembers goes through Store. The engine knows
// nothing about VMs, git, or files on disk.
//
// Implementers fill in the bodies of the functions declared here (and add
// unexported files freely) but do not change exported names, signatures or
// field sets. Proposals go to HANDOFF.md.
package engine

import (
	"context"
	"errors"
	"io/fs"
	"time"
)

// ErrNotImplemented is returned by the stubs in this file until implemented.
var ErrNotImplemented = errors.New("engine: not implemented")

// ---------------------------------------------------------------------------
// Definitions
// ---------------------------------------------------------------------------

// NodeType is a node's kind. The set is fixed by the engine; definitions pick
// from it and cannot add to it.
type NodeType string

const (
	NodeAgent    NodeType = "agent"
	NodeExec     NodeType = "exec"
	NodeApproval NodeType = "approval"
	NodeQuestion NodeType = "question"
	NodeForeach  NodeType = "foreach"
	NodeWorkflow NodeType = "workflow"
	NodeCommit   NodeType = "commit"
	NodePublish  NodeType = "publish"
	NodeDiscard  NodeType = "discard"

	// NodePrivileged runs one of the host's declared privileged commands by
	// name. The engine knows the name only; what runs, where, and what it may
	// reach are the host's declaration and approval.
	NodePrivileged NodeType = "privileged"
)

// Outcomes the engine reserves. Definitions can neither declare them as agent
// outcomes nor use them as end labels, except where noted.
const (
	OutcomeDone       = "done"
	OutcomeFailed     = "failed"     // exec, privileged: non-zero exit or timeout
	OutcomeExhausted  = "exhausted"  // entry limit reached
	OutcomeBlocked    = "blocked"    // engine stopped the run
	OutcomeApproved   = "approved"   // approval
	OutcomeRejected   = "rejected"   // approval, commit (deviation refused)
	OutcomeAnswered   = "answered"   // question
	OutcomeIncomplete = "incomplete" // foreach with on_incomplete: continue
)

// Gates the engine opens on its own. Definitions cannot name a gate after
// them.
const (
	GateTriage    = "triage"
	GateDeviation = "deviation"
)

// DefaultMax is the entry limit a node gets when it declares no `max`.
const DefaultMax = 3

// Fuse is the number of occurrences after which a run is considered runaway
// and is blocked. Not configurable.
const Fuse = 20000

// Target is where a transition goes: another node, or the end of the
// workflow (`end` = outcome done, `end:<label>` = that label).
type Target struct {
	Node  string
	End   bool
	Label string
}

// Question is one item a question node asks when it carries fixed questions
// (no role).
type Question struct {
	ID      string
	Text    string
	Options []string // empty = free text
}

// Node is one node of a workflow. Only the fields that belong to its Type are
// set; Load rejects others.
type Node struct {
	ID   string
	Type NodeType
	Next map[string]Target
	Max  int // 0 = DefaultMax for agent/exec/privileged, unlimited otherwise

	// agent / question(with role)
	Role string
	// agent: このタスクを、その役（agents/<name>）が最後に担当した出現の
	// サブエージェントに続けて渡す。空なら無し。
	Continues string
	// exec
	Command []string
	Timeout time.Duration
	// privileged: the name of the privileged command's declaration
	PrivilegedName string
	// agent / exec: data this node reads beyond the workflow's inputs, and
	// data it writes.
	Inputs  []string
	Outputs []string
	// agent / exec: policy while the node runs.
	Egress  []string
	Secrets []string
	// approval
	Gate   string
	Target string // "plan", "diff", or a data name
	// question (fixed)
	Questions []Question
	// foreach
	Over         string // "steps", "findings", "perspectives", "perspectives(from=<node>)", or "<data>[]"
	Body         string
	OnIncomplete string // "stop" (default) | "continue"
	// foreach / workflow
	With     map[string]string
	Workflow string
	// commit
	Scope string // "step" | "plan"
	// publish
	PublishTarget string // "local" (default) | "remote"
	// publish / discard
	Export []string
}

// Workflow is one parsed workflow file.
type Workflow struct {
	Path    string // reference path, e.g. "workflows/develop"
	Version int
	Inputs  []string
	Start   string
	Nodes   map[string]*Node
	Order   []string // file order, for stable diagnostics
	// UserInvocable is false for a workflow meant to be called by other
	// workflows (or kept for checks), not started by a user. Hosts leave such
	// workflows out of their lists; starting one is still allowed.
	UserInvocable bool
}

// Agent is one parsed agent definition.
type Agent struct {
	Name        string
	Description string
	Tools       []string // nil = all tools
	Inputs      []string
	Outputs     []string
	Outcomes    map[string]string // outcome -> description; must contain done
	Model       string            // "" = the Runner's default; passed through uninterpreted
	Effort      string            // "" = the Runner's default; low, medium, high, xhigh or max
	Body        string            // the prompt
}

// WriteCapable reports whether the agent can change the worktree: it has
// Write or Edit, or declares no tools at all. Decided from Tools, never from
// the definition's own claims.
func (a *Agent) WriteCapable() bool {
	if a.Tools == nil {
		return true
	}
	for _, t := range a.Tools {
		if t == "Write" || t == "Edit" {
			return true
		}
	}
	return false
}

// Origin says where a definition file came from.
type Origin string

const (
	OriginBundled Origin = "bundled"
	OriginRepo    Origin = "repo"
)

// Set is every definition a run can use, resolved and checked.
type Set struct {
	Workflows map[string]*Workflow
	Agents    map[string]*Agent
	Schemas   map[string][]byte // data name -> JSON Schema
	Origins   map[string]Origin // reference path -> origin
}

// Load reads definitions. repo is the target repository's `.masuda/`
// directory (may be nil); bundled is the engine's own defaults. A file at the
// same path in repo replaces the bundled one whole. Each file's shape is
// checked here; rules spanning files are checked by Set.Check.
func Load(repo fs.FS, bundled fs.FS) (*Set, error) { return load(repo, bundled) }

// Bundled returns the definitions embedded in this module.
func Bundled() fs.FS { return bundledFS() }

// Problem is one thing wrong with a definition set.
type Problem struct {
	Path    string // reference path of the file
	Node    string // node id, if any
	Message string
}

// Check validates the set as a whole for running root. checks are the names
// available to exec nodes' environment (settings.json `checks`); kept for
// parity with the previous engine and may be empty. Returns all problems
// found, nil when the set is runnable.
func (s *Set) Check(root string) []Problem { return s.check(root) }

// Reachable lists the reference paths root transitively uses, for
// snapshotting.
func (s *Set) Reachable(root string) ([]string, error) { return s.reachable(root) }

// Mermaid renders root with the engine's own interrupts (triage, deviation)
// drawn in, so what is shown is what runs.
func (s *Set) Mermaid(root string) (string, error) { return s.mermaid(root) }

// ---------------------------------------------------------------------------
// Runtime: what the engine asks of its host
// ---------------------------------------------------------------------------

// RunID identifies one execution of a root workflow (masuda uses the
// workspace id).
type RunID string

// DataRef names one value of a data item: the output `Name` written by
// occurrence `Occurrence`, or an input placed at run start (Occurrence "").
type DataRef struct {
	Name       string
	Occurrence string
}

// Policy is what a node is allowed to reach while it runs.
type Policy struct {
	Egress  []string
	Secrets []string
}

// AgentTask is one unit of work for the guest's main agent.
type AgentTask struct {
	Run        RunID
	Occurrence string
	Workflow   string
	Node       string
	Agent      *Agent
	Inputs     map[string]DataRef // input name -> value to materialize
	Outputs    []string           // names the agent must write on done (and may write otherwise)
	Feedback   string             // from the previous node/gate, if any
	Policy     Policy
	// Continues は、このタスクを続けて渡す宛先の出現ID。ノードが`continues`
	// を書いていれば、その役を担当したagentノードの出現のうち、終了済みで
	// 出現IDが最大のもの（フレームを問わない）。書いていないか該当が無ければ
	// 空。続きが成立したか（VMの再開後でサブエージェントがいない等）は
	// Runner／ゲストの事情で、エンジンは関知しない。Inputsは新しい
	// サブエージェントが単独でこなせる完全なものなので、どちらでも成立する。
	Continues string
}

// CommandTask is one exec node run.
type CommandTask struct {
	Run        RunID
	Occurrence string
	Node       string
	Command    []string
	Inputs     map[string]DataRef
	Outputs    []string
	Timeout    time.Duration
	Policy     Policy
}

// PrivilegedTask is one privileged node run: the host runs the command it
// declared under Name.
type PrivilegedTask struct {
	Run        RunID
	Occurrence string
	Node       string
	Name       string
}

// CommandResult is what came back from an exec or privileged node. A
// privileged node declares no outputs, so its Outputs are not read.
type CommandResult struct {
	ExitCode int
	TimedOut bool
	LogTail  string            // last part of stdout+stderr, for feedback
	Outputs  map[string][]byte // name -> content found in the output dir
}

// SnapshotRef points at a WIP snapshot of the worktree in staging.
type SnapshotRef string

// DiffKind selects what Runner.Diff computes.
type DiffKind string

const (
	DiffFromBase DiffKind = "diff"      // base..worktree
	DiffFromHead DiffKind = "step-diff" // HEAD..worktree
	DiffFromRef  DiffKind = "fix-diff"  // a SnapshotRef..worktree
	// DiffCommitted is base..branch head: exactly what publish will land.
	// Approval nodes with target "diff" decide on this, not on the worktree.
	DiffCommitted DiffKind = "committed-diff"
)

// GateRequest opens a human decision.
type GateRequest struct {
	Run        RunID
	Occurrence string
	Gate       string
	Target     string // "plan", "diff", data name, or "" for triage/deviation
	TargetHash string // hash of what is being decided on
	Subject    []byte // what to show: deviation file list, concern text, ...
}

// Decision is a human's answer to a gate.
type Decision struct {
	Outcome    string // approved | rejected | dismiss | halt | redo
	Comment    string
	TargetHash string // must match GateRequest.TargetHash for approvals
	// Files approved as additions to the plan (deviation gate only). Files
	// the gate listed but this does not name are NOT added: the commit
	// proceeds without them and they stay uncommitted in the worktree (the
	// engine passes them as Byproducts for that commit). An empty list is
	// therefore "approve the commit, add nothing", never "approve all".
	ApprovedFiles []string
}

// QuestionRequest opens a question to a human.
type QuestionRequest struct {
	Run        RunID
	Occurrence string
	Questions  []Question
}

// Answer is a human's reply.
type Answer struct {
	Answers map[string]string // question id -> answer
}

// CommitRequest asks the host to commit the plan's files.
type CommitRequest struct {
	Run        RunID
	Occurrence string
	Scope      string   // "step" | "plan"
	Step       string   // step key when Scope is "step"
	Allowed    []string // paths the commit may include (plan files + approved deviations)
	// Byproducts are paths neither committed nor counted as deviations: exact
	// paths or doublestar globs ("*" stays within one directory, "**" spans
	// zero or more). Hosts must match them the same way.
	Byproducts []string
	Message    string
}

// CommitResult says what the commit did.
type CommitResult struct {
	// Non-empty when files outside Allowed changed; nothing was committed.
	Deviations     []string
	DeviationsHash string
	Commit         string // hash, when committed
}

// PublishRequest finishes a run by landing its branch.
type PublishRequest struct {
	Run    RunID
	Target string // "local" | "remote"
	Commit string // the exact hash approved by the review gate
	Export []string
}

// Event is one line of the execution record. Kinds: enter, finish, end,
// gate-open, decision, question-open, answer, concern, triage, invalid,
// blocked, snapshot, policy.
type Event struct {
	Time       time.Time `json:"time"`
	Kind       string    `json:"kind"`
	Run        RunID     `json:"run"`
	Occurrence string    `json:"occurrence,omitempty"`
	Workflow   string    `json:"workflow,omitempty"`
	Node       string    `json:"node,omitempty"`
	Outcome    string    `json:"outcome,omitempty"`
	Detail     string    `json:"detail,omitempty"`
}

// Runner is everything the engine does outside its own records. masuda
// supplies the real one; tests supply stubs.
type Runner interface {
	// SetPolicy switches the sandbox's egress/secret policy for the node about
	// to run. Called before every agent/exec task.
	SetPolicy(ctx context.Context, run RunID, p Policy) error

	// RunCommand executes an exec node synchronously.
	RunCommand(ctx context.Context, t CommandTask) (CommandResult, error)

	// RunPrivileged executes a privileged node synchronously. A command that
	// ran and exited non-zero (or timed out) is a result, not an error; an
	// error means it could not be run at all, including a name that is not
	// declared or not approved. The engine calls neither SetPolicy nor
	// Snapshot for it: the host's declaration decides the policy, and the
	// command does not change the worktree.
	RunPrivileged(ctx context.Context, t PrivilegedTask) (CommandResult, error)

	// ReadOutput returns the content an agent occurrence wrote for name, or
	// ok=false if it wrote nothing.
	ReadOutput(ctx context.Context, run RunID, occurrence, name string) (content []byte, ok bool, err error)

	// PutData stores a validated data value; GetData reads one back.
	PutData(ctx context.Context, run RunID, ref DataRef, content []byte) error
	GetData(ctx context.Context, run RunID, ref DataRef) ([]byte, error)

	// Snapshot records the worktree as a WIP snapshot in staging.
	Snapshot(ctx context.Context, run RunID, occurrence string) (SnapshotRef, error)
	// Diff computes a diff data value and stores it under ref.
	Diff(ctx context.Context, run RunID, kind DiffKind, from SnapshotRef, into DataRef) error
	// ChangedSince lists worktree paths that differ from a snapshot, with a
	// hash identifying that exact set of changes. An empty from means the
	// branch head (HEAD): "everything not yet committed", which is what the
	// commit node's deviation check and the review gate's unpublished list
	// use.
	ChangedSince(ctx context.Context, run RunID, from SnapshotRef) (files []string, hash string, err error)

	// Items lists what a foreach iterates over, resolved from data.
	Items(ctx context.Context, run RunID, over string, from DataRef) ([]Item, error)

	// OpenGate / OpenQuestion announce a human decision is wanted. They return
	// immediately; the answer arrives through Engine.Decide / Engine.Answer.
	OpenGate(ctx context.Context, g GateRequest) error
	OpenQuestion(ctx context.Context, q QuestionRequest) error

	Commit(ctx context.Context, c CommitRequest) (CommitResult, error)
	Publish(ctx context.Context, p PublishRequest) error
	Discard(ctx context.Context, run RunID, export []string) error

	// Log appends to the execution record.
	Log(e Event)
}

// Item is one element of a foreach.
type Item struct {
	Key     string // stable key (step number, perspective name, finding id)
	Input   string // input name the body receives it as ("step", "perspective", "finding", or data name)
	Content []byte
	// Done means the item is already complete (e.g. a committed step) and is
	// skipped.
	Done bool
}

// Op is one operation of a Store.Apply.
type Op struct {
	Kind  OpKind
	Key   string
	Value []byte // put: new value; check: expected current value (nil = absent)
}

// OpKind is an Op's kind.
type OpKind string

const (
	OpCheck  OpKind = "check"
	OpPut    OpKind = "put"
	OpDelete OpKind = "delete"
)

// KV is one stored pair.
type KV struct {
	Key   string
	Value []byte
}

// Store is the engine's durable memory. Keys are scoped by the engine under
// "<run>/...". Apply is atomic: if any check fails nothing is written and
// applied is false.
type Store interface {
	Get(key string) (value []byte, ok bool, err error)
	Put(key string, value []byte) error
	Delete(key string) error
	List(prefix string) ([]KV, error)
	Apply(ops []Op) (applied bool, err error)
}

// ---------------------------------------------------------------------------
// Runtime: the engine itself
// ---------------------------------------------------------------------------

// StatusKind says why Advance returned.
type StatusKind string

const (
	StatusAgent    StatusKind = "agent"    // waiting for the guest to do Task
	StatusGate     StatusKind = "gate"     // waiting for a human decision
	StatusQuestion StatusKind = "question" // waiting for a human answer
	StatusDone     StatusKind = "done"     // root workflow finished
	StatusBlocked  StatusKind = "blocked"  // stopped; see Reason
	// StatusPending: a result, decision or answer has been recorded and the
	// run has not been advanced since. Only Status returns it; Advance never
	// does (it moves the run instead).
	StatusPending StatusKind = "pending"
)

// Status is what the engine is waiting for after Advance.
type Status struct {
	Kind       StatusKind
	Occurrence string
	Task       *AgentTask       // Kind == StatusAgent
	Gate       *GateRequest     // Kind == StatusGate
	Question   *QuestionRequest // Kind == StatusQuestion
	Outcome    string           // Kind == StatusDone
	Reason     string           // Kind == StatusBlocked
}

// Engine runs workflows. It holds no position in memory: every Advance
// recomputes where the run is from the Store.
type Engine struct {
	set    *Set
	store  Store
	runner Runner
	now    func() time.Time
}

// Options tunes an Engine.
type Options struct {
	Now func() time.Time // for tests; nil = time.Now
}

// New makes an engine over a checked Set.
func New(set *Set, store Store, runner Runner, opts Options) *Engine {
	return newEngine(set, store, runner, opts)
}

// Start begins run with the root workflow and its inputs (already stored via
// Runner.PutData with Occurrence ""). It records nothing but the start; call
// Advance to move.
func (e *Engine) Start(ctx context.Context, run RunID, root string, inputs []string) error {
	return e.start(run, root, inputs)
}

// Advance moves the run as far as it can without a human or an agent, runs
// exec/privileged/commit/publish/discard nodes on the way, and returns what it
// is now waiting for. Idempotent: calling it again without new information
// returns the same Status.
func (e *Engine) Advance(ctx context.Context, run RunID) (Status, error) {
	return e.walk(ctx, run, true)
}

// ReportResult records how an agent occurrence ended. The engine verifies the
// outcome is one the agent declared and reads the declared outputs (via
// Runner.ReadOutput): on done every one must be written, on any other outcome
// only those written are taken. Whatever was written must validate against
// schemas and is stored; failing that it re-enters the node with the reason
// as feedback.
func (e *Engine) ReportResult(ctx context.Context, run RunID, occurrence, outcome, feedback string) error {
	return e.reportResult(ctx, run, occurrence, outcome, feedback)
}

// Decide records a human's decision on the gate opened by occurrence. An
// approval whose TargetHash does not match the open gate's is refused.
func (e *Engine) Decide(ctx context.Context, run RunID, occurrence string, d Decision) error {
	return e.decide(ctx, run, occurrence, d)
}

// Answer records a human's answers to the question opened by occurrence.
func (e *Engine) Answer(ctx context.Context, run RunID, occurrence string, a Answer) error {
	return e.answer(ctx, run, occurrence, a)
}

// ReportConcern records a security concern raised by the guest. The next
// Advance opens the triage gate before anything else.
func (e *Engine) ReportConcern(ctx context.Context, run RunID, occurrence, text string) error {
	return e.reportConcern(ctx, run, occurrence, text)
}

// Status returns the current status without moving the run.
func (e *Engine) Status(ctx context.Context, run RunID) (Status, error) {
	return e.status(ctx, run)
}
