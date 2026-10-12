package flow

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/expr-lang/expr"
)

// Expressions run inside Workflow code (conditions, templates, transforms), so
// they must be deterministic: the same input must give the same answer on
// replay. now() is the only clock-reading builtin, so it is disabled, and date
// parsing is pinned to UTC instead of the worker's local zone.
var exprOptions = []expr.Option{
	expr.AllowUndefinedVariables(),
	expr.DisableBuiltin("now"),
	expr.Timezone("UTC"),
}

// Env is what expressions can see: the trigger input, and each finished
// node's output under its key.
func Env(input any, steps map[string]any) map[string]any {
	return map[string]any{"input": input, "steps": steps}
}

// CheckExpression reports a syntax error without evaluating anything.
func CheckExpression(src string) error {
	if strings.TrimSpace(src) == "" {
		return fmt.Errorf("expression is empty")
	}
	if _, err := expr.Compile(src, exprOptions...); err != nil {
		return fmt.Errorf("expression: %w", err)
	}
	return nil
}

// Eval evaluates one expression against env.
func Eval(src string, env map[string]any) (any, error) {
	program, err := expr.Compile(src, exprOptions...)
	if err != nil {
		return nil, err
	}
	return expr.Run(program, env)
}

var placeholder = regexp.MustCompile(`\{\{(.*?)\}\}`)

// Render expands {{ expression }} placeholders in a string. A template that is
// exactly one placeholder returns the raw value, so "{{ steps.order.body }}"
// stays an object rather than becoming its JSON text.
func Render(template string, env map[string]any) (any, error) {
	trimmed := strings.TrimSpace(template)
	if m := placeholder.FindStringSubmatchIndex(trimmed); m != nil && m[0] == 0 && m[1] == len(trimmed) {
		return Eval(trimmed[m[2]:m[3]], env)
	}
	var firstErr error
	out := placeholder.ReplaceAllStringFunc(template, func(match string) string {
		v, err := Eval(match[2:len(match)-2], env)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return ""
		}
		return Stringify(v)
	})
	return out, firstErr
}

// RenderString is Render for places that need text, such as URLs and bodies.
func RenderString(template string, env map[string]any) (string, error) {
	v, err := Render(template, env)
	if err != nil {
		return "", err
	}
	return Stringify(v), nil
}

// RenderJSON parses a JSON template and renders every string inside it.
func RenderJSON(template string, env map[string]any) (any, error) {
	var tree any
	if err := json.Unmarshal([]byte(template), &tree); err != nil {
		return nil, err
	}
	return renderTree(tree, env)
}

func renderTree(v any, env map[string]any) (any, error) {
	switch t := v.(type) {
	case string:
		return Render(t, env)
	case []any:
		for i := range t {
			r, err := renderTree(t[i], env)
			if err != nil {
				return nil, err
			}
			t[i] = r
		}
		return t, nil
	case map[string]any:
		// Sorted so the first error reported is the same on every replay.
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			r, err := renderTree(t[k], env)
			if err != nil {
				return nil, err
			}
			t[k] = r
		}
		return t, nil
	default:
		return v, nil
	}
}

// Stringify turns a value into text: strings as-is, everything else as JSON.
// encoding/json sorts map keys, so the result is stable across replays.
func Stringify(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprint(t)
		}
		return string(b)
	}
}

// Truthy decides which branch a condition takes.
func Truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != "" && t != "false"
	case float64:
		return t != 0
	case int:
		return t != 0
	default:
		return true
	}
}
