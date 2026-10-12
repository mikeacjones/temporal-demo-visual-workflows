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
	// KeyedBy makes the flow long-lived: one run per key, where the key is
	// this expression over the start input (for example `input.customerId`).
	// Later events for the same key reach the existing run.
	KeyedBy string `json:"keyedBy,omitempty"`
	// IdleTimeoutSeconds ends a keyed run that has received no events for
	// this long. 0 uses the default (24h).
	IdleTimeoutSeconds int `json:"idleTimeoutSeconds,omitempty"`
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
	// Entry points: the main trigger, and the event lanes.
	TypeTrigger  NodeType = "trigger"
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
)

// Handles lists the outcomes each node type can produce. The UI draws one
// source handle per entry; the first is the main path.
var Handles = map[NodeType][]string{
	TypeTrigger:       {HandleOut},
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
	TypeFirstOf:       {HandleOut},
	TypeRespond:       {HandleOut},
	TypeAgent:         {HandleOut, HandleError},
	TypeRunFlow:       {HandleOut, HandleError},
	TypeForEach:       {HandleOut, HandleError},
}

// IsEntry reports whether a node type starts the main path or a lane.
func IsEntry(t NodeType) bool {
	return t == TypeTrigger || t == TypeOnSignal || t == TypeOnUpdate
}

type TriggerConfig struct {
	// Kind is "manual", "webhook", or "schedule".
	Kind string `json:"kind"`
	// SampleInput is JSON text used as the run input for manual starts.
	SampleInput string          `json:"sampleInput,omitempty"`
	Schedule    *ScheduleConfig `json:"schedule,omitempty"`
	// EverySeconds is the original interval field, still accepted.
	EverySeconds int `json:"everySeconds,omitempty"`
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

// FirstOfConfig has no settings: the node fires when the first incoming
// branch arrives and cancels the others.
type FirstOfConfig struct{}

type RespondConfig struct {
	// Value is a JSON template returned to the Update's caller.
	Value string `json:"value"`
}

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
}

// ForEachConfig runs a flow once per item, as child Workflows.
type ForEachConfig struct {
	// Items is an expression returning a list.
	Items  string `json:"items"`
	FlowID string `json:"flowId"`
	// Input is a JSON template; it sees `item` and `index`.
	Input string `json:"input,omitempty"`
	// Concurrency limits how many children run at once (1-20).
	Concurrency int `json:"concurrency"`
}

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

	byID := map[string]Node{}
	keys := map[string]bool{}
	triggers := 0
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
		}
	}
	if len(problems) > 0 {
		return problems
	}
	if _, err := d.TopologicalOrder(); err != nil {
		add("", "%s", err.Error())
		return problems
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
			if byID[id].Type == TypeRespond && entry.Type != TypeOnUpdate {
				add(id, "Respond only works in an On update lane")
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
		return decode(n, &c)
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
	case TypeForEach:
		var c ForEachConfig
		if err := decode(n, &c); err != nil {
			return err
		}
		if c.FlowID == "" {
			return fmt.Errorf("choose a flow to run for each item")
		}
		if err := CheckExpression(c.Items); err != nil {
			return err
		}
		if c.Input != "" && !json.Valid([]byte(c.Input)) {
			return fmt.Errorf("input must be a JSON template")
		}
		if c.Concurrency < 1 || c.Concurrency > 20 {
			return fmt.Errorf("concurrency must be between 1 and 20")
		}
	default:
		return fmt.Errorf("unknown node type %q", n.Type)
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
			if c, err := DecodeConfig[ForEachConfig](n); err == nil {
				ids = append(ids, c.FlowID)
			}
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}

// TopologicalOrder returns node IDs so every node follows its upstream nodes,
// breaking ties by the order nodes appear in the definition. The interpreter
// uses it as its deterministic scheduling order. Flows are acyclic; repetition
// belongs inside a node (For each, the agent) or in a lane, not in the graph.
func (d *Definition) TopologicalOrder() ([]string, error) {
	position := map[string]int{}
	for i, n := range d.Nodes {
		position[n.ID] = i
	}
	indegree := map[string]int{}
	next := map[string][]string{}
	for _, e := range d.Edges {
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
		return nil, fmt.Errorf("flow contains a cycle; repeat work with For each or an event lane instead")
	}
	return order, nil
}
