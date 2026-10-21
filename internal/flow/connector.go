package flow

import (
	"encoding/json"
	"fmt"
	"regexp"
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
	// Params are the operation's parameters by key: templates as text, and
	// switches as booleans.
	Params map[string]any `json:"params,omitempty"`
	// Undo compensates if the run fails after this step succeeded, as the
	// operation's spec says (for example, deleting what a create made).
	Undo bool `json:"undo,omitempty"`
	// Activity overrides the flow's Activity defaults for this step.
	Activity ActivityOptions `json:"activity"`
	// RateLimit names a limit shared by every run, as on an HTTP step.
	RateLimit string `json:"rateLimit,omitempty"`
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

// ConnectorOperation returns the spec of a connector step's operation.
func ConnectorOperation(t NodeType, operation string) (connectors.Connector, connectors.Operation, error) {
	c, ok := connectors.Get(string(t))
	if !ok {
		return c, connectors.Operation{}, fmt.Errorf("unknown connector %q", t)
	}
	op, ok := c.Operation(operation)
	if !ok {
		return c, op, fmt.Errorf("%s has no operation %q", c.Label, operation)
	}
	return c, op, nil
}

func (c ConnectorConfig) validate(t NodeType) error {
	conn, op, err := ConnectorOperation(t, c.Operation)
	if err != nil {
		return err
	}
	for _, p := range op.Params {
		v, present := c.Params[p.Key]
		if p.Kind == connectors.Bool {
			if _, ok := v.(bool); present && !ok {
				return fmt.Errorf("%s must be on or off", p.Label)
			}
			continue
		}
		s, ok := v.(string)
		if present && !ok {
			return fmt.Errorf("%s must be text", p.Label)
		}
		if strings.TrimSpace(s) == "" {
			if p.Required {
				return fmt.Errorf("%s %s needs %s", conn.Label, strings.ToLower(op.Label), strings.ToLower(p.Label))
			}
			continue
		}
		if p.Kind == connectors.JSON && !json.Valid([]byte(s)) {
			return fmt.Errorf("%s must be a JSON template", p.Label)
		}
		if p.Pattern != "" && !strings.Contains(s, "{{") && !regexp.MustCompile(p.Pattern).MatchString(s) {
			return fmt.Errorf("%s must be %s", p.Label, p.PatternHint)
		}
	}
	if c.Undo && op.Undo == nil {
		return fmt.Errorf("%s %s can't be undone", conn.Label, strings.ToLower(op.Label))
	}
	if c.RateLimit != "" && !keyPattern.MatchString(c.RateLimit) {
		return fmt.Errorf("unknown rate limit %q", c.RateLimit)
	}
	return c.Activity.validate()
}

// RenderParams renders a connector step's parameters into its Activity's
// argument: text as strings, numbers as numbers, JSON as values, lists as
// lists of strings. Empty parameters are left out.
func RenderParams(op connectors.Operation, params map[string]any, env map[string]any) (map[string]any, error) {
	out := map[string]any{}
	for _, p := range op.Params {
		v, ok := params[p.Key]
		if !ok {
			continue
		}
		if p.Kind == connectors.Bool {
			if b, _ := v.(bool); b {
				out[p.Key] = true
			}
			continue
		}
		tmpl, _ := v.(string)
		if strings.TrimSpace(tmpl) == "" {
			continue
		}
		var err error
		switch p.Kind {
		case connectors.JSON:
			out[p.Key], err = RenderJSON(tmpl, env)
		case connectors.Number:
			out[p.Key], err = renderNumber(tmpl, env)
		case connectors.List:
			out[p.Key], err = renderList(tmpl, env)
		default:
			out[p.Key], err = RenderString(tmpl, env)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.Label, err)
		}
	}
	return out, nil
}

func renderNumber(tmpl string, env map[string]any) (any, error) {
	v, err := Render(tmpl, env)
	if err != nil {
		return nil, err
	}
	switch n := v.(type) {
	case int, int64, float64:
		return n, nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a number", n)
		}
		return f, nil
	}
	return nil, fmt.Errorf("%v is not a number", v)
}

func renderList(tmpl string, env map[string]any) ([]string, error) {
	v, err := Render(tmpl, env)
	if err != nil {
		return nil, err
	}
	list := []string{}
	if items, ok := v.([]any); ok {
		for _, item := range items {
			list = append(list, Stringify(item))
		}
		return list, nil
	}
	for _, item := range strings.Split(Stringify(v), ",") {
		if item = strings.TrimSpace(item); item != "" {
			list = append(list, item)
		}
	}
	return list, nil
}
