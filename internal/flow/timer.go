package flow

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// TimerTarget turns a delay expression's result into the time the timer
// fires. start is when the delay step began, so relative results count from
// there and a recalculation (after a variable changes) keeps the same origin.
//
//   - a number is seconds: vars.hold_minutes * 60
//   - a duration value: duration("2h") * 3
//   - a duration string, which may use days: "90m", "2d", "1d12h"
//   - a time, or an RFC 3339 string: date(input.deliverBy) - duration("1h")
func TimerTarget(v any, start time.Time) (time.Time, error) {
	switch t := v.(type) {
	case time.Time:
		return t, nil
	case time.Duration:
		return start.Add(t), nil
	case int:
		return start.Add(time.Duration(t) * time.Second), nil
	case int64:
		return start.Add(time.Duration(t) * time.Second), nil
	case float64:
		return start.Add(time.Duration(t * float64(time.Second))), nil
	case string:
		s := strings.TrimSpace(t)
		if d, err := ParseDuration(s); err == nil {
			return start.Add(d), nil
		}
		if at, err := time.Parse(time.RFC3339, s); err == nil {
			return at, nil
		}
		return time.Time{}, fmt.Errorf("%q is neither a duration (like 90m or 2d) nor a time", s)
	}
	return time.Time{}, fmt.Errorf("a timer needs seconds, a duration, or a time; got %T", v)
}

// ParseDuration is time.ParseDuration plus a "d" (24h) unit.
func ParseDuration(s string) (time.Duration, error) {
	days, rest, found := strings.Cut(s, "d")
	if !found {
		return time.ParseDuration(s)
	}
	n, err := strconv.ParseFloat(days, 64)
	if err != nil {
		return 0, err
	}
	d := time.Duration(n * float64(24*time.Hour))
	if rest == "" {
		return d, nil
	}
	more, err := time.ParseDuration(rest)
	return d + more, err
}

// DelayDuration returns a duration-mode delay's length.
func (c DelayConfig) DelayDuration() time.Duration {
	if c.Mode == "duration" {
		return time.Duration(c.Amount) * unitDuration(c.Unit)
	}
	return time.Duration(c.Seconds) * time.Second
}
