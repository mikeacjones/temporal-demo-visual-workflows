// Package connectors describes the builder's business connectors: steps that
// call a SaaS API (Salesforce, Stripe, ServiceNow, Slack, GitHub) by
// operation, instead of through a hand-built HTTP request.
//
// A connector is declared here once, as data: its operations, each
// operation's parameters, the Activity that runs it, and how to undo it. The
// flow package validates and renders steps from these specs, the interpreter
// runs them, and the builder draws each step's form from them. The calls
// themselves are Activities in package workflows, one per operation, which own
// the connection (the worker's credentials) and the service's error rules.
package connectors

import "slices"

// Parameter kinds: how a parameter is edited and rendered.
const (
	// Text is a one-line template, rendered to a string.
	Text = "text"
	// Code is a multi-line template (SOQL, a message), rendered to a string.
	Code = "code"
	// Number is a template rendered to a number.
	Number = "number"
	// JSON is a JSON template, rendered to a value.
	JSON = "json"
	// List is a comma-separated template rendered to a list of strings, or
	// a single placeholder that returns a list.
	List = "list"
	// Bool is a switch.
	Bool = "bool"
)

// Connector is one service.
type Connector struct {
	// Type is the step type in a flow, e.g. "salesforce".
	Type  string `json:"type"`
	Label string `json:"label"`
	// Connection says where the worker's credentials come from.
	Connection string `json:"connection"`
	// Errors says which failures are retried.
	Errors     string      `json:"errors"`
	Operations []Operation `json:"operations"`
}

// Operation is one thing a connector does, run by one Activity.
type Operation struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	// Activity is the Activity type that runs it. Its argument is the
	// rendered parameters, keyed as below.
	Activity string  `json:"activity"`
	Params   []Param `json:"params"`
	// Summary is the step card's text; {param} stands for a parameter.
	Summary string `json:"summary"`
	// Output says what later steps can read; {out} stands for the step's
	// output, e.g. {out}.id.
	Output string `json:"output"`
	Note   string `json:"note,omitempty"`
	// UndoNote, when set, offers an undo and says what it does.
	UndoNote string `json:"undoNote,omitempty"`
	// Undo returns the operation that compensates this one, and its
	// parameters, from this one's parameters and output. It runs in
	// Workflow code, so it must be deterministic.
	Undo func(params, output map[string]any) (operation string, undoParams map[string]any) `json:"-"`
}

// Param is one input of an operation.
type Param struct {
	// Key is the field of the Activity's argument.
	Key      string `json:"key"`
	Label    string `json:"label"`
	Kind     string `json:"kind"`
	Required bool   `json:"required,omitempty"`
	Hint     string `json:"hint,omitempty"`
	// Placeholder shows in an empty field; Default fills the field when the
	// operation is chosen.
	Placeholder string `json:"placeholder,omitempty"`
	Default     string `json:"default,omitempty"`
	// Suggestions are values one click away.
	Suggestions []string `json:"suggestions,omitempty"`
	// Pattern, when set, is a regular expression a value without
	// placeholders must match, explained by PatternHint.
	Pattern     string `json:"pattern,omitempty"`
	PatternHint string `json:"patternHint,omitempty"`
}

// All lists the connectors, in palette order.
var All = []Connector{Salesforce, Stripe, ServiceNow, Slack, GitHub}

// Get returns the connector whose steps have this type.
func Get(stepType string) (Connector, bool) {
	i := slices.IndexFunc(All, func(c Connector) bool { return c.Type == stepType })
	if i < 0 {
		return Connector{}, false
	}
	return All[i], true
}

// Operation returns the named operation.
func (c Connector) Operation(name string) (Operation, bool) {
	i := slices.IndexFunc(c.Operations, func(o Operation) bool { return o.Name == name })
	if i < 0 {
		return Operation{}, false
	}
	return c.Operations[i], true
}

// keep returns the parameters of p named in keys, for an undo.
func keep(p map[string]any, keys ...string) map[string]any {
	out := map[string]any{}
	for _, k := range keys {
		if v, ok := p[k]; ok {
			out[k] = v
		}
	}
	return out
}

func with(m map[string]any, k string, v any) map[string]any {
	m[k] = v
	return m
}
