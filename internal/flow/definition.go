// Package flow defines the document the visual builder saves and the
// interpreter Workflow executes. It has no Temporal imports: the API validates
// definitions with it, and the Workflow reads them with it.
package flow

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Definition is one saved flow. Each run receives a snapshot of it as Workflow
// input, so editing a flow never changes executions that are already running.
type Definition struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	Version     int          `json:"version"`
	UpdatedAt   time.Time    `json:"updatedAt"`
	Settings    FlowSettings `json:"settings"`
	Nodes       []Node       `json:"nodes"`
	Edges       []Edge       `json:"edges"`
}

// FlowSettings apply to the whole flow.
type FlowSettings struct {
	// Direction is how the canvas lays the flow out: "LR" or "TB".
	Direction string        `json:"direction,omitempty"`
	Variables []VariableDef `json:"variables,omitempty"`
	// Activity holds the defaults every step that calls out inherits.
	Activity ActivityOptions `json:"activity"`
	// KeyedBy gives the flow one run per key, where the key is this
	// expression over the start input (for example `input.customerId`).
	// Events for a key reach its run while it is running; once it has
	// finished, the key's next event starts a fresh run.
	KeyedBy string `json:"keyedBy,omitempty"`
	// Entity keeps the run alive after its main path: it serves events until
	// an End run step, or for ever if the flow has none. Keyed or not: a key
	// only decides the Workflow ID, so senders can address it by key.
	// Without it, a run completes when its main path does, like any
	// Workflow; to hold one open for events, end the main path with a wait.
	Entity bool `json:"entity"`
	// Priority orders this flow's tasks when a task queue is backed up: 1
	// runs first, 5 last, 0 is the default (3). Activities and child
	// Workflows inherit it.
	Priority int `json:"priority,omitempty"`
	// IDConflictPolicy and IDReusePolicy say what starting a keyed flow does
	// when its key's Workflow ID is already taken: by a running run
	// (conflict) or by a closed one (reuse). Unkeyed runs get a fresh
	// Workflow ID every start, so neither applies to them.
	IDConflictPolicy string `json:"idConflictPolicy,omitempty"`
	IDReusePolicy    string `json:"idReusePolicy,omitempty"`
}

// Workflow ID conflict policies: what a start does when a run with the same
// Workflow ID is running.
const (
	// ConflictUseExisting reaches the running run instead of starting one
	// (the default for keyed flows).
	ConflictUseExisting = "use_existing"
	// ConflictFail rejects the start. Signal-With-Start can't fail this way;
	// it delivers to the running run, as with use_existing.
	ConflictFail = "fail"
	// ConflictTerminateExisting terminates the running run and starts afresh.
	ConflictTerminateExisting = "terminate_existing"
)

// Workflow ID reuse policies: whether a start may reuse the Workflow ID of a
// run that has closed.
const (
	// ReuseAllowDuplicate always allows a new run (the default).
	ReuseAllowDuplicate = "allow_duplicate"
	// ReuseAllowDuplicateFailedOnly allows one only if the last run did not
	// complete successfully: failed, cancelled, terminated or timed out.
	ReuseAllowDuplicateFailedOnly = "allow_duplicate_failed_only"
	// ReuseRejectDuplicate never reuses the ID while the closed run is
	// retained: one run per key, ever.
	ReuseRejectDuplicate = "reject_duplicate"
)

// ConflictPolicy returns the flow's conflict policy, defaulted.
func (s FlowSettings) ConflictPolicy() string {
	if s.IDConflictPolicy == "" {
		return ConflictUseExisting
	}
	return s.IDConflictPolicy
}

// ReusePolicy returns the flow's reuse policy, defaulted.
func (s FlowSettings) ReusePolicy() string {
	if s.IDReusePolicy == "" {
		return ReuseAllowDuplicate
	}
	return s.IDReusePolicy
}

