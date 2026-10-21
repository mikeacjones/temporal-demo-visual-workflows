package flow

import (
	"encoding/json"
	"time"
)

// RunInput starts one execution of a flow.
type RunInput struct {
	Flow  Definition `json:"flow"`
	Input any        `json:"input"`
	// Source records what started the run: manual, webhook, or schedule.
	Source string `json:"source"`
	// CallbackBase is the flow's hook URL. The run's own event URL is
	// <CallbackBase>/runs/<workflow ID>/events/<signal name>; a POST there
	// becomes that Signal on the run.
	CallbackBase string `json:"callbackBase"`
	// Subflows are snapshots of the flows this one runs as children (Run
	// flow, For each), resolved when the run starts.
	Subflows map[string]Definition `json:"subflows,omitempty"`
	// Key identifies a keyed (long-lived) run.
	Key string `json:"key,omitempty"`
	// Resume carries a run's state across Continue-As-New: the new run picks
	// up where the last one left off.
	Resume *ResumeState `json:"resume,omitempty"`
	// SharedLimits names the rate limits shared by every run that the
	// deployment had when this run started; a step can only use those (a
	// request on any other limit's task queue would wait for ever).
	SharedLimits []string `json:"sharedLimits,omitempty"`
}

// ResumeState is what a run carries into its next run when it continues as
// new.
type ResumeState struct {
	Vars       map[string]any        `json:"vars"`
	Events     map[string]EventState `json:"events"`
	Steps      map[string]any        `json:"steps"`
	Generation int                   `json:"generation"`
	// Nodes is every step's state, so the run view carries on seamlessly.
	Nodes map[string]NodeState `json:"nodes,omitempty"`
	// Interpreter is the interpreter's own progress: where the main path
	// is, unconsumed events, the undo stack, paused waits. Opaque here; the
	// Workflow package owns its shape. A resume without it comes from a
	// keyed run whose main path had already finished.
	Interpreter json.RawMessage `json:"interpreter,omitempty"`
}

// RunState is what the interpreter Workflow reports through its "state"
// Query. The builder overlays it on the canvas.
type RunState struct {
	FlowID      string               `json:"flowId"`
	FlowName    string               `json:"flowName"`
	FlowVersion int                  `json:"flowVersion"`
	Source      string               `json:"source"`
	Status      RunStatus            `json:"status"`
	Error       string               `json:"error,omitempty"`
	StartedAt   time.Time            `json:"startedAt"`
	EndedAt     *time.Time           `json:"endedAt,omitempty"`
	Nodes       map[string]NodeState `json:"nodes"`
	// CallbackURL is where outside systems send events to this run:
	// POST <CallbackURL>/<signal name>.
	CallbackURL string `json:"callbackUrl"`
	// Events records the Signals received so far, by name.
	Events map[string]EventState `json:"events"`
	// Vars is the run's current variables.
	Vars map[string]any `json:"vars"`
	// Compensation is "", "running", "done", or "failed": undo steps after
	// a failure.
	Compensation string `json:"compensation,omitempty"`
	// Key and Generation describe a keyed run; Generation counts
	// Continue-As-New.
	Key        string `json:"key,omitempty"`
	Generation int    `json:"generation,omitempty"`
	// Flow is the snapshot this run executes, so the run view can draw the
	// graph as it was, even after the flow has been edited.
	Flow Definition `json:"flow"`
	// Items holds, per For each that runs steps, the most recent items and
	// each one's own step states. The body's steps in Nodes summarise all
	// items.
	Items map[string][]*ItemState `json:"items,omitempty"`
}

// ItemState is one item of a For each that runs steps.
type ItemState struct {
	Index int `json:"index"`
	// Item is the item itself, when it is small.
	Item      json.RawMessage `json:"item,omitempty"`
	Status    NodeStatus      `json:"status"`
	Error     string          `json:"error,omitempty"`
	StartedAt *time.Time      `json:"startedAt,omitempty"`
	EndedAt   *time.Time      `json:"endedAt,omitempty"`
	// Nodes is the item's state of each body step.
	Nodes map[string]NodeState `json:"nodes"`
}

// ItemSeparator joins a step ID and an item index in the Activity ID of a
// step running for one item of a For each: "fetch#3".
const ItemSeparator = "#"

// EventState is what the run knows about one Signal name. Conditions read
// the latest payload as events.<name>.
type EventState struct {
	Count  int             `json:"count"`
	Last   json.RawMessage `json:"last,omitempty"`
	LastAt *time.Time      `json:"lastAt,omitempty"`
	// Pending counts received events no Wait-for-signal step has consumed.
	Pending int `json:"pending"`
}

type RunStatus string

const (
	RunRunning   RunStatus = "running"
	RunCompleted RunStatus = "completed"
	RunFailed    RunStatus = "failed"
)

type NodeStatus string

const (
	NodePending   NodeStatus = "pending"
	NodeRunning   NodeStatus = "running"
	NodeWaiting   NodeStatus = "waiting"
	NodeCompleted NodeStatus = "completed"
	NodeFailed    NodeStatus = "failed"
	NodeSkipped   NodeStatus = "skipped"
)

