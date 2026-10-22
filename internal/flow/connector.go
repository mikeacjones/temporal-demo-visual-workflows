package flow

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/connectors"
)

// ConnectorConfig is a business connector step: one operation of a service
// such as Salesforce or Stripe (see package connectors), run as an Activity.
// The connection, credentials included, is the worker's, so a flow never
// holds a secret.
type ConnectorConfig struct {
	Operation string `json:"operation"`
	// Settings say what the operation acts on (an sObject, a table, a
	// repository): templates as text, switches as booleans.
	Settings map[string]any `json:"settings,omitempty"`
	// Mapping gives the operation's inputs their values: a template per
	// input field, by its dotted path (values.Name, metadata.plan). The
	// Workflow renders them deterministically into the Activity's argument.
	Mapping map[string]string `json:"mapping,omitempty"`
	// Undo compensates if the run fails after this step succeeded, as the
	// operation's spec says (for example, deleting what a create made).
	Undo bool `json:"undo,omitempty"`
	// Activity overrides the flow's Activity defaults for this step.
	Activity ActivityOptions `json:"activity"`
	// RateLimit names a limit shared by every run, as on an HTTP step.
	RateLimit string `json:"rateLimit,omitempty"`
}

// UnmarshalJSON reads connector steps saved with params (every setting and
// input in one map, objects as JSON templates) as settings and a mapping.
func (c *ConnectorConfig) UnmarshalJSON(raw []byte) error {
	type plain ConnectorConfig
	var v struct {
		plain
		Params map[string]any `json:"params"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	*c = ConnectorConfig(v.plain)
	if len(v.Params) == 0 || len(c.Mapping) > 0 || len(c.Settings) > 0 {
		return nil
	}
	c.Settings, c.Mapping = map[string]any{}, map[string]string{}
	for k, val := range v.Params {
		switch s := val.(type) {
		case bool:
			c.Settings[k] = s
		case string:
			// Settings and inputs were one map; settings are what remains
			// once the inputs are mapped. A JSON object template becomes a
			// mapping per field.
			var obj map[string]any
			if strings.HasPrefix(strings.TrimSpace(s), "{") && json.Unmarshal([]byte(s), &obj) == nil && k != "blocks" {
				flatten(c.Mapping, k, obj)
			} else {
				c.Mapping[k] = s
			}
		}
	}
	return nil
}

// settingsOf moves a legacy step's settings out of its mapping, now that its
// operation's spec says which are settings.
func (c *ConnectorConfig) settingsOf(op connectors.Operation) {
	for _, f := range op.Settings {
		if v, ok := c.Mapping[f.Name]; ok {
			if c.Settings == nil {
				c.Settings = map[string]any{}
			}
			c.Settings[f.Name] = v
			delete(c.Mapping, f.Name)
		}
	}
}

func flatten(into map[string]string, prefix string, obj map[string]any) {
	for k, v := range obj {
		switch x := v.(type) {
		case map[string]any:
			flatten(into, prefix+"."+k, x)
		case string:
			into[prefix+"."+k] = x
		default:
			raw, _ := json.Marshal(x)
			into[prefix+"."+k] = string(raw)
		}
	}
}

// Every connector is a step type with a main and an error outcome, and can
// run in a For each body.
func init() {
	for _, c := range connectors.All {
		Handles[NodeType(c.Type)] = []string{HandleOut, HandleError}
		InForEachBody = append(InForEachBody, NodeType(c.Type))
	}
}

// IsConnector reports whether a step type is a business connector.
func IsConnector(t NodeType) bool {
	_, ok := connectors.Get(string(t))
	return ok
}

// ConnectorStep reads a connector step: its connector, operation and config.
func ConnectorStep(n Node) (connectors.Connector, connectors.Operation, ConnectorConfig, error) {
	var c ConnectorConfig
	conn, ok := connectors.Get(string(n.Type))
	if !ok {
		return conn, connectors.Operation{}, c, fmt.Errorf("unknown connector %q", n.Type)
	}
	if err := decode(n, &c); err != nil {
		return conn, connectors.Operation{}, c, err
	}
	op, ok := conn.Operation(c.Operation)
	if !ok {
		return conn, op, c, fmt.Errorf("%s has no operation %q", conn.Label, c.Operation)
	}
	c.settingsOf(op)
	return conn, op, c, nil
}

func validateConnector(n Node) error {
	conn, op, c, err := ConnectorStep(n)
	if err != nil {
		return err
	}
	what := conn.Label + " " + strings.ToLower(op.Label)
	for _, f := range op.Settings {
		v, present := c.Settings[f.Name]
		if f.Type == connectors.Boolean {
			if _, ok := v.(bool); present && !ok {
				return fmt.Errorf("%s must be on or off", f.Label)
			}
			continue
		}
		s, _ := v.(string)
		if strings.TrimSpace(s) == "" {
			if f.Required {
				return fmt.Errorf("%s needs %s", what, strings.ToLower(f.Label))
			}
			continue
		}
		if err := checkValue(f, s); err != nil {
			return err
		}
	}
	for _, path := range slices.Sorted(maps.Keys(c.Mapping)) {
		f, ok := conn.Find(op.Inputs, path, c.Settings)
		if !ok || f.ReadOnly {
			return fmt.Errorf("%s has no input %s", what, path)
		}
		if err := checkValue(f, c.Mapping[path]); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	if missing := MissingInputs(conn, op, c); len(missing) > 0 {
		return fmt.Errorf("%s needs %s mapped", what, strings.Join(missing, ", "))
	}
	if c.Undo && op.Compensation == nil {
		return fmt.Errorf("%s can't be undone", what)
	}
	if c.RateLimit != "" && !keyPattern.MatchString(c.RateLimit) {
		return fmt.Errorf("unknown rate limit %q", c.RateLimit)
	}
	return c.Activity.validate()
}

// MissingInputs lists the required inputs a step doesn't map: top-level
// fields, and the fields a create requires within an object it maps
// (values.LastName for a Contact).
func MissingInputs(conn connectors.Connector, op connectors.Operation, c ConnectorConfig) []string {
	var missing []string
	mapped := func(path string) bool {
		if strings.TrimSpace(c.Mapping[path]) != "" {
			return true
		}
		for k, v := range c.Mapping {
			if strings.HasPrefix(k, path+".") && strings.TrimSpace(v) != "" {
				return true
			}
		}
		return false
	}
	for _, f := range op.Inputs {
		f = conn.Resolve(f, c.Settings)
		if f.Required && !mapped(f.Name) {
			missing = append(missing, f.Name)
			continue
		}
		if f.Type == connectors.Object && strings.TrimSpace(c.Mapping[f.Name]) == "" {
			for _, sub := range f.Fields {
				if sub.Required && !sub.ReadOnly && !mapped(f.Name+"."+sub.Name) {
					missing = append(missing, f.Name+"."+sub.Name)
				}
			}
		}
	}
	return missing
}

// checkValue checks a template's placeholders, a JSON template's syntax, and
// a literal value against the field's pattern and picklist.
func checkValue(f connectors.Field, s string) error {
	if err := CheckTemplate(s); err != nil {
		return err
	}
	if strings.Contains(s, "{{") {
		return nil
	}
	if f.Type == connectors.JSON && !json.Valid([]byte(s)) {
		return fmt.Errorf("%s must be a JSON template", labelOf(f))
	}
	if f.Pattern != "" && !regexp.MustCompile(f.Pattern).MatchString(s) {
		return fmt.Errorf("%s must be %s", labelOf(f), f.PatternHint)
	}
	if len(f.Enum) > 0 && !slices.Contains(f.Enum, s) {
		return fmt.Errorf("%s must be one of %s", labelOf(f), strings.Join(f.Enum, ", "))
	}
	return nil
}

func labelOf(f connectors.Field) string {
	if f.Label != "" {
		return f.Label
	}
	return f.Name
}

// CheckTemplate reports a syntax error in any of a template's placeholders.
func CheckTemplate(s string) error {
	for _, m := range placeholder.FindAllStringSubmatch(s, -1) {
		if err := CheckExpression(m[1]); err != nil {
			return err
		}
	}
	return nil
}

// ConnectorArgument builds a connector step's Activity argument: its
// settings and its mapped inputs, each rendered and given its field's type,
// with dotted paths nested into objects. Empty values are left out.
func ConnectorArgument(conn connectors.Connector, op connectors.Operation, c ConnectorConfig, env map[string]any) (map[string]any, error) {
	arg := map[string]any{}
	for _, f := range op.Settings {
		v, ok := c.Settings[f.Name]
		if !ok {
			continue
		}
		if b, isBool := v.(bool); isBool {
			if b {
				arg[f.Name] = true
			}
			continue
		}
		s, _ := v.(string)
		val, skip, err := renderField(f, s, env)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", labelOf(f), err)
		}
		if !skip {
			arg[f.Name] = val
		}
	}
	// Settings are rendered first: a catalog setting (the object) decides
	// the inputs' fields.
	for _, path := range slices.Sorted(maps.Keys(c.Mapping)) {
		f, ok := conn.Find(op.Inputs, path, arg)
		if !ok {
			return nil, fmt.Errorf("%s has no input %s", op.Label, path)
		}
		val, skip, err := renderField(f, c.Mapping[path], env)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if !skip {
			setPath(arg, path, val)
		}
	}
	return arg, nil
}

// renderField renders a template and gives the result the field's type.
// skip is true for an empty template, or one that renders to nothing.
func renderField(f connectors.Field, tmpl string, env map[string]any) (val any, skip bool, err error) {
	if strings.TrimSpace(tmpl) == "" {
		return nil, true, nil
	}
	if f.Type == connectors.JSON {
		v, err := RenderJSON(tmpl, env)
		return v, false, err
	}
	v, err := Render(tmpl, env)
	if err != nil {
		return nil, false, err
	}
	if v == nil {
		return nil, true, nil
	}
	v, err = coerce(f, v)
	return v, false, err
}

// coerce gives a rendered value the field's type: "48000" for a number is
// 48000, 1 for a string is "1", and "a, b" for a list of strings is [a b].
func coerce(f connectors.Field, v any) (any, error) {
	switch f.Type {
	case connectors.String, connectors.Date, connectors.DateTime:
		return Stringify(v), nil
	case connectors.Number, connectors.Integer:
		n, ok := toNumber(v)
		if !ok {
			return nil, fmt.Errorf("%q is not a number", Stringify(v))
		}
		if f.Type == connectors.Integer {
			n = math.Round(n)
		}
		return n, nil
	case connectors.Boolean:
		switch x := v.(type) {
		case bool:
			return x, nil
		case string:
			if b, err := strconv.ParseBool(strings.TrimSpace(x)); err == nil {
				return b, nil
			}
		}
		return nil, fmt.Errorf("%q is not true or false", Stringify(v))
	case connectors.Array:
		if items, ok := v.([]any); ok {
			if f.Items == connectors.String {
				out := make([]any, len(items))
				for i, it := range items {
					out[i] = Stringify(it)
				}
				return out, nil
			}
			return items, nil
		}
		var out []any
		for _, it := range strings.Split(Stringify(v), ",") {
			if it = strings.TrimSpace(it); it != "" {
				out = append(out, it)
			}
		}
		return out, nil
	case connectors.Object:
		if _, ok := v.(map[string]any); !ok {
			return nil, fmt.Errorf("expected an object, got %s", Stringify(v))
		}
	}
	return v, nil
}

func toNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return f, err == nil
	}
	return 0, false
}

// setPath sets a dotted path in a nested map, creating objects on the way.
func setPath(m map[string]any, path string, v any) {
	keys := strings.Split(path, ".")
	for _, k := range keys[:len(keys)-1] {
		next, ok := m[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[k] = next
		}
		m = next
	}
	m[keys[len(keys)-1]] = v
}

// PreviewConnector builds a connector step's Activity argument against sample
// data, with the same code the Workflow runs, and reports each input that
// fails on its own, by path. The builder's mapper shows both.
func PreviewConnector(n Node, env map[string]any) (map[string]any, map[string]string, error) {
	conn, op, c, err := ConnectorStep(n)
	if err != nil {
		return nil, nil, err
	}
	errs := map[string]string{}
	ok := ConnectorConfig{Operation: c.Operation, Settings: c.Settings, Mapping: map[string]string{}}
	if _, err := ConnectorArgument(conn, op, ok, env); err != nil {
		errs["settings"] = err.Error()
		ok.Settings = nil
	}
	for path, tmpl := range c.Mapping {
		one := ConnectorConfig{Operation: c.Operation, Settings: ok.Settings, Mapping: map[string]string{path: tmpl}}
		if _, err := ConnectorArgument(conn, op, one, env); err != nil {
			errs[path] = err.Error()
			continue
		}
		ok.Mapping[path] = tmpl
	}
	arg, err := ConnectorArgument(conn, op, ok, env)
	return arg, errs, err
}
