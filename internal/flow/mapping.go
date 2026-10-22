package flow

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/connectors"
)

// A mapping fills a typed target (a Transform's output) from the run's data,
// field by field, in Workflow code:
//
//   - Mapping gives a field its value: a template per dotted path
//     (customer.email ← {{ input.email }}). Each renders, deterministically,
//     and takes its field's type.
//   - Lists builds a list of objects element by element: lines from each
//     element of steps.query.records, optionally only where a condition
//     holds. Inside it, `it` is the current element and `itIndex` its index,
//     and the element's fields are mapped by paths such as lines[].sku.

// ListMapping builds a list field from another list, one element at a time.
type ListMapping struct {
	// From is an expression returning the source list.
	From string `json:"from"`
	// Where, when set, is an expression over it (and the run's data); only
	// elements where it is true are kept.
	Where string `json:"where,omitempty"`
}

// ElementVar is the name a list mapping's current element has in templates.
const ElementVar = "it"

var fieldName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// FindField returns the field at a path among fields: customer.email, or
// lines[].sku for a field of each element of the list lines.
func FindField(fields []connectors.Field, path string) (connectors.Field, bool) {
	name, rest, nested := strings.Cut(path, ".")
	element := strings.HasSuffix(name, "[]")
	name = strings.TrimSuffix(name, "[]")
	i := slices.IndexFunc(fields, func(f connectors.Field) bool { return f.Name == name })
	if i < 0 {
		return connectors.Field{}, false
	}
	f := fields[i]
	if element {
		if f.Type != connectors.Array {
			return connectors.Field{}, false
		}
		if !nested {
			// The element itself: a list of scalars mapped element by element.
			return connectors.Field{Name: name + "[]", Type: cmpType(f.Items)}, true
		}
		return FindField(f.Fields, rest)
	}
	if !nested {
		return f, true
	}
	if f.Type != connectors.Object {
		return connectors.Field{}, false
	}
	return FindField(f.Fields, rest)
}

func cmpType(t string) string {
	if t == "" {
		return connectors.Any
	}
	return t
}

// BuildMapped builds a target from its fields, mapping and list mappings.
// It returns the first error, naming its path.
func BuildMapped(fields []connectors.Field, mapping map[string]string, lists map[string]ListMapping, env map[string]any) (map[string]any, error) {
	out := map[string]any{}
	// Fields outside any list, in a stable order so errors and output agree
	// on every replay.
	for _, path := range slices.Sorted(maps.Keys(mapping)) {
		if strings.Contains(path, "[]") {
			continue
		}
		if _, isList := lists[path]; isList {
			continue
		}
		f, ok := FindField(fields, path)
		if !ok {
			return nil, fmt.Errorf("%s: not an output field", path)
		}
		v, skip, err := renderField(f, mapping[path], env)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if !skip {
			setPath(out, path, v)
		}
	}
	for _, path := range slices.Sorted(maps.Keys(lists)) {
		list, err := buildList(fields, path, lists[path], mapping, env)
		if err != nil {
			return nil, err
		}
		setPath(out, path, list)
	}
	return out, nil
}

// buildList maps a list field element by element.
func buildList(fields []connectors.Field, path string, l ListMapping, mapping map[string]string, env map[string]any) ([]any, error) {
	f, ok := FindField(fields, path)
	if !ok || f.Type != connectors.Array {
		return nil, fmt.Errorf("%s: not a list field", path)
	}
	src, err := Eval(l.From, env)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	items, ok := src.([]any)
	if src != nil && !ok {
		return nil, fmt.Errorf("%s: %s is not a list", path, l.From)
	}
	prefix := path + "[]"
	var subs []string
	for k := range mapping {
		if k == prefix || strings.HasPrefix(k, prefix+".") {
			subs = append(subs, k)
		}
	}
	slices.Sort(subs)
	out := make([]any, 0, len(items))
	for i, it := range items {
		el := maps.Clone(env)
		el[ElementVar], el[ElementVar+"Index"] = it, i
		if l.Where != "" {
			keep, err := Eval(l.Where, el)
			if err != nil {
				return nil, fmt.Errorf("%s where: %w", path, err)
			}
			if keep != true {
				continue
			}
		}
		if f.Items != "" || len(f.Fields) == 0 {
			// A list of values: the element's own mapping, or the element.
			v := any(it)
			if tmpl, ok := mapping[prefix]; ok {
				r, skip, err := renderField(connectors.Field{Type: cmpType(f.Items)}, tmpl, el)
				if err != nil {
					return nil, fmt.Errorf("%s[%d]: %w", path, i, err)
				}
				if skip {
					continue
				}
				v = r
			}
			out = append(out, v)
			continue
		}
		obj := map[string]any{}
		for _, k := range subs {
			if k == prefix {
				continue
			}
			rest := strings.TrimPrefix(k, prefix+".")
			sf, ok := FindField(f.Fields, rest)
			if !ok {
				return nil, fmt.Errorf("%s: not an output field", k)
			}
			v, skip, err := renderField(sf, mapping[k], el)
			if err != nil {
				return nil, fmt.Errorf("%s[%d].%s: %w", path, i, rest, err)
			}
			if !skip {
				setPath(obj, rest, v)
			}
		}
		out = append(out, obj)
	}
	return out, nil
}