type NodeState struct {
	Status    NodeStatus      `json:"status"`
	StartedAt *time.Time      `json:"startedAt,omitempty"`
	EndedAt   *time.Time      `json:"endedAt,omitempty"`
	Output    json.RawMessage `json:"output,omitempty"`
	Error     string          `json:"error,omitempty"`
	// Handle is the outcome the node routed to (out, true, approved, ...).
	Handle  string   `json:"handle,omitempty"`
	Waiting *Waiting `json:"waiting,omitempty"`
	// ChildWorkflowID is set on agent and Run flow nodes, which run as a
	// child Workflow.
	ChildWorkflowID string `json:"childWorkflowId,omitempty"`
	// Invocations counts how many times a lane's steps have run.
	Invocations int `json:"invocations,omitempty"`
	// Undo is the compensation status of a step with an undo request:
	// "running", "done", or "failed".
	Undo string `json:"undo,omitempty"`
	// Progress is a short note such as "3 of 10 items" for For each.
	Progress string `json:"progress,omitempty"`
}

// Waiting explains what a waiting node needs before it can continue.
type Waiting struct {
	// Kind is "signal", "collect", "condition", "approval", "timer", "fix",
	// or "children".
	Kind       string     `json:"kind"`
	SignalName string     `json:"signalName,omitempty"`
	Title      string     `json:"title,omitempty"`
	Details    string     `json:"details,omitempty"`
	Until      *time.Time `json:"until,omitempty"`
	// Request is the request a step paused for a fix would send, so a
	// person can correct it.
	Request *HTTPRequestSpec `json:"request,omitempty"`
}

// Message names used by the API and the Workflows.
const (
	QueryState    = "state"
	QueryVars     = "vars"
	UpdateApprove = "approve"
	UpdateAnswer  = "answer"
	// UpdateFix resumes a step paused after its retries ran out.
	UpdateFix = "fix_step"
	// UpdateEarlyResponse returns the main path's Respond value to whoever
	// started the run (sent with Update-With-Start).
	UpdateEarlyResponse = "early_response"
	QueryAgent          = "agent"
	// MemoProgress is the memo key holding the run's Progress.
	MemoProgress = "progress"
)

// Progress is a compact copy of RunState that the Workflow keeps in its memo.
// Describe returns it without a worker, so watching a run never wakes one
// (which matters on serverless workers). Outputs are left out to stay well
// under the 40 KB memo limit; they are fetched on demand.
type Progress struct {
	Status       RunStatus               `json:"status"`
	Error        string                  `json:"error,omitempty"`
	Nodes        map[string]NodeProgress `json:"nodes"`
	Events       map[string]EventState   `json:"events,omitempty"`
	Vars         map[string]any          `json:"vars,omitempty"`
	Compensation string                  `json:"compensation,omitempty"`
	Generation   int                     `json:"generation,omitempty"`
}

// NodeProgress uses short JSON names to keep the memo small.
type NodeProgress struct {
	Status      NodeStatus `json:"s"`
	Handle      string     `json:"h,omitempty"`
	Error       string     `json:"e,omitempty"`
	Waiting     *Waiting   `json:"w,omitempty"`
	StartedAt   *time.Time `json:"t0,omitempty"`
	EndedAt     *time.Time `json:"t1,omitempty"`
	Invocations int        `json:"i,omitempty"`
	Undo        string     `json:"u,omitempty"`
	Progress    string     `json:"p,omitempty"`
	Child       string     `json:"c,omitempty"`
}

// FixDecision is the argument of the "fix_step" Update. Action is "retry"
// (with optional corrections) or "fail".
type FixDecision struct {
	NodeID string `json:"nodeId"`
	// Item is the item, for a step in a For each body.
	Item    *int              `json:"item,omitempty"`
	Action  string            `json:"action"`
	URL     string            `json:"url,omitempty"`
	Body    *string           `json:"body,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	By      string            `json:"by,omitempty"`
}

// ApprovalDecision is the argument of the "approve" Update.
type ApprovalDecision struct {
	NodeID string `json:"nodeId"`
	// Item is the item, for a step in a For each body.
	Item     *int   `json:"item,omitempty"`
	Approved bool   `json:"approved"`
	Comment  string `json:"comment,omitempty"`
	By       string `json:"by,omitempty"`
}

// AgentState is what the agent child Workflow reports through its "agent"
// Query: the ReAct trace and any question waiting for a person.
type AgentState struct {
	Status          string       `json:"status"` // thinking, acting, waiting, done, failed
	Turn            int          `json:"turn"`
	MaxTurns        int          `json:"maxTurns"`
	Trace           []TraceEntry `json:"trace"`
	PendingQuestion string       `json:"pendingQuestion,omitempty"`
	Answer          string       `json:"answer,omitempty"`
}

type TraceEntry struct {
	// Kind is "reasoning", "tool_call", "tool_result", "question", "answer",
	// or "final".
	Kind    string          `json:"kind"`
	Text    string          `json:"text,omitempty"`
	Tool    string          `json:"tool,omitempty"`
	Input   json.RawMessage `json:"input,omitempty"`
	IsError bool            `json:"isError,omitempty"`
	At      time.Time       `json:"at"`
}