// UnmarshalJSON reads flows saved before Entity existed, when every keyed
// flow without an idle timeout was an entity, so they keep behaving as they
// did. Flows saved since always carry the field.
func (s *FlowSettings) UnmarshalJSON(raw []byte) error {
	type plain FlowSettings
	var v struct {
		plain
		Entity             *bool `json:"entity"`
		IdleTimeoutSeconds int   `json:"idleTimeoutSeconds"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	*s = FlowSettings(v.plain)
	if v.Entity != nil {
		s.Entity = *v.Entity
	} else {
		s.Entity = s.KeyedBy != "" && v.IdleTimeoutSeconds == 0
	}
	return nil
}

// VariableDef declares run state that steps can read (vars.<name>) and Set
// variable steps can change.
type VariableDef struct {
	Name string `json:"name"`
	// Initial is the starting value as JSON text.
	Initial string `json:"initial,omitempty"`
	// Visible variables can be read from outside the run (a Query) without
	// adding anything to its history.
	Visible bool `json:"visible,omitempty"`
}

// ActivityOptions configure Activities. Zero fields inherit: a step inherits
// the flow's defaults, and the flow inherits the system defaults.
type ActivityOptions struct {
	// StartToCloseSeconds bounds one attempt. Capped at 600 so every attempt
	// fits inside a serverless worker's invocation.
	StartToCloseSeconds int `json:"startToCloseSeconds,omitempty"`
	// ScheduleToCloseSeconds bounds all attempts together.
	ScheduleToCloseSeconds int         `json:"scheduleToCloseSeconds,omitempty"`
	Retry                  RetryConfig `json:"retry"`
	// SlowRetry, when set, takes over once Retry's attempts are used up: a
	// few quick retries for a blip, then slow ones (often unlimited) to
	// outlast a long outage.
	SlowRetry *RetryConfig `json:"slowRetry,omitempty"`
}

type RetryConfig struct {
	MaxAttempts            int     `json:"maxAttempts,omitempty"`
	InitialIntervalSeconds float64 `json:"initialIntervalSeconds,omitempty"`
	BackoffCoefficient     float64 `json:"backoffCoefficient,omitempty"`
	MaximumIntervalSeconds float64 `json:"maximumIntervalSeconds,omitempty"`
	// NonRetryableErrorTypes are error types that end retries immediately,
	// for example "HTTPClientError".
	NonRetryableErrorTypes []string `json:"nonRetryableErrorTypes,omitempty"`
	// NonRetryableStatusCodes are HTTP statuses that end retries
	// immediately, in addition to the 4xx codes that already do.
	NonRetryableStatusCodes []int `json:"nonRetryableStatusCodes,omitempty"`
}

// SystemActivityDefaults is what a flow inherits when it sets nothing.
var SystemActivityDefaults = ActivityOptions{
	StartToCloseSeconds: 30,
	Retry: RetryConfig{
		MaxAttempts:            5,
		InitialIntervalSeconds: 1,
		BackoffCoefficient:     2,
		MaximumIntervalSeconds: 60,
	},
}

// MaxStartToCloseSeconds keeps each attempt well inside AWS Lambda's
// 15-minute invocation limit.
const MaxStartToCloseSeconds = 600

// Resolve fills zero fields of o from parent.
func (o ActivityOptions) Resolve(parent ActivityOptions) ActivityOptions {
	if o.StartToCloseSeconds == 0 {
		o.StartToCloseSeconds = parent.StartToCloseSeconds
	}
	if o.ScheduleToCloseSeconds == 0 {
		o.ScheduleToCloseSeconds = parent.ScheduleToCloseSeconds
	}
	r, p := &o.Retry, parent.Retry
	if r.MaxAttempts == 0 {
		r.MaxAttempts = p.MaxAttempts
	}
	if r.InitialIntervalSeconds == 0 {
		r.InitialIntervalSeconds = p.InitialIntervalSeconds
	}
	if r.BackoffCoefficient == 0 {
		r.BackoffCoefficient = p.BackoffCoefficient
	}
	if r.MaximumIntervalSeconds == 0 {
		r.MaximumIntervalSeconds = p.MaximumIntervalSeconds
	}
	if len(r.NonRetryableErrorTypes) == 0 {
		r.NonRetryableErrorTypes = p.NonRetryableErrorTypes
	}
	if len(r.NonRetryableStatusCodes) == 0 {
		r.NonRetryableStatusCodes = p.NonRetryableStatusCodes
	}
	if o.SlowRetry == nil {
		o.SlowRetry = parent.SlowRetry
	}
	return o
}

func (o ActivityOptions) validate() error {
	if o.StartToCloseSeconds < 0 || o.StartToCloseSeconds > MaxStartToCloseSeconds {
		return fmt.Errorf("an attempt's timeout must be between 1 and %d seconds", MaxStartToCloseSeconds)
	}
	r := o.Retry
	if o.ScheduleToCloseSeconds < 0 || r.MaxAttempts < 0 || r.InitialIntervalSeconds < 0 || r.MaximumIntervalSeconds < 0 {
		return fmt.Errorf("retry and timeout values can't be negative")
	}
	if r.BackoffCoefficient != 0 && r.BackoffCoefficient < 1 {
		return fmt.Errorf("backoff coefficient must be at least 1")
	}
	if s := o.SlowRetry; s != nil {
		if s.MaxAttempts < 0 || s.InitialIntervalSeconds < 1 || (s.BackoffCoefficient != 0 && s.BackoffCoefficient < 1) {
			return fmt.Errorf("slow retries need an interval of at least 1 second")
		}
	}
	return nil
}

type Node struct {
	ID   string   `json:"id"`
	Type NodeType `json:"type"`
	// Key names the node's output in expressions: {{ steps.<key>.body }}.
	Key      string          `json:"key"`
	Label    string          `json:"label"`
	Position Position        `json:"position"`
	Config   json.RawMessage `json:"config"`
}

type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type Edge struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Target string `json:"target"`
	// SourceHandle selects a branch on nodes with more than one outcome
	// (condition true/false, approval approved/rejected/timeout, ...).
	// Empty means the default "out" handle.
	SourceHandle string `json:"sourceHandle,omitempty"`
}

type NodeType string

const (
	// Entry points: the main trigger, the init lane, and the event lanes.
	TypeTrigger  NodeType = "trigger"
	TypeOnStart  NodeType = "onStart"
	TypeOnSignal NodeType = "onSignal"
	TypeOnUpdate NodeType = "onUpdate"

	TypeHTTP          NodeType = "http"
	TypeWaitCondition NodeType = "waitForCondition"
	TypeWaitSignal    NodeType = "waitForSignal"
	TypeApproval      NodeType = "approval"
	TypeDelay         NodeType = "delay"
	TypeCondition     NodeType = "condition"
	TypeTransform     NodeType = "transform"
	TypeSetVariable   NodeType = "setVariable"
	TypeFirstOf       NodeType = "firstOf"
	TypeRespond       NodeType = "respond"
	TypeAgent         NodeType = "agent"
	TypeRunFlow       NodeType = "runFlow"
	TypeForEach       NodeType = "forEach"
	TypeLoop          NodeType = "loop"
	// TypeEnd completes the run: an entity's explicit end of life, or an
	// early finish from any path.
	TypeEnd NodeType = "end"
)

// Handles are the named outcomes a node can route to.
const (
	HandleOut      = "out"
	HandleError    = "error"
	HandleTrue     = "true"
	HandleFalse    = "false"
	HandleApproved = "approved"
	HandleRejected = "rejected"
	HandleTimeout  = "timeout"
	HandleReceived = "received"
	// A loop's outcomes: the body runs each pass, done follows when the
	// until condition holds, limit when the pass limit is reached first. A
	// For each that runs steps runs its body once per item.
	HandleBody  = "body"
	HandleDone  = "done"
	HandleLimit = "limit"
)

// Handles lists the outcomes each node type can produce. The UI draws one
// source handle per entry; the first is the main path.
var Handles = map[NodeType][]string{
	TypeTrigger:       {HandleOut},
	TypeOnStart:       {HandleOut},
	TypeOnSignal:      {HandleOut},
	TypeOnUpdate:      {HandleOut},
	TypeHTTP:          {HandleOut, HandleError},
	TypeWaitCondition: {HandleOut, HandleTimeout},
	TypeWaitSignal:    {HandleReceived, HandleTimeout},
	TypeApproval:      {HandleApproved, HandleRejected, HandleTimeout},
	TypeDelay:         {HandleOut},
	TypeCondition:     {HandleTrue, HandleFalse},
	TypeTransform:     {HandleOut},
	TypeSetVariable:   {HandleOut},
	TypeFirstOf:       {HandleOut, HandleError},
	TypeRespond:       {HandleOut},
	TypeAgent:         {HandleOut, HandleError},
	TypeRunFlow:       {HandleOut, HandleError},
	TypeForEach:       {HandleOut, HandleBody, HandleError},
	TypeLoop:          {HandleDone, HandleBody, HandleLimit},
	TypeEnd:           {},
}

// IsEntry reports whether a node type starts the main path or a lane.
func IsEntry(t NodeType) bool {
	return t == TypeTrigger || t == TypeOnStart || t == TypeOnSignal || t == TypeOnUpdate
}

// OnStartConfig has no settings. An On start step begins the init lane: it
// runs once, when the run starts, before the main path and before any
// On signal or On update lane handles a message. Messages that arrive
// meanwhile (including the one sent with Signal- or Update-With-Start)
// wait for it. If it fails, the run fails and queued messages are dropped.
type OnStartConfig struct{}

type TriggerConfig struct {
	// Kind is "manual", "webhook", or "schedule".
	Kind string `json:"kind"`
	// SampleInput is JSON text used as the run input for manual starts.
	SampleInput string          `json:"sampleInput,omitempty"`
	Schedule    *ScheduleConfig `json:"schedule,omitempty"`
	// EverySeconds is the original interval field, still accepted.
	EverySeconds int `json:"everySeconds,omitempty"`
	// StartDelay holds manual and webhook runs back: the Workflow is created
	// at once (its ID is known and Signals can reach it) but does no work
	// until the delay has passed. Duration mode only.
	StartDelay *DelayConfig `json:"startDelay,omitempty"`
}

// ScheduleConfig describes a Temporal Schedule. Use one of Every, Cron, or
// Calendar.
type ScheduleConfig struct {
	Every     int    `json:"every,omitempty"`
	EveryUnit string `json:"everyUnit,omitempty"` // seconds, minutes, hours, days
	Cron      string `json:"cron,omitempty"`
	// Calendar runs at a time of day on chosen days.
	Calendar *CalendarSpec `json:"calendar,omitempty"`
	Timezone string        `json:"timezone,omitempty"` // IANA name; default UTC
	// Overlap is "skip" (default), "buffer_one", "buffer_all", "cancel_other",
	// "terminate_other", or "allow_all".
	Overlap              string `json:"overlap,omitempty"`
	CatchupWindowSeconds int    `json:"catchupWindowSeconds,omitempty"`
	JitterSeconds        int    `json:"jitterSeconds,omitempty"`
}

type CalendarSpec struct {
	// Days are "mon".."sun"; empty means every day.
	Days []string `json:"days,omitempty"`
	Hour int      `json:"hour"`
	// Minute is 0-59.
	Minute int `json:"minute"`
}

// Interval returns the schedule's fixed interval, if it has one.
func (c TriggerConfig) Interval() time.Duration {
	if c.Schedule != nil && c.Schedule.Every > 0 {
		return time.Duration(c.Schedule.Every) * unitDuration(c.Schedule.EveryUnit)
	}
	return time.Duration(c.EverySeconds) * time.Second
}

type OnSignalConfig struct {
	SignalName string `json:"signalName"`
}

type OnUpdateConfig struct {
	UpdateName string `json:"updateName"`
	// Validator, when set, must be true for the Update to be accepted. It
	// sees `request` (the Update's argument) and the run's data. A rejected
	// Update leaves no trace in history.
	Validator string `json:"validator,omitempty"`
}

type HTTPConfig struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
	// Activity overrides the flow's Activity defaults for this step.
	Activity ActivityOptions `json:"activity"`
	// Undo is an optional compensating request. If the run fails after this
	// step succeeded, completed undo requests run in reverse order.
	Undo *HTTPRequestSpec `json:"undo,omitempty"`
	// PauseForFix makes the step wait for a person when its retries run
	// out, instead of failing: they can retry it (optionally with a
	// corrected URL or body) or let it fail. A resumable Activity.
	PauseForFix bool `json:"pauseForFix,omitempty"`
	// RateLimit names a limit shared by every run (see SharedLimit): the
	// request runs on that limit's task queue, which Temporal rate-limits.
	RateLimit string `json:"rateLimit,omitempty"`
	// TimeoutSeconds and Retry are the original per-step fields, still
	// accepted and folded into Activity.
	TimeoutSeconds int          `json:"timeoutSeconds,omitempty"`
	Retry          *RetryConfig `json:"retry,omitempty"`
}

// ActivityOptions returns the step's own settings, including the original
// TimeoutSeconds and Retry fields.
func (c HTTPConfig) ActivityOptions() ActivityOptions {
	o := c.Activity
	if o.StartToCloseSeconds == 0 {
		o.StartToCloseSeconds = c.TimeoutSeconds
	}
	if c.Retry != nil {
		o.Retry = o.Retry.mergeFrom(*c.Retry)
	}
	return o
}

func (r RetryConfig) mergeFrom(old RetryConfig) RetryConfig {
	return ActivityOptions{Retry: r}.Resolve(ActivityOptions{Retry: old}).Retry
}

type HTTPRequestSpec struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

// WaitConditionConfig waits until an expression over the run's data is true.
// It is re-checked whenever the run's state changes: a SignalName event
// arriving, or a variable being set.
type WaitConditionConfig struct {
	// SignalName is optional: the event the condition is about, if any.
	SignalName     string `json:"signalName,omitempty"`
	Condition      string `json:"condition"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"` // 0 waits indefinitely
}

type WaitSignalConfig struct {
	SignalName     string `json:"signalName"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"` // 0 waits indefinitely
	// Collect, when set, gathers events into a batch instead of taking one
	// (an event accumulator). The timeout then applies to the first event.
	Collect *CollectConfig `json:"collect,omitempty"`
	// Match, required in a For each body, is an expression over `event` and
	// the item that picks the events meant for this item, for example
	// event.orderId == item.id. Other events stay queued for other items.
	Match string `json:"match,omitempty"`
}

// CollectConfig ends a batch when events stop arriving for QuietSeconds,
// when MaxEvents have been collected, or MaxWaitSeconds after the first one,
// whichever comes first.
type CollectConfig struct {
	QuietSeconds   int `json:"quietSeconds"`
	MaxEvents      int `json:"maxEvents,omitempty"`
	MaxWaitSeconds int `json:"maxWaitSeconds,omitempty"`
	// DedupeBy is an expression over `event`; events with a key already in
	// the batch are dropped, for example event.orderId.
	DedupeBy string `json:"dedupeBy,omitempty"`
}

type ApprovalConfig struct {
	Title          string `json:"title"`
	Details        string `json:"details,omitempty"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"` // 0 waits indefinitely
}

// DelayConfig is a durable timer.
type DelayConfig struct {
	// Mode is "duration" (Amount + Unit) or "expression".
	Mode   string `json:"mode,omitempty"`
	Amount int    `json:"amount,omitempty"`
	Unit   string `json:"unit,omitempty"` // seconds, minutes, hours, days
	// Expression returns a number of seconds, a duration ("90m", "2d",
	// duration("1h") * 3), or a time to wait until. It is recalculated when
	// a variable it uses changes, so a lane can move the deadline.
	Expression string `json:"expression,omitempty"`
	// Seconds is the original field, still accepted.
	Seconds int `json:"seconds,omitempty"`
}

type ConditionConfig struct {
	Expression string `json:"expression"`
}

type TransformConfig struct {
	// Output is a JSON template; string leaves may contain {{ expressions }}.
	Output string `json:"output"`
}

type SetVariableConfig struct {
	Assignments []Assignment `json:"assignments"`
}

type Assignment struct {
	Name string `json:"name"`
	// Value is a template: "{{ vars.count + 1 }}" keeps its type.
	Value string `json:"value"`
}

// FirstOfConfig races the branches leading into the step. The winning
// branch continues the flow, and the others are cancelled.
type FirstOfConfig struct {
	// Wins is FirstOfArrives (the default) or FirstOfSucceeds.
	Wins string `json:"wins,omitempty"`
}

const (
	// FirstOfArrives: the first branch to reach the step wins. A failed
	// branch fails the run, like a failed step anywhere else.
	FirstOfArrives = "arrives"
	// FirstOfSucceeds: a failed branch drops out of the race and the others
	// carry on (a hedged request). The step fails, or takes its "error"
	// outcome, only when every branch has failed.
	FirstOfSucceeds = "succeeds"
)

type RespondConfig struct {
	// Value is a JSON template returned to the caller: the Update's caller
	// in an On update lane, or whoever started the run on the main path
	// (early return).
	Value string `json:"value"`
}

// LoopConfig repeats the steps on its body outcome until a condition holds.
// The body is every step reachable from the body outcome; its last steps
// lead back into the loop. The body runs at least once (repeat-until).
type LoopConfig struct {
	// Until is checked after each pass, over the run's data and the body's
	// outputs. When true the loop takes its done outcome.
	Until string `json:"until"`
	// MaxPasses caps the passes; 0 is no limit. Past it the loop takes its
	// limit outcome, or fails when nothing is connected there. However many
	// passes it takes, the run continues as new when Temporal suggests it.
	MaxPasses int `json:"maxPasses"`
	// Pause is an optional durable timer between passes, a fixed duration
	// or an expression (for a backoff: steps.<loop>.pass * 5).
	Pause *DelayConfig `json:"pause,omitempty"`
}

// EndConfig has no settings: the step completes the run once any lane
// already running finishes. Steps still running on the main path stop.
type EndConfig struct{}

// MaxLoopPasses guards against a runaway loop; 0 means no limit at all.
const MaxLoopPasses = 1_000_000

// MaxForEachItems bounds one For each step. Larger sets page through a Loop.
const MaxForEachItems = 10_000

type AgentConfig struct {
	Instructions  string      `json:"instructions"`
	Tools         []AgentTool `json:"tools"`
	AllowAskHuman bool        `json:"allowAskHuman"`
	MaxTurns      int         `json:"maxTurns"`
	// ModelActivity and ToolActivity override the flow's Activity defaults
	// for model calls and tool calls.
	ModelActivity ActivityOptions `json:"modelActivity"`
	ToolActivity  ActivityOptions `json:"toolActivity"`
}

// AgentTool exposes an HTTP endpoint to the agent. URL may reference the
// tool's parameters as {{ args.<name> }}; for POST and PUT the arguments are
// also sent as the JSON body.
type AgentTool struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Method      string           `json:"method"`
	URL         string           `json:"url"`
	Params      []AgentToolParam `json:"params"`
}