// validateFields checks a target schema: names, types, and no duplicates.
func validateFields(fields []connectors.Field, at string) error {
	seen := map[string]bool{}
	for _, f := range fields {
		where := strings.TrimPrefix(at+"."+f.Name, ".")
		if !fieldName.MatchString(f.Name) {
			return fmt.Errorf("output field %q needs a name of letters, digits and _", where)
		}
		if seen[f.Name] {
			return fmt.Errorf("output field %q appears twice", where)
		}
		seen[f.Name] = true
		switch f.Type {
		case connectors.String, connectors.Number, connectors.Integer, connectors.Boolean, connectors.Date, connectors.DateTime, connectors.JSON, connectors.Any:
		case connectors.Object, connectors.Array:
			if err := validateFields(f.Fields, where); err != nil {
				return err
			}
		default:
			return fmt.Errorf("output field %q has unknown type %q", where, f.Type)
		}
	}
	return nil
}

// validateMapped checks a mapping against its target: every path is a field,
// templates and list expressions parse, a list field isn't also mapped whole,
// and required fields are mapped.
func validateMapped(fields []connectors.Field, mapping map[string]string, lists map[string]ListMapping) error {
	if err := validateFields(fields, ""); err != nil {
		return err
	}
	for _, path := range slices.Sorted(maps.Keys(lists)) {
		l := lists[path]
		if f, ok := FindField(fields, path); !ok || f.Type != connectors.Array || strings.Contains(path, "[]") {
			return fmt.Errorf("%s: only a list field outside other lists can be mapped element by element", path)
		}
		if err := CheckExpression(l.From); err != nil {
			return fmt.Errorf("%s from: %w", path, err)
		}
		if l.Where != "" {
			if err := CheckExpression(l.Where); err != nil {
				return fmt.Errorf("%s where: %w", path, err)
			}
		}
		if strings.TrimSpace(mapping[path]) != "" {
			return fmt.Errorf("%s is mapped both as a whole and element by element", path)
		}
	}
	for _, path := range slices.Sorted(maps.Keys(mapping)) {
		f, ok := FindField(fields, path)
		if !ok {
			return fmt.Errorf("%s isn't an output field", path)
		}
		if list, _, inList := strings.Cut(path, "[]"); inList {
			if _, ok := lists[list]; !ok {
				return fmt.Errorf("%s is inside %s, which isn't mapped from a list", path, list)
			}
		}
		if err := checkValue(f, mapping[path]); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	if missing := missingMapped(fields, "", mapping, lists); len(missing) > 0 {
		return fmt.Errorf("output needs %s mapped", strings.Join(missing, ", "))
	}
	return nil
}

// missingMapped lists required fields with nothing mapped at or under them.
func missingMapped(fields []connectors.Field, prefix string, mapping map[string]string, lists map[string]ListMapping) []string {
	var missing []string
	for _, f := range fields {
		path := prefix + f.Name
		if !f.Required {
			continue
		}
		if _, ok := lists[path]; ok {
			continue
		}
		mapped := strings.TrimSpace(mapping[path]) != ""
		for k, v := range mapping {
			if strings.HasPrefix(k, path+".") && strings.TrimSpace(v) != "" {
				mapped = true
			}
		}
		if !mapped {
			missing = append(missing, path)
		}
	}
	return missing
}

// PreviewMapped builds a target against sample data and reports each mapped
// field (and each list) that fails on its own, by path.
func PreviewMapped(fields []connectors.Field, mapping map[string]string, lists map[string]ListMapping, env map[string]any) (map[string]any, map[string]string) {
	errs := map[string]string{}
	good := map[string]string{}
	goodLists := map[string]ListMapping{}
	for path, tmpl := range mapping {
		if strings.Contains(path, "[]") {
			continue
		}
		if _, err := BuildMapped(fields, map[string]string{path: tmpl}, nil, env); err != nil {
			errs[path] = err.Error()
			continue
		}
		good[path] = tmpl
	}
	for path, l := range lists {
		sub := map[string]string{}
		for k, v := range mapping {
			if strings.HasPrefix(k, path+"[]") {
				sub[k] = v
			}
		}
		if _, err := BuildMapped(fields, sub, map[string]ListMapping{path: l}, env); err != nil {
			errs[path] = err.Error()
			continue
		}
		maps.Copy(good, sub)
		goodLists[path] = l
	}
	out, err := BuildMapped(fields, good, goodLists, env)
	if err != nil {
		errs[""] = err.Error()
	}
	return out, errs
}
