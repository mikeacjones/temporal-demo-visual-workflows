package salesforcemock

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const timeFormat = "2006-01-02T15:04:05.000+0000"

// soql is a parsed query: the subset of SOQL the demos need.
type soql struct {
	object string
	count  bool
	fields []string
	where  cond
	order  []ordering
	limit  int // -1: none
	offset int
}

type ordering struct {
	field string
	desc  bool
}

type cond interface {
	eval(get func(string) (any, bool)) (bool, error)
}

type (
	andCond struct{ l, r cond }
	orCond  struct{ l, r cond }
	notCond struct{ c cond }
	cmpCond struct {
		field string
		op    string // = != < > <= >= LIKE IN
		value any    // string, float64, bool, nil, dateRange, []any
	}
)

// dateRange is a date literal such as TODAY or LAST_N_DAYS:30: the days from
// start to end, inclusive, as YYYY-MM-DD.
type dateRange struct{ start, end string }

func (c andCond) eval(get func(string) (any, bool)) (bool, error) {
	l, err := c.l.eval(get)
	if err != nil || !l {
		return false, err
	}
	return c.r.eval(get)
}

func (c orCond) eval(get func(string) (any, bool)) (bool, error) {
	l, err := c.l.eval(get)
	if err != nil || l {
		return l, err
	}
	return c.r.eval(get)
}

func (c notCond) eval(get func(string) (any, bool)) (bool, error) {
	v, err := c.c.eval(get)
	return !v, err
}

func (c cmpCond) eval(get func(string) (any, bool)) (bool, error) {
	v, ok := get(c.field)
	if !ok {
		return false, fmt.Errorf("No such column '%s' on entity", c.field)
	}
	switch c.op {
	case "IN", "NOT IN":
		in := slices.ContainsFunc(c.value.([]any), func(x any) bool { return equal(v, x) })
		return in == (c.op == "IN"), nil
	case "LIKE":
		s, _ := v.(string)
		pattern, _ := c.value.(string)
		re := "(?is)^" + strings.NewReplacer("%", ".*", "_", ".").Replace(regexp.QuoteMeta(pattern)) + "$"
		return regexp.MustCompile(re).MatchString(s), nil
	}
	if d, ok := c.value.(dateRange); ok {
		s, _ := v.(string)
		if len(s) < 10 {
			return false, nil
		}
		day := s[:10]
		switch c.op {
		case "=":
			return day >= d.start && day <= d.end, nil
		case "!=":
			return day < d.start || day > d.end, nil
		case "<":
			return day < d.start, nil
		case "<=":
			return day <= d.end, nil
		case ">":
			return day > d.end, nil
		case ">=":
			return day >= d.start, nil
		}
	}
	switch c.op {
	case "=":
		return equal(v, c.value), nil
	case "!=":
		return !equal(v, c.value), nil
	}
	if v == nil || c.value == nil {
		return false, nil
	}
	n := compare(v, c.value)
	switch c.op {
	case "<":
		return n < 0, nil
	case "<=":
		return n <= 0, nil
	case ">":
		return n > 0, nil
	case ">=":
		return n >= 0, nil
	}
	return false, fmt.Errorf("unexpected operator %s", c.op)
}

// equal compares as SOQL does: text without regard to case.
func equal(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if as, ok := a.(string); ok {
		bs, ok := b.(string)
		return ok && strings.EqualFold(as, bs)
	}
	return compare(a, b) == 0
}

func compare(a, b any) int {
	if af, ok := toFloat(a); ok {
		if bf, ok := toFloat(b); ok {
			switch {
			case af < bf:
				return -1
			case af > bf:
				return 1
			}
			return 0
		}
	}
	if ab, ok := a.(bool); ok {
		if bb, ok := b.(bool); ok && ab == bb {
			return 0
		}
		return 1
	}
	return strings.Compare(strings.ToLower(fmt.Sprint(a)), strings.ToLower(fmt.Sprint(b)))
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	}
	return 0, false
}