type AgentToolParam struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// RunFlowConfig runs another saved flow as a child Workflow.
type RunFlowConfig struct {
	FlowID string `json:"flowId"`
	// Input is a JSON template for the child's input.
	Input string `json:"input,omitempty"`
	// ParentClosePolicy is "terminate" (default), "request_cancel", or
	// "abandon": what happens to the child if this run ends first.
	ParentClosePolicy string `json:"parentClosePolicy,omitempty"`
	// Wait is RunFlowWaitResult (the default) or RunFlowWaitStart.
	Wait string `json:"wait,omitempty"`
}

const (
	// RunFlowWaitResult waits for the child to finish; the step's output is
	// the child's step outputs.
	RunFlowWaitResult = "result"
	// RunFlowWaitStart (fire and forget) waits only until the child has
	// started, then carries on with {workflowId, runId}. It needs the abandon
	// policy: otherwise the child would be stopped whenever this run closes,
	// including each time it continues as new.
	RunFlowWaitStart = "start"
)

// ForEachConfig runs something once per item: a flow, as child Workflows, or
// the steps on its body outcome, inside this run.
type ForEachConfig struct {
	// Items is an expression returning a list.
	Items string `json:"items"`
	// Run is ForEachRunFlow (the default) or ForEachRunSteps.
	Run    string `json:"run,omitempty"`
	FlowID string `json:"flowId,omitempty"`
	// Input is a JSON template; it sees `item` and `index`.
	Input string `json:"input,omitempty"`
	// Concurrency limits how many items run at once (1-20).
	Concurrency int `json:"concurrency"`
	// Rate, when set, spaces out starting items: at most Count per second
	// or minute.
	Rate *RateLimit `json:"rate,omitempty"`
	// FinishWhen is ForEachAll (the default), ForEachFirst, or ForEachCount.
	FinishWhen string `json:"finishWhen,omitempty"`
	// Count is how many children must succeed with ForEachCount.
	Count int `json:"count,omitempty"`
}

