// Package connectors describes the builder's business connectors: steps that
// call a SaaS API (Salesforce, Stripe, ServiceNow, Slack, GitHub) by
// operation, instead of through a hand-built HTTP request.
//
// A connector is declared here once, as data. Each operation has:
//
//   - settings: what it acts on, set on the step (an sObject, a table, a
//     repository);
//   - inputs: the data it takes, which a step maps from the run's data, field
//     by field (Salesforce's values.Name from input.company);
//   - outputs: what it returns, which later steps map from;
//   - the Activity that runs it, and how to undo it.
//
// Salesforce objects and ServiceNow tables come from a curated catalog. The
// flow package validates steps and builds each Activity's argument from these
// specs, the interpreter runs them, and the builder draws the palette, the
// step cards and the mapping form from them. The calls themselves are
// Activities in package workflows, one per operation, which own the
// connection (the worker's credentials) and the service's error rules.
package connectors

import (
	"slices"
	"strings"
)

// Field types.
const (
	String   = "string"
	Number   = "number"
	Integer  = "integer"
	Boolean  = "boolean"
	Date     = "date"     // YYYY-MM-DD
	DateTime = "datetime" // ISO 8601
	Object   = "object"
	Array    = "array"
	// JSON is any JSON value, written as a JSON template (Slack blocks).
	JSON = "json"
	// Any is whatever a template renders to, kept as it is: a whole object
	// from an earlier step, a list, a number.
	Any = "any"
)

// Field is one field of an operation: a setting, an input or an output.
type Field struct {
	Name        string `json:"name"`
	Label       string `json:"label,omitempty"`
	Type        string `json:"type"`
	Required    bool   `json:"required,omitempty"`
	Description string `json:"description,omitempty"`
	// Enum lists the values the field takes (a picklist); EnumLabels, when
	// set, names them in the same order (ServiceNow's state 6 is Resolved).
	Enum       []string `json:"enum,omitempty"`
	EnumLabels []string `json:"enumLabels,omitempty"`
	// Suggestions are common values, one click away; others are fine.
	Suggestions []string `json:"suggestions,omitempty"`
	// Default fills the field when the step is created; Placeholder shows
	// in it while empty.
	Default     string `json:"default,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	// Multiline fields (SOQL, a message) get a larger editor.
	Multiline bool `json:"multiline,omitempty"`
	// Pattern, when set, is a regular expression a value without
	// placeholders must match, explained by PatternHint.
	Pattern     string `json:"pattern,omitempty"`
	PatternHint string `json:"patternHint,omitempty"`
	// ReadOnly fields are only ever outputs (an Id, a created date).
	ReadOnly bool `json:"readOnly,omitempty"`
	// Fields are an object's fields, or each element's of an array of
	// objects. Items is the element type of an array of anything else.
	Fields []Field `json:"fields,omitempty"`
	Items  string  `json:"items,omitempty"`
	// Open objects take fields beyond Fields: custom fields, metadata keys.
	Open bool `json:"open,omitempty"`
	// Catalog names the setting whose value picks this object's fields
	// from the connector's catalog: "object" gives Account's fields when the
	// step's object is Account.
	Catalog string `json:"catalog,omitempty"`
	// Creating means the catalog fields required to create a record are
	// required here.
	Creating bool `json:"creating,omitempty"`
}

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
	// Catalog holds the fields of the service's records by name: Salesforce
	// objects, ServiceNow tables. CatalogOf names what they are.
	Catalog   map[string][]Field `json:"catalog,omitempty"`
	CatalogOf string             `json:"catalogOf,omitempty"`
}

// Operation is one thing a connector does, run by one Activity.
type Operation struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	// Activity is the Activity type that runs it. Its argument holds the
	// settings and the inputs, by name.
	Activity string  `json:"activity"`
	Settings []Field `json:"settings,omitempty"`
	Inputs   []Field `json:"inputs,omitempty"`
	Outputs  []Field `json:"outputs,omitempty"`
	// Summary is the step card's text; {name} stands for a setting.
	Summary string `json:"summary"`
	Note    string `json:"note,omitempty"`
	// UndoNote, when set, offers an undo and says what it does.
	UndoNote string `json:"undoNote,omitempty"`
	// Undo returns the operation that compensates this one, and its
	// argument, from this one's argument and output. It runs in Workflow
	// code, so it must be deterministic.
	Undo func(arg, output map[string]any) (operation string, undoArg map[string]any) `json:"-"`
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

// Setting returns the named setting.
func (o Operation) Setting(name string) (Field, bool) {
	i := slices.IndexFunc(o.Settings, func(f Field) bool { return f.Name == name })
	if i < 0 {
		return Field{}, false
	}
	return o.Settings[i], true
}

// Resolve gives a field its catalog fields: Account's, when its catalog
// setting is Account. An object the catalog doesn't know keeps no fields
// and stays open.
func (c Connector) Resolve(f Field, settings map[string]any) Field {
	if f.Catalog == "" {
		return f
	}
	name, _ := settings[f.Catalog].(string)
	fields := c.Catalog[name]
	if f.Creating {
		f.Fields = fields
	} else {
		// Outside a create nothing is required: an update changes only
		// what it maps.
		f.Fields = make([]Field, len(fields))
		for i, cf := range fields {
			cf.Required = false
			f.Fields[i] = cf
		}
	}
	return f
}

// Find returns the field at a dotted path among fields (values.Name),
// resolving catalogs. A path under an open object that names no known field
// is a custom field of any type. ok is false when the path is unknown.
func (c Connector) Find(fields []Field, path string, settings map[string]any) (Field, bool) {
	name, rest, nested := strings.Cut(path, ".")
	i := slices.IndexFunc(fields, func(f Field) bool { return f.Name == name })
	if i < 0 {
		return Field{}, false
	}
	f := c.Resolve(fields[i], settings)
	if !nested {
		return f, true
	}
	if f.Type != Object {
		return Field{}, false
	}
	if sub, ok := c.Find(f.Fields, rest, settings); ok {
		return sub, true
	}
	if f.Open && !strings.Contains(rest, ".") {
		return Field{Name: rest, Type: ""}, true
	}
	return Field{}, false
}

// with returns m with k set, for building an undo's argument.
func with(m map[string]any, k string, v any) map[string]any {
	m[k] = v
	return m
}

// keep returns the entries of m named in keys.
func keep(m map[string]any, keys ...string) map[string]any {
	out := map[string]any{}
	for _, k := range keys {
		if v, ok := m[k]; ok {
			out[k] = v
		}
	}
	return out
}