// run filters, sorts, pages and projects the object's rows.
func (q soql) run(rows map[string]record, lookup func(string) (string, record), now time.Time) ([]record, error) {
	known := map[string]bool{}
	for _, rec := range rows {
		for k := range derived(q.object, rec) {
			known[strings.ToLower(k)] = true
		}
	}
	// get reads a field, following one relationship (Account.Name). A field
	// no record has is an error, as in a real org, unless the object is
	// still empty.
	getter := func(rec record) func(string) (any, bool) {
		full := derived(q.object, rec)
		return func(field string) (any, bool) {
			rel, sub, dotted := strings.Cut(field, ".")
			if !dotted {
				v, name := fieldOf(full, field)
				return v, name != "" || len(known) == 0 || known[strings.ToLower(field)] || strings.EqualFold(field, "Id")
			}
			id, _ := fieldOf(full, rel+"Id")
			parentID, _ := id.(string)
			if parentID == "" {
				return nil, true
			}
			object, parent := lookup(parentID)
			if parent == nil {
				return nil, true
			}
			v, _ := fieldOf(derived(object, parent), sub)
			return v, true
		}
	}

	var out []record
	for _, rec := range rows {
		if q.where != nil {
			ok, err := q.where.eval(getter(rec))
			if err != nil {
				return nil, fmt.Errorf("%s '%s'", err.Error(), q.object)
			}
			if !ok {
				continue
			}
		}
		out = append(out, rec)
	}
	// A stable order first (creation order), then the query's.
	slices.SortStableFunc(out, func(a, b record) int { return strings.Compare(a["Id"].(string), b["Id"].(string)) })
	for i := len(q.order) - 1; i >= 0; i-- {
		o := q.order[i]
		slices.SortStableFunc(out, func(a, b record) int {
			av, _ := getter(a)(o.field)
			bv, _ := getter(b)(o.field)
			var n int
			switch {
			case av == nil && bv == nil:
			case av == nil:
				n = -1
			case bv == nil:
				n = 1
			default:
				n = compare(av, bv)
			}
			if o.desc {
				n = -n
			}
			return n
		})
	}
	out = out[min(q.offset, len(out)):]
	if q.limit >= 0 && len(out) > q.limit {
		out = out[:q.limit]
	}
	if q.count {
		return out, nil
	}

	projected := make([]record, 0, len(out))
	for _, rec := range out {
		get := getter(rec)
		row := record{"attributes": attributes(q.object, rec["Id"])}
		for _, field := range q.fields {
			v, ok := get(field)
			if !ok {
				return nil, fmt.Errorf("No such column '%s' on entity '%s'", field, q.object)
			}
			rel, sub, dotted := strings.Cut(field, ".")
			if !dotted {
				_, name := fieldOf(rec, field)
				row[cmpOrString(name, field)] = v
				continue
			}
			id, _ := fieldOf(rec, rel+"Id")
			if id == nil {
				row[rel] = nil
				continue
			}
			nested, _ := row[rel].(record)
			if nested == nil {
				object, _ := lookup(id.(string))
				nested = record{"attributes": attributes(object, id)}
				row[rel] = nested
			}
			nested[sub] = v
		}
		projected = append(projected, row)
	}
	return projected, nil
}

// fieldOf reads a field without regard to case, as SOQL does, and returns the
// field's own name.
func fieldOf(rec record, field string) (any, string) {
	if v, ok := rec[field]; ok {
		return v, field
	}
	for k, v := range rec {
		if strings.EqualFold(k, field) {
			return v, k
		}
	}
	return nil, ""
}