const (
	// ForEachRunFlow runs a saved flow per item, as a child Workflow with
	// its own history.
	ForEachRunFlow = "flow"
	// ForEachRunSteps runs the steps on the body outcome per item, inside
	// this run: no child Workflows, and every step's Activities are this
	// run's. Each item sees its own `item`, `index`, and step outputs.
	ForEachRunSteps = "steps"
)

// RateLimit caps how often a For each starts an item.
type RateLimit struct {
	Count int `json:"count"`
	// Per is "second" or "minute".
	Per string `json:"per"`
}

// Interval is the average gap between starts.
func (r RateLimit) Interval() time.Duration {
	per := time.Second
	if r.Per == "minute" {
		per = time.Minute
	}
	return per / time.Duration(max(r.Count, 1))
}

// Burst is how many starts may happen together: a second's worth when the
// rate is faster than one a second, since timers only fire about once a
// second, and otherwise one.
func (r RateLimit) Burst() int {
	return max(1, int(time.Second/r.Interval()))
}

// MaxRateCount bounds RateLimit.Count.
const MaxRateCount = 1000

// InForEachBody lists the step types a For each body can hold. The others
// answer a caller, end the run, or open a region of their own; put them in a
// flow and run it per item instead. Approvals and fixes in a body are
// addressed to an item, and a Wait for signal there matches its events to
// items.
var InForEachBody = []NodeType{
	TypeHTTP, TypeCondition, TypeTransform, TypeSetVariable, TypeDelay, TypeRunFlow, TypeFirstOf,
	TypeApproval, TypeWaitSignal, TypeWaitCondition, TypeAgent,
}

// SharedLimit is a rate limit shared by every run: a downstream service's
// quota. Limits are deployment configuration (RATE_LIMITS), not part of a
// flow: each is a task queue whose Activities Temporal dispatches no faster
// than the rate, however many runs or workers there are.
type SharedLimit struct {
	Name string `json:"name"`
	RateLimit
}

// ParseSharedLimits reads "crm=5/s,payments=30/m".
func ParseSharedLimits(s string) ([]SharedLimit, error) {
	var limits []SharedLimit
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, rate, ok := strings.Cut(part, "=")
		count, per, ok2 := strings.Cut(rate, "/")
		n, err := strconv.Atoi(count)
		units := map[string]string{"s": "second", "second": "second", "m": "minute", "min": "minute", "minute": "minute"}
		if !ok || !ok2 || err != nil || n < 1 || n > MaxRateCount || units[per] == "" || !keyPattern.MatchString(name) {
			return nil, fmt.Errorf("rate limit %q: want name=N/s or name=N/m", part)
		}
		limits = append(limits, SharedLimit{Name: name, RateLimit: RateLimit{Count: n, Per: units[per]}})
	}
	return limits, nil
}

