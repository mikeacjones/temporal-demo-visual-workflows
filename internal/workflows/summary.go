package workflows

import (
	"fmt"
	"net/url"
	"strings"

	"go.temporal.io/sdk/workflow"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/connectors"
	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
)

// Summaries are the one-line descriptions Temporal keeps with each Activity,
// timer and child Workflow (user metadata), shown beside it in the event
// history: which step it is and what it was set up to do, so a history reads
// like the flow. They are built from the step's settings, never its data:
// a URL loses its query string, and no body or mapped value is included.

const maxSummary = 200

// stepSummary describes something a step does: its label, then what, then
// its item in a For each body. A step in a compensation chain says whose
// undo it is.
func (r *flowRun) stepSummary(sc *scope, node flow.Node, what string) string {
	s := node.Label
	if what != "" {
		s += " · " + what
	}
	return r.placeSummary(sc, node, s)
}

// connectorSummary describes a connector step's call. Its Activity type
// already names the operation, so the settings say the rest (Account by
// Domain__c), and the step's label stands in only when there are none.
func (r *flowRun) connectorSummary(sc *scope, node flow.Node, op connectors.Operation, arg map[string]any) string {
	s := operationSummary(op, arg)
	if s == "" {
		s = node.Label
	}
	return r.placeSummary(sc, node, s)
}

// placeSummary adds where a step runs: its item in a For each body, and
// whose undo it is in a compensation chain.
func (r *flowRun) placeSummary(sc *scope, node flow.Node, s string) string {
	if sc != nil && sc.item != nil {
		s += fmt.Sprintf(" · item %d", sc.item.Index)
	}
	if member, ok := r.compensating[node.ID]; ok {
		s = "↺ undo " + r.nodes[member].Label + ": " + s
	}
	return truncate(s, maxSummary)
}

// requestSummary is an HTTP request without its query string or fragment,
// which could carry secrets: POST api.example.com/orders.
func requestSummary(method, raw string) string {
	method = strings.ToUpper(method)
	if method == "" {
		method = "GET"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return method + " " + strings.SplitN(raw, "?", 2)[0]
	}
	return method + " " + u.Host + u.Path
}

// operationSummary is what a connector operation's settings add to its
// Activity type, which the history already shows (SalesforceUpsertRecord):
// "Account by Domain__c". Empty when the operation has no settings to name.
func operationSummary(op connectors.Operation, arg map[string]any) string {
	if !strings.Contains(op.Summary, "{") {
		return ""
	}
	text := op.Summary
	for _, f := range op.Settings {
		text = strings.ReplaceAll(text, "{"+f.Name+"}", fmt.Sprint(arg[f.Name]))
	}
	// The operation's verb is the Activity type's: keep what follows it.
	if _, rest, ok := strings.Cut(text, " "); ok {
		return rest
	}
	return text
}

// timerSummary names a step's durable timer.
func (r *flowRun) timerSummary(sc *scope, node flow.Node, what string) workflow.TimerOptions {
	return workflow.TimerOptions{Summary: r.stepSummary(sc, node, what)}
}