func cmpOrString(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// parseSOQL parses one query.
func parseSOQL(src string) (soql, error) {
	toks, err := tokenize(src)
	if err != nil {
		return soql{}, err
	}
	p := &parser{toks: toks, now: time.Now().UTC()}
	q, err := p.query()
	if err != nil {
		return soql{}, err
	}
	if p.pos < len(p.toks) {
		return soql{}, fmt.Errorf("unexpected token: '%s'", p.toks[p.pos].text)
	}
	return q, nil
}

type token struct {
	kind string // ident, string, number, op
	text string
}

func tokenize(src string) ([]token, error) {
	var toks []token
	rs := []rune(src)
	for i := 0; i < len(rs); {
		c := rs[i]
		switch {
		case unicode.IsSpace(c):
			i++
		case c == '\'':
			var b strings.Builder
			i++
			for ; i < len(rs) && rs[i] != '\''; i++ {
				if rs[i] == '\\' && i+1 < len(rs) {
					i++
				}
				b.WriteRune(rs[i])
			}
			if i >= len(rs) {
				return nil, fmt.Errorf("unterminated string literal")
			}
			i++
			toks = append(toks, token{"string", b.String()})
		case unicode.IsDigit(c) || (c == '-' && i+1 < len(rs) && unicode.IsDigit(rs[i+1])):
			j := i + 1
			for j < len(rs) && (unicode.IsDigit(rs[j]) || rs[j] == '.' || rs[j] == '-' || rs[j] == ':' || rs[j] == 'T' || rs[j] == 'Z' || rs[j] == '+') {
				j++
			}
			toks = append(toks, token{"number", string(rs[i:j])})
			i = j
		case unicode.IsLetter(c) || c == '_':
			j := i + 1
			for j < len(rs) && (unicode.IsLetter(rs[j]) || unicode.IsDigit(rs[j]) || rs[j] == '_' || rs[j] == '.' || rs[j] == ':') {
				j++
			}
			toks = append(toks, token{"ident", string(rs[i:j])})
			i = j
		case strings.ContainsRune("(),", c):
			toks = append(toks, token{"op", string(c)})
			i++
		case strings.ContainsRune("=!<>", c):
			j := i + 1
			if j < len(rs) && (rs[j] == '=' || (c == '<' && rs[j] == '>')) {
				j++
			}
			op := string(rs[i:j])
			if op == "<>" {
				op = "!="
			}
			toks = append(toks, token{"op", op})
			i = j
		default:
			return nil, fmt.Errorf("unexpected character '%c'", c)
		}
	}
	return toks, nil
}

type parser struct {
	toks []token
	pos  int
	now  time.Time
}

func (p *parser) peek() token {
	if p.pos < len(p.toks) {
		return p.toks[p.pos]
	}
	return token{}
}

func (p *parser) keyword(words ...string) bool {
	for i, w := range words {
		t := p.toks[min(p.pos+i, len(p.toks)-1)]
		if p.pos+i >= len(p.toks) || t.kind != "ident" || !strings.EqualFold(t.text, w) {
			return false
		}
	}
	p.pos += len(words)
	return true
}

func (p *parser) expect(kind string) (token, error) {
	t := p.peek()
	if t.kind != kind {
		if t.text == "" {
			return t, fmt.Errorf("unexpected end of query")
		}
		return t, fmt.Errorf("unexpected token: '%s'", t.text)
	}
	p.pos++
	return t, nil
}

func (p *parser) query() (soql, error) {
	q := soql{limit: -1}
	if len(p.toks) == 0 || !p.keyword("SELECT") {
		return q, fmt.Errorf("a query starts with SELECT")
	}
	if p.keyword("COUNT") {
		if !p.op("(") || !p.op(")") {
			return q, fmt.Errorf("only COUNT() is supported")
		}
		q.count = true
	} else {
		for {
			t, err := p.expect("ident")
			if err != nil {
				return q, err
			}
			q.fields = append(q.fields, t.text)
			if !p.op(",") {
				break
			}
		}
	}
	if !p.keyword("FROM") {
		return q, fmt.Errorf("expecting FROM")
	}
	t, err := p.expect("ident")
	if err != nil {
		return q, err
	}
	q.object = canonicalObject(t.text)
	if p.keyword("WHERE") {
		if q.where, err = p.or(); err != nil {
			return q, err
		}
	}
	if p.keyword("ORDER", "BY") {
		for {
			t, err := p.expect("ident")
			if err != nil {
				return q, err
			}
			o := ordering{field: t.text}
			if p.keyword("DESC") {
				o.desc = true
			} else {
				p.keyword("ASC")
			}
			if p.keyword("NULLS") && !p.keyword("FIRST") && !p.keyword("LAST") {
				return q, fmt.Errorf("expecting NULLS FIRST or NULLS LAST")
			}
			q.order = append(q.order, o)
			if !p.op(",") {
				break
			}
		}
	}
	if p.keyword("LIMIT") {
		if q.limit, err = p.integer(); err != nil {
			return q, err
		}
	}
	if p.keyword("OFFSET") {
		if q.offset, err = p.integer(); err != nil {
			return q, err
		}
	}
	return q, nil
}

func (p *parser) op(s string) bool {
	if t := p.peek(); t.kind == "op" && t.text == s {
		p.pos++
		return true
	}
	return false
}

func (p *parser) integer() (int, error) {
	t, err := p.expect("number")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(t.text)
}

func (p *parser) or() (cond, error) {
	l, err := p.and()
	for err == nil && p.keyword("OR") {
		var r cond
		if r, err = p.and(); err == nil {
			l = orCond{l, r}
		}
	}
	return l, err
}

func (p *parser) and() (cond, error) {
	l, err := p.not()
	for err == nil && p.keyword("AND") {
		var r cond
		if r, err = p.not(); err == nil {
			l = andCond{l, r}
		}
	}
	return l, err
}

func (p *parser) not() (cond, error) {
	if p.keyword("NOT") {
		c, err := p.not()
		return notCond{c}, err
	}
	if p.op("(") {
		c, err := p.or()
		if err == nil && !p.op(")") {
			err = fmt.Errorf("expecting ')'")
		}
		return c, err
	}
	return p.comparison()
}

func (p *parser) comparison() (cond, error) {
	field, err := p.expect("ident")
	if err != nil {
		return nil, err
	}
	c := cmpCond{field: field.text}
	switch {
	case p.keyword("LIKE"):
		c.op = "LIKE"
	case p.keyword("NOT", "IN"):
		c.op = "NOT IN"
	case p.keyword("IN"):
		c.op = "IN"
	default:
		t, err := p.expect("op")
		if err != nil || !slices.Contains([]string{"=", "!=", "<", ">", "<=", ">="}, t.text) {
			return nil, fmt.Errorf("expecting a comparison after %s", field.text)
		}
		c.op = t.text
	}
	if strings.HasSuffix(c.op, "IN") {
		if !p.op("(") {
			return nil, fmt.Errorf("expecting '(' after IN")
		}
		var list []any
		for {
			v, err := p.value()
			if err != nil {
				return nil, err
			}
			list = append(list, v)
			if !p.op(",") {
				break
			}
		}
		if !p.op(")") {
			return nil, fmt.Errorf("expecting ')'")
		}
		c.value = list
		return c, nil
	}
	c.value, err = p.value()
	return c, err
}

func (p *parser) value() (any, error) {
	t := p.peek()
	p.pos++
	switch t.kind {
	case "string":
		return t.text, nil
	case "number":
		if n, err := strconv.ParseFloat(t.text, 64); err == nil {
			return n, nil
		}
		// A date (2026-10-21) or datetime literal.
		if len(t.text) >= 10 {
			return dateRange{t.text[:10], t.text[:10]}, nil
		}
		return nil, fmt.Errorf("invalid number: %s", t.text)
	case "ident":
		switch strings.ToUpper(t.text) {
		case "TRUE":
			return true, nil
		case "FALSE":
			return false, nil
		case "NULL":
			return nil, nil
		}
		if d, ok := p.dateLiteral(t.text); ok {
			return d, nil
		}
		return nil, fmt.Errorf("unexpected token: '%s'", t.text)
	}
	if t.text == "" {
		return nil, fmt.Errorf("unexpected end of query")
	}
	return nil, fmt.Errorf("unexpected token: '%s'", t.text)
}

func (p *parser) dateLiteral(s string) (dateRange, bool) {
	day := func(offset int) string { return p.now.AddDate(0, 0, offset).Format("2006-01-02") }
	name, arg, hasArg := strings.Cut(strings.ToUpper(s), ":")
	n, err := strconv.Atoi(arg)
	if hasArg && (err != nil || n < 0) {
		return dateRange{}, false
	}
	switch name {
	case "TODAY":
		return dateRange{day(0), day(0)}, true
	case "YESTERDAY":
		return dateRange{day(-1), day(-1)}, true
	case "TOMORROW":
		return dateRange{day(1), day(1)}, true
	case "LAST_N_DAYS":
		return dateRange{day(-n), day(0)}, hasArg
	case "NEXT_N_DAYS":
		return dateRange{day(0), day(n)}, hasArg
	}
	return dateRange{}, false
}

// canonicalObject gives standard objects their own spelling: SOQL object
// names aren't case-sensitive.
func canonicalObject(name string) string {
	for object := range prefixes {
		if strings.EqualFold(object, name) {
			return object
		}
	}
	return name
}