// PerSecond is the rate as a number of starts a second.
func (r RateLimit) PerSecond() float64 {
	return float64(time.Second) / float64(r.Interval())
}

// LimitTaskQueue is the task queue of a shared limit.
func LimitTaskQueue(taskQueue, limit string) string {
	return taskQueue + "-limit-" + limit
}

// RateLimit is the shared limit a step's requests wait their turn on, if
// any: an HTTP or connector step's.
func (n Node) RateLimit() string {
	switch n.Type {
	case TypeHTTP:
		c, _ := DecodeConfig[HTTPConfig](n)
		return c.RateLimit
	}
	if IsConnector(n.Type) {
		c, _ := DecodeConfig[ConnectorConfig](n)
		return c.RateLimit
	}
	return ""
}

// SharedLimits lists the shared limits the flow's steps use.
func (d *Definition) SharedLimits() []string {
	var names []string
	for _, n := range d.Nodes {
		if limit := n.RateLimit(); limit != "" {
			names = append(names, limit)
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

const (
	// ForEachAll waits for every child. The first failure cancels the rest
	// and fails the step.
	ForEachAll = "all"
	// ForEachFirst finishes on the first child to succeed and cancels the
	// rest. Failed children are tolerated while another can still win.
	ForEachFirst = "first"
	// ForEachCount finishes once Count children have succeeded (a quorum)
	// and cancels the rest. It fails once too many have failed to reach it.
	ForEachCount = "count"
)

// ReservedUpdates are Update names the builder's own steps use.
var ReservedUpdates = []string{"approve", "answer", "fix_step", "early_response"}

// Problem is one validation finding, attached to a node when it has one.
type Problem struct {
	NodeID  string `json:"nodeId,omitempty"`
	Message string `json:"message"`
}

var keyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Validate reports everything wrong with a definition so the builder can
// highlight every bad node at once. A nil result means the flow can run.
func (d *Definition) Validate() []Problem {
	var problems []Problem
	add := func(nodeID, format string, args ...any) {
		problems = append(problems, Problem{NodeID: nodeID, Message: fmt.Sprintf(format, args...)})
	}

	if d.Name == "" {
		add("", "flow needs a name")
	}
	switch d.Settings.Direction {
	case "", "LR", "TB":
	default:
		add("", "direction must be LR or TB")
	}
	if err := d.Settings.Activity.validate(); err != nil {
		add("", "flow defaults: %s", err.Error())
	}
	variables := map[string]bool{}
	for _, v := range d.Settings.Variables {
		if !keyPattern.MatchString(v.Name) || variables[v.Name] {
			add("", "variable %q must be unique lower_snake_case", v.Name)
		}
		variables[v.Name] = true
		if v.Initial != "" && !json.Valid([]byte(v.Initial)) {
			add("", "variable %s: starting value is not valid JSON", v.Name)
		}
	}
	if d.Settings.KeyedBy != "" {
		if err := CheckExpression(d.Settings.KeyedBy); err != nil {
			add("", "keyed by: %s", err.Error())
		}
	}
	if p := d.Settings.Priority; p < 0 || p > 5 {
		add("", "priority must be between 1 (first) and 5 (last)")
	}
	switch d.Settings.IDConflictPolicy {
	case "", ConflictUseExisting, ConflictFail, ConflictTerminateExisting:
	default:
		add("", "unknown Workflow ID conflict policy %q", d.Settings.IDConflictPolicy)
	}
	switch d.Settings.IDReusePolicy {
	case "", ReuseAllowDuplicate, ReuseAllowDuplicateFailedOnly, ReuseRejectDuplicate:
	default:
		add("", "unknown Workflow ID reuse policy %q", d.Settings.IDReusePolicy)
	}

	byID := map[string]Node{}
	keys := map[string]bool{}
	triggers, inits := 0, 0
	signalLanes, updateLanes := map[string]bool{}, map[string]bool{}
	for _, n := range d.Nodes {
		if _, dup := byID[n.ID]; dup || n.ID == "" {
			add(n.ID, "node IDs must be unique and non-empty")
			continue
		}
		byID[n.ID] = n
		if !keyPattern.MatchString(n.Key) {
			add(n.ID, "key %q must be lower_snake_case", n.Key)
		} else if keys[n.Key] {
			add(n.ID, "key %q is used by another node", n.Key)
		}
		keys[n.Key] = true
		switch n.Type {
		case TypeTrigger:
			triggers++
		case TypeOnStart:
			inits++
		case TypeOnSignal:
			if c, err := DecodeConfig[OnSignalConfig](n); err == nil {
				if signalLanes[c.SignalName] {
					add(n.ID, "another lane already handles %q", c.SignalName)
				}
				signalLanes[c.SignalName] = true
			}
		case TypeOnUpdate:
			if c, err := DecodeConfig[OnUpdateConfig](n); err == nil {
				if updateLanes[c.UpdateName] {
					add(n.ID, "another lane already handles %q", c.UpdateName)
				}
				if slices.Contains(ReservedUpdates, c.UpdateName) {
					add(n.ID, "%q is used by the builder itself; choose another name", c.UpdateName)
				}
				updateLanes[c.UpdateName] = true
			}
		case TypeSetVariable:
			if c, err := DecodeConfig[SetVariableConfig](n); err == nil {
				for _, a := range c.Assignments {
					if !variables[a.Name] {
						add(n.ID, "variable %q isn't declared in flow settings", a.Name)
					}
				}
			}
		}
		if err := validateConfig(n); err != nil {
			add(n.ID, "%s", err.Error())
		}
	}
	if triggers != 1 {
		add("", "flow needs exactly one trigger (found %d)", triggers)
	}
	if inits > 1 {
		add("", "flow can have at most one On start step (found %d)", inits)
	}
	if d.RespondsEarly() {
		for _, n := range d.Nodes {
			if c, err := DecodeConfig[TriggerConfig](n); err == nil && n.Type == TypeTrigger && c.StartDelay != nil {
				add(n.ID, "a flow that responds early can't also delay its start: the caller is waiting for the response")
			}
		}
	}

	incoming := map[string]int{}
	for _, e := range d.Edges {
		src, okS := byID[e.Source]
		dst, okT := byID[e.Target]
		if !okS || !okT {
			add("", "edge %s connects a missing node", e.ID)
			continue
		}
		if !slices.Contains(Handles[src.Type], e.Handle()) {
			add(src.ID, "%s has no %q outcome", src.Label, e.Handle())
		}
		if src.Type == TypeFirstOf && e.Handle() == HandleError {
			if c, err := DecodeConfig[FirstOfConfig](src); err == nil && c.Wins != FirstOfSucceeds {
				add(src.ID, "%s has an error outcome only when its winner is \"First branch to succeed\"", src.Label)
			}
		}
		if IsEntry(dst.Type) {
			add(dst.ID, "nothing can lead into %s; it starts its own path", dst.Label)
		}
		incoming[e.Target]++
	}
	for _, n := range d.Nodes {
		switch {
		case !IsEntry(n.Type) && incoming[n.ID] == 0:
			add(n.ID, "%s is not connected to anything upstream", n.Label)
		case n.Type == TypeFirstOf && incoming[n.ID] < 2:
			add(n.ID, "%s needs at least two branches leading into it", n.Label)
		case n.Type == TypeForEach && !HasBody(n) && len(d.Body(n.ID)) > 0:
			add(n.ID, "%s runs a flow for each item; to run the steps on its body outcome instead, switch it to run steps", n.Label)
		}
	}
	if len(problems) > 0 {
		return problems
	}
	if _, err := d.TopologicalOrder(); err != nil {
		add("", "%s", err.Error())
		return problems
	}
	for _, n := range d.Nodes {
		switch {
		case HasBody(n):
			for _, msg := range d.bodyProblems(n) {
				add(n.ID, "%s", msg)
			}
		}
		if c, err := DecodeConfig[ForEachConfig](n); err == nil && n.Type == TypeForEach && c.Run == ForEachRunSteps {
			for id := range d.Body(n.ID) {
				step := byID[id]
				switch {
				case !slices.Contains(InForEachBody, step.Type):
					add(id, "%s can't run inside a For each body; put it in a flow and have %s run that flow for each item", step.Label, n.Label)
				case step.Type == TypeWaitSignal:
					if wc, err := DecodeConfig[WaitSignalConfig](step); err == nil {
						if wc.Collect != nil {
							add(id, "%s can't collect a batch inside a For each body; collect before the For each and loop over the batch", step.Label)
						}
						if wc.Match == "" {
							add(id, "%s is inside a For each body: set which events are for which item, for example event.orderId == item.id", step.Label)
						}
					}
				}
			}
		}
	}

	// Every step belongs to exactly one path: the main path or one lane.
	owner := map[string]string{}
	for _, entry := range d.Nodes {
		if !IsEntry(entry.Type) {
			continue
		}
		for id := range d.Reachable(entry.ID) {
			if prev, ok := owner[id]; ok && prev != entry.ID {
				add(id, "%s is reachable from both %s and %s; each step belongs to one path", byID[id].Label, byID[prev].Label, entry.Label)
			}
			owner[id] = entry.ID
			if byID[id].Type == TypeRespond && entry.Type == TypeOnSignal {
				add(id, "Respond answers a caller, and a Signal has none; use it on the main path or in an On update lane")
			}
			if byID[id].Type == TypeRespond && entry.Type == TypeOnStart {
				add(id, "Respond answers a caller, and On start has none; use it on the main path or in an On update lane")
			}
		}
	}
	return problems
}

// Handle returns the edge's source handle, defaulting to "out".
func (e Edge) Handle() string {
	if e.SourceHandle == "" {
		return HandleOut
	}
	return e.SourceHandle
}

// Reachable returns the IDs of the entry node and every node downstream of
// it: one path of the flow.
func (d *Definition) Reachable(entryID string) map[string]bool {
	next := map[string][]string{}
	for _, e := range d.Edges {
		next[e.Source] = append(next[e.Source], e.Target)
	}
	seen := map[string]bool{entryID: true}
	stack := []string{entryID}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, t := range next[id] {
			if !seen[t] {
				seen[t] = true
				stack = append(stack, t)
			}
		}
	}
	return seen
}

var validUnits = []string{"seconds", "minutes", "hours", "days"}

func unitDuration(unit string) time.Duration {
	switch unit {
	case "minutes":
		return time.Minute
	case "hours":
		return time.Hour
	case "days":
		return 24 * time.Hour
	default:
		return time.Second
	}
}

func validateConfig(n Node) error {
	switch n.Type {
	case TypeTrigger:
		var c TriggerConfig
		if err := decode(n, &c); err != nil {
			return err
		}
		switch c.Kind {
		case "manual", "webhook":
		case "schedule":
			s := c.Schedule
			switch {
			case s != nil && s.Cron != "":
			case s != nil && s.Calendar != nil:
				if s.Calendar.Hour < 0 || s.Calendar.Hour > 23 || s.Calendar.Minute < 0 || s.Calendar.Minute > 59 {
					return fmt.Errorf("schedule time must be a valid hour and minute")
				}
			case c.Interval() >= 10*time.Second:
			default:
				return fmt.Errorf("schedule needs an interval of at least 10 seconds, a cron expression, or a calendar time")
			}
			if s != nil && s.EveryUnit != "" && !slices.Contains(validUnits, s.EveryUnit) {
				return fmt.Errorf("unknown interval unit %q", s.EveryUnit)
			}
		default:
			return fmt.Errorf("unknown trigger kind %q", c.Kind)
		}
		if c.SampleInput != "" && !json.Valid([]byte(c.SampleInput)) {
			return fmt.Errorf("sample input is not valid JSON")
		}
		if d := c.StartDelay; d != nil {
			if c.Kind == "schedule" {
				return fmt.Errorf("a scheduled flow can't delay its start; change the schedule instead")
			}
			if d.Mode != "duration" || d.Amount < 1 || !slices.Contains(validUnits, d.Unit) {
				return fmt.Errorf("start delay needs an amount of at least 1 and a unit")
			}
		}
	case TypeOnStart:
		var c OnStartConfig
		return decode(n, &c)
	case TypeOnSignal:
		var c OnSignalConfig
		if err := decode(n, &c); err != nil {
			return err
		}
		if !keyPattern.MatchString(c.SignalName) {
			return fmt.Errorf("signal name must be lower_snake_case")
		}
	case TypeOnUpdate:
		var c OnUpdateConfig
		if err := decode(n, &c); err != nil {
			return err
		}
		if !keyPattern.MatchString(c.UpdateName) {
			return fmt.Errorf("update name must be lower_snake_case")
		}
		if c.Validator != "" {
			if err := CheckExpression(c.Validator); err != nil {
				return err
			}
		}
	case TypeHTTP:
		var c HTTPConfig
		if err := decode(n, &c); err != nil {
			return err
		}
		if c.URL == "" {
			return fmt.Errorf("HTTP request needs a URL")
		}
		if c.Undo != nil && c.Undo.URL == "" {
			return fmt.Errorf("the undo request needs a URL")
		}
		if c.RateLimit != "" && !keyPattern.MatchString(c.RateLimit) {
			return fmt.Errorf("unknown rate limit %q", c.RateLimit)
		}
		return c.ActivityOptions().validate()
	case TypeWaitCondition:
		var c WaitConditionConfig
		if err := decode(n, &c); err != nil {
			return err
		}
		if c.SignalName != "" && !keyPattern.MatchString(c.SignalName) {
			return fmt.Errorf("signal name must be lower_snake_case")
		}
		return CheckExpression(c.Condition)
	case TypeWaitSignal:
		var c WaitSignalConfig
		if err := decode(n, &c); err != nil {
			return err
		}
		if !keyPattern.MatchString(c.SignalName) {
			return fmt.Errorf("signal name must be lower_snake_case")
		}
		if c.Match != "" {
			if err := CheckExpression(c.Match); err != nil {
				return fmt.Errorf("match: %w", err)
			}
		}
		if k := c.Collect; k != nil {
			if k.QuietSeconds < 1 || k.MaxEvents < 0 || k.MaxWaitSeconds < 0 {
				return fmt.Errorf("collecting needs a quiet period of at least 1 second")
			}
			if k.DedupeBy != "" {
				if err := CheckExpression(k.DedupeBy); err != nil {
					return fmt.Errorf("dedupe by: %w", err)
				}
			}
		}
	case TypeApproval:
		var c ApprovalConfig
		if err := decode(n, &c); err != nil {
			return err
		}
		if c.Title == "" {
			return fmt.Errorf("approval needs a title")
		}
	case TypeDelay:
		var c DelayConfig
		if err := decode(n, &c); err != nil {
			return err
		}
		return c.validate()
	case TypeLoop:
		var c LoopConfig
		if err := decode(n, &c); err != nil {
			return err
		}
		if err := CheckExpression(c.Until); err != nil {
			return fmt.Errorf("until: %w", err)
		}
		if c.MaxPasses < 0 || c.MaxPasses > MaxLoopPasses {
			return fmt.Errorf("max passes must be between 1 and %d, or empty for no limit", MaxLoopPasses)
		}
		if c.Pause != nil {
			if err := c.Pause.validate(); err != nil {
				return fmt.Errorf("pause: %w", err)
			}
		}
	case TypeCondition:
		var c ConditionConfig
		if err := decode(n, &c); err != nil {
			return err
		}
		return CheckExpression(c.Expression)
	case TypeTransform:
		var c TransformConfig
		if err := decode(n, &c); err != nil {
			return err
		}
		if !json.Valid([]byte(c.Output)) {
			return fmt.Errorf("transform output must be a JSON template")
		}
	case TypeSetVariable:
		var c SetVariableConfig
		if err := decode(n, &c); err != nil {
			return err
		}
		if len(c.Assignments) == 0 {
			return fmt.Errorf("set at least one variable")
		}
	case TypeFirstOf:
		var c FirstOfConfig
		if err := decode(n, &c); err != nil {
			return err
		}
		switch c.Wins {
		case "", FirstOfArrives, FirstOfSucceeds:
		default:
			return fmt.Errorf("unknown race rule %q", c.Wins)
		}
	case TypeRespond:
		var c RespondConfig
		if err := decode(n, &c); err != nil {
			return err
		}
		if !json.Valid([]byte(c.Value)) {
			return fmt.Errorf("response must be a JSON template")
		}
	case TypeAgent:
		var c AgentConfig
		if err := decode(n, &c); err != nil {
			return err
		}
		if c.Instructions == "" {
			return fmt.Errorf("agent needs instructions")
		}
		if c.MaxTurns < 1 || c.MaxTurns > 25 {
			return fmt.Errorf("agent max turns must be between 1 and 25")
		}
		names := map[string]bool{"ask_human": true}
		for _, t := range c.Tools {
			if !keyPattern.MatchString(t.Name) || names[t.Name] {
				return fmt.Errorf("tool name %q must be unique lower_snake_case", t.Name)
			}
			names[t.Name] = true
			if t.URL == "" || t.Description == "" {
				return fmt.Errorf("tool %s needs a URL and a description", t.Name)
			}
		}
		if err := c.ModelActivity.validate(); err != nil {
			return fmt.Errorf("model calls: %w", err)
		}
		if err := c.ToolActivity.validate(); err != nil {
			return fmt.Errorf("tool calls: %w", err)
		}
	case TypeRunFlow:
		var c RunFlowConfig
		if err := decode(n, &c); err != nil {
			return err
		}
		if c.FlowID == "" {
			return fmt.Errorf("choose a flow to run")
		}
		if c.Input != "" && !json.Valid([]byte(c.Input)) {
			return fmt.Errorf("input must be a JSON template")
		}
		switch c.ParentClosePolicy {
		case "", "terminate", "request_cancel", "abandon":
		default:
			return fmt.Errorf("unknown parent close policy %q", c.ParentClosePolicy)
		}
		switch c.Wait {
		case "", RunFlowWaitResult:
		case RunFlowWaitStart:
			if c.ParentClosePolicy != "abandon" {
				return fmt.Errorf("a child this run doesn't wait for must be abandoned, or it stops when this run closes (including when it continues as new)")
			}
		default:
			return fmt.Errorf("unknown wait %q", c.Wait)
		}
	case TypeEnd:
		var c EndConfig
		return decode(n, &c)
	case TypeForEach:
		var c ForEachConfig
		if err := decode(n, &c); err != nil {
			return err
		}
		switch c.Run {
		case "", ForEachRunFlow:
			if c.FlowID == "" {
				return fmt.Errorf("choose a flow to run for each item")
			}
			if c.Input != "" && !json.Valid([]byte(c.Input)) {
				return fmt.Errorf("input must be a JSON template")
			}
		case ForEachRunSteps:
		default:
			return fmt.Errorf("unknown For each mode %q", c.Run)
		}
		if err := CheckExpression(c.Items); err != nil {
			return err
		}
		if r := c.Rate; r != nil {
			if r.Count < 1 || r.Count > MaxRateCount || (r.Per != "second" && r.Per != "minute") {
				return fmt.Errorf("the start rate must be between 1 and %d per second or per minute", MaxRateCount)
			}
		}
		if c.Concurrency < 1 || c.Concurrency > 20 {
			return fmt.Errorf("concurrency must be between 1 and 20")
		}
		switch c.FinishWhen {
		case "", ForEachAll, ForEachFirst:
		case ForEachCount:
			if c.Count < 1 || c.Count > MaxForEachItems {
				return fmt.Errorf("the number that must succeed must be between 1 and %d", MaxForEachItems)
			}
		default:
			return fmt.Errorf("unknown finish rule %q", c.FinishWhen)
		}
	default:
		if IsConnector(n.Type) {
			var c ConnectorConfig
			if err := decode(n, &c); err != nil {
				return err
			}
			return c.validate(n.Type)
		}
		return fmt.Errorf("unknown node type %q", n.Type)
	}
	return nil
}

func (c DelayConfig) validate() error {
	switch c.Mode {
	case "expression":
		return CheckExpression(c.Expression)
	case "duration":
		if c.Amount < 1 || !slices.Contains(validUnits, c.Unit) {
			return fmt.Errorf("delay needs an amount of at least 1 and a unit")
		}
	case "":
		if c.Seconds < 1 {
			return fmt.Errorf("delay must be at least 1 second")
		}
	default:
		return fmt.Errorf("unknown delay mode %q", c.Mode)
	}
	return nil
}

func decode(n Node, v any) error {
	if len(n.Config) == 0 {
		return fmt.Errorf("missing config")
	}
	if err := json.Unmarshal(n.Config, v); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	return nil
}

// DecodeConfig unmarshals a node's config into the struct for its type.
func DecodeConfig[T any](n Node) (T, error) {
	var c T
	err := decode(n, &c)
	return c, err
}

// SignalNames lists, sorted, every Signal the flow listens for: those its
// steps wait on and those that start a lane. The Workflow listens for exactly
// these.
func (d *Definition) SignalNames() []string {
	seen := map[string]bool{}
	for _, n := range d.Nodes {
		switch n.Type {
		case TypeWaitSignal:
			if c, err := DecodeConfig[WaitSignalConfig](n); err == nil {
				seen[c.SignalName] = true
			}
		case TypeWaitCondition:
			if c, err := DecodeConfig[WaitConditionConfig](n); err == nil && c.SignalName != "" {
				seen[c.SignalName] = true
			}
		case TypeOnSignal:
			if c, err := DecodeConfig[OnSignalConfig](n); err == nil {
				seen[c.SignalName] = true
			}
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// UpdateNames lists, sorted, the Updates the flow's On update lanes handle.
func (d *Definition) UpdateNames() []string {
	var names []string
	for _, n := range d.Nodes {
		if n.Type == TypeOnUpdate {
			if c, err := DecodeConfig[OnUpdateConfig](n); err == nil {
				names = append(names, c.UpdateName)
			}
		}
	}
	slices.Sort(names)
	return names
}

// SubflowIDs lists the flows this one runs as children.
func (d *Definition) SubflowIDs() []string {
	var ids []string
	for _, n := range d.Nodes {
		switch n.Type {
		case TypeRunFlow:
			if c, err := DecodeConfig[RunFlowConfig](n); err == nil {
				ids = append(ids, c.FlowID)
			}
		case TypeForEach:
			if c, err := DecodeConfig[ForEachConfig](n); err == nil && c.Run != ForEachRunSteps {
				ids = append(ids, c.FlowID)
			}
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}

// RespondsEarly reports whether the main path has a Respond step: the run
// answers whoever started it as soon as it gets there, and carries on.
func (d *Definition) RespondsEarly() bool {
	var trigger string
	for _, n := range d.Nodes {
		if n.Type == TypeTrigger {
			trigger = n.ID
		}
	}
	main := d.Reachable(trigger)
	for _, n := range d.Nodes {
		if n.Type == TypeRespond && main[n.ID] {
			return true
		}
	}
	return false
}

// HasBody reports whether a node owns a body region: a Loop, or a For each
// that runs steps.
func HasBody(n Node) bool {
	switch n.Type {
	case TypeLoop:
		return true
	case TypeForEach:
		c, err := DecodeConfig[ForEachConfig](n)
		return err == nil && c.Run == ForEachRunSteps
	}
	return false
}

// Body returns the steps a Loop repeats, or a For each runs per item:
// everything reachable from its body outcome without passing back through
// the step itself.
func (d *Definition) Body(ownerID string) map[string]bool {
	next := map[string][]string{}
	var start []string
	for _, e := range d.Edges {
		if e.Source == ownerID {
			if e.Handle() == HandleBody {
				start = append(start, e.Target)
			}
			continue
		}
		next[e.Source] = append(next[e.Source], e.Target)
	}
	body := map[string]bool{}
	stack := slices.Clone(start)
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if id == ownerID || body[id] {
			continue
		}
		body[id] = true
		stack = append(stack, next[id]...)
	}
	return body
}

// BackEdges returns the IDs of edges that lead from a body back into the
// Loop or For each that owns it. They close the region on the canvas; the
// interpreter starts the next pass (or item) instead of following them.
func (d *Definition) BackEdges() map[string]bool {
	back := map[string]bool{}
	for _, n := range d.Nodes {
		if !HasBody(n) {
			continue
		}
		body := d.Body(n.ID)
		for _, e := range d.Edges {
			if e.Target == n.ID && body[e.Source] {
				back[e.ID] = true
			}
		}
	}
	return back
}

// bodyProblems checks that a body is a closed region: entered only through
// its owner's body outcome, left only by going back to the owner.
func (d *Definition) bodyProblems(owner Node) []string {
	body := d.Body(owner.ID)
	var problems []string
	if len(body) == 0 {
		problems = append(problems, owner.Label+" needs steps on its body outcome")
	}
	closes := false
	for _, e := range d.Edges {
		switch {
		case e.Target == owner.ID && body[e.Source]:
			closes = true
		case body[e.Target] && !body[e.Source] && !(e.Source == owner.ID && e.Handle() == HandleBody):
			problems = append(problems, fmt.Sprintf("steps in the body of %s can only be reached through its body outcome", owner.Label))
		case e.Source == owner.ID && e.Handle() != HandleBody && body[e.Target]:
			problems = append(problems, fmt.Sprintf("the %s outcome of %s leads back into its body", e.Handle(), owner.Label))
		}
	}
	if len(body) > 0 && !closes {
		problems = append(problems, "connect the last step of "+owner.Label+"'s body back into "+owner.Label)
	}
	return slices.Compact(problems)
}

// TopologicalOrder returns node IDs so every node follows its upstream nodes,
// breaking ties by the order nodes appear in the definition. The interpreter
// uses it as its deterministic scheduling order. The only cycles allowed are
// loops, closed by back edges, which the order ignores; any other repetition
// belongs inside a node (For each, the agent) or in a lane.
func (d *Definition) TopologicalOrder() ([]string, error) {
	position := map[string]int{}
	for i, n := range d.Nodes {
		position[n.ID] = i
	}
	back := d.BackEdges()
	indegree := map[string]int{}
	next := map[string][]string{}
	for _, e := range d.Edges {
		if back[e.ID] {
			continue
		}
		indegree[e.Target]++
		next[e.Source] = append(next[e.Source], e.Target)
	}
	var ready, order []string
	for _, n := range d.Nodes {
		if indegree[n.ID] == 0 {
			ready = append(ready, n.ID)
		}
	}
	for len(ready) > 0 {
		sort.Slice(ready, func(i, j int) bool { return position[ready[i]] < position[ready[j]] })
		id := ready[0]
		ready = ready[1:]
		order = append(order, id)
		for _, t := range next[id] {
			indegree[t]--
			if indegree[t] == 0 {
				ready = append(ready, t)
			}
		}
	}
	if len(order) != len(d.Nodes) {
		return nil, fmt.Errorf("flow contains a cycle; repeat steps with a Loop, or For each, instead")
	}
	return order, nil
}
