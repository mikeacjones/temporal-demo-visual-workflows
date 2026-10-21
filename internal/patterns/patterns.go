// Package patterns is the design pattern library: each pattern from Temporal's
// design pattern catalog (https://docs.temporal.io/design-patterns), and how
// to build it in the visual builder. Most come with a flow that can be opened
// and run against the sandbox; some are things the platform does for every
// flow, and a few are worker or infrastructure concerns a flow can't express.
package patterns

import (
	"encoding/json"
	"strings"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
)

// Support says how a pattern shows up in the builder.
const (
	// Visual: draw it. The pattern has an example flow.
	Visual = "visual"
	// BuiltIn: every flow gets it without drawing anything.
	BuiltIn = "built-in"
	// NotInBuilder: a worker or deployment concern, not a flow's.
	NotInBuilder = "not-in-builder"
)

type Pattern struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
	// Summary is one sentence: what the pattern does.
	Summary string `json:"summary"`
	Support string `json:"support"`
	// UseFor lists the situations it fits.
	UseFor []string `json:"useFor"`
	// Build is how to make it in the builder, step by step. A step that is
	// about one node names it, so the page can highlight it.
	Build []Step `json:"build"`
	// Watch lists trade-offs and pitfalls.
	Watch []string `json:"watch,omitempty"`
	// TryIt says what to do after opening the example to see the pattern.
	TryIt   string `json:"tryIt,omitempty"`
	DocsURL string `json:"docsUrl"`
	// Flow is the example; Children are flows it runs as child Workflows.
	Flow     *flow.Definition  `json:"flow,omitempty"`
	Children []flow.Definition `json:"children,omitempty"`
	Related  []string          `json:"related,omitempty"`
}

type Step struct {
	Node string `json:"node,omitempty"`
	Text string `json:"text"`
}

// Categories in the catalog's order.
var Categories = []string{
	"Task orchestration", "Workflow messaging", "Entity & lifecycle", "External interaction",
	"Distributed transactions", "Error handling & retries", "Batch processing", "QoS & throughput",
	"Performance & latency", "Worker configuration",
}

const docs = "https://docs.temporal.io/design-patterns/"

// All returns every pattern, with example flows pointing at the sandbox.
func All(sandboxURL string) []Pattern {
	s := strings.TrimRight(sandboxURL, "/")
	notify := func(channel, text string) flow.HTTPConfig {
		body, _ := json.Marshal(map[string]string{"channel": channel, "text": text})
		return flow.HTTPConfig{Method: "POST", URL: s + "/notify", Body: string(body)}
	}
	manual := func(sample string) flow.TriggerConfig {
		return flow.TriggerConfig{Kind: "manual", SampleInput: sample}
	}
	retry := func(attempts int, first float64) flow.ActivityOptions {
		return flow.ActivityOptions{Retry: flow.RetryConfig{MaxAttempts: attempts, InitialIntervalSeconds: first, BackoffCoefficient: 1.5}}
	}

	return []Pattern{
		// ---- Task orchestration ----
		{
			ID: "child-workflows", Name: "Child Workflows", Category: "Task orchestration", Support: Visual,
			Summary: "Split a big process into smaller flows, each its own Workflow with its own ID, history, and lifecycle.",
			UseFor: []string{
				"Reusing a piece of process (charge a card, notify a customer) from many flows.",
				"Keeping a parent's history small: each child's events live in the child.",
				"Giving one part of a process its own retry, timeout, or cancellation boundary.",
			},
			Build: []Step{
				{Text: "Build the reusable part as its own flow first. Open the example to get both: the parent and \"Pattern: charge card\"."},
				{Node: "charge", Text: "Drag in a Run flow step and choose the child flow. The Child input template becomes the child's {{ input }}."},
				{Node: "charge", Text: "If this run ends first sets the parent close policy: terminate the child (default), ask it to cancel, or let it keep running."},
				{Node: "charge", Text: "Wait: Don't wait makes it fire and forget. The step finishes once the child has started, with its workflowId, and the child is abandoned, so it runs on after this run completes or continues as new."},
				{Node: "receipt", Text: "Later steps read the child's step outputs by their keys: {{ steps.charge.receipt.chargeId }}."},
			},
			Watch: []string{
				"A child is a separate Workflow: use it for a real boundary, not for every few steps. Activities in one flow are cheaper.",
				"A child this run doesn't wait for must be abandoned. With any other policy it would be stopped whenever the parent closes, and Continue-As-New closes the parent.",
				"The run view links to the child; it also lists under its own flow in Executions.",
			},
			TryIt: "Run it, then click the Charge card step and follow the link to the child Workflow.",
			Flow: def("pattern-child-workflows", "Pattern: child workflows", "Charges a card through a reusable child flow, then emails a receipt.",
				[]flow.Node{
					n("trigger", flow.TypeTrigger, "start", "Order paid", 0, 120, manual(`{"orderId": "A-1001", "amount": 42}`)),
					n("charge", flow.TypeRunFlow, "charge", "Charge card", 300, 120, flow.RunFlowConfig{FlowID: "pattern-charge-card", Input: `{"orderId": "{{ input.orderId }}", "amount": "{{ input.amount }}"}`}),
					n("receipt", flow.TypeHTTP, "receipt", "Email receipt", 600, 120, notify("email:customer", "Paid {{ input.orderId }}: charge {{ steps.charge.receipt.chargeId }}.")),
				},
				[]flow.Edge{e("trigger", "charge", ""), e("charge", "receipt", "")}),
			Children: []flow.Definition{*def("pattern-charge-card", "Pattern: charge card", "The reusable child: charge a card and build a receipt.",
				[]flow.Node{
					n("trigger", flow.TypeTrigger, "start", "Charge requested", 0, 120, manual(`{"orderId": "A-1001", "amount": 42}`)),
					n("charge", flow.TypeHTTP, "charge_card", "Charge card", 300, 120, flow.HTTPConfig{Method: "POST", URL: s + "/cards/charge", Body: `{"orderId": "{{ input.orderId }}", "amount": {{ input.amount }}}`}),
					n("receipt", flow.TypeTransform, "receipt", "Build receipt", 600, 120, flow.TransformConfig{Output: `{"orderId": "{{ input.orderId }}", "chargeId": "{{ steps.charge_card.body.chargeId }}"}`}),
				},
				[]flow.Edge{e("trigger", "charge", ""), e("charge", "receipt", "")})},
			DocsURL: docs + "child-workflows", Related: []string{"fanout-child-workflows", "sliding-window"},
		},
		{
			ID: "parallel-execution", Name: "Parallel Execution", Category: "Task orchestration", Support: Visual,
			Summary: "Run several steps at the same time and continue when all of them are done.",
			UseFor: []string{
				"Independent lookups that a later step combines (stock, customer, open tickets).",
				"Cutting latency: three 2-second calls take 2 seconds, not 6.",
			},
			Build: []Step{
				{Node: "trigger", Text: "Draw several edges out of one step's main outcome. Each starts a branch, and the branches run at the same time."},
				{Node: "combine", Text: "Connect every branch into one step. It runs once all its incoming edges are decided: that's the join. No extra step type needed."},
				{Text: "Each branch's step has its own retry policy; a branch that fails fails the run unless that step routes its error outcome."},
			},
			Watch: []string{
				"A branch that takes a condition's other way is skipped, and the join still runs once everything is decided.",
				"To keep going when one branch is slow or fails, use a First of race instead.",
				"Branches are drawn, so their number is fixed. For one branch per item of a list known only at run time, use a For each that runs steps (see Sliding Window).",
			},
			TryIt: "Run it and watch the three lookups start together; the summary waits for the last.",
			Flow: def("pattern-parallel", "Pattern: parallel execution", "Three lookups at once, joined into one summary.",
				[]flow.Node{
					n("trigger", flow.TypeTrigger, "start", "Ticket opened", 0, 160, manual(`{"customerId": "C-42", "sku": "GRINDER-1", "ticketId": "T-1001"}`)),
					n("stock", flow.TypeHTTP, "stock", "Check stock", 300, 0, flow.HTTPConfig{Method: "GET", URL: s + "/inventory/{{ input.sku }}"}),
					n("customer", flow.TypeHTTP, "customer", "Load customer", 300, 160, flow.HTTPConfig{Method: "GET", URL: s + "/customers/{{ input.customerId }}"}),
					n("ticket", flow.TypeHTTP, "ticket", "Load ticket", 300, 320, flow.HTTPConfig{Method: "GET", URL: s + "/tickets/{{ input.ticketId }}"}),
					n("combine", flow.TypeTransform, "summary", "Combine", 600, 160, flow.TransformConfig{Output: `{"customer": "{{ steps.customer.body.name }}", "inStock": "{{ steps.stock.body.inStock }}", "ticket": "{{ steps.ticket.body.subject }}"}`}),
				},
				[]flow.Edge{
					e("trigger", "stock", ""), e("trigger", "customer", ""), e("trigger", "ticket", ""),
					e("stock", "combine", ""), e("customer", "combine", ""), e("ticket", "combine", ""),
				}),
			DocsURL: docs + "parallel-execution", Related: []string{"pick-first", "sliding-window", "fanout-child-workflows"},
		},
		{
			ID: "pick-first", Name: "Pick First (Race)", Category: "Task orchestration", Support: Visual,
			Summary: "Run branches in parallel, keep the first result, and cancel the rest.",
			UseFor: []string{
				"Hedged requests: ask several providers, take the first good answer.",
				"Racing work against a deadline or a cancel event.",
			},
			Build: []Step{
				{Node: "trigger", Text: "Fan out: draw an edge from the trigger to each provider's step."},
				{Node: "race", Text: "Connect every branch into a First of step. Winner: First branch to succeed, so a provider that fails drops out instead of failing the run."},
				{Node: "none", Text: "With \"first to succeed\", First of gains an error outcome: it's taken only if every branch fails."},
				{Node: "book", Text: "Read the winner as {{ steps.best_quote.winner }} and its output as {{ steps.best_quote.output }}."},
			},
			Watch: []string{
				"Losing branches are cancelled: their timers stop, and in-flight HTTP requests are aborted through their heartbeat.",
				"With \"first to arrive\", the first branch to finish wins even if it failed, and a failure fails the run.",
			},
			TryIt: "Run it: Budget Freight fails, Swift wins in about 4 seconds, and Parcel Post is cancelled.",
			Flow: def("pattern-pick-first", "Pattern: pick first", "Ask three carriers at once; the first good quote wins.",
				[]flow.Node{
					n("trigger", flow.TypeTrigger, "start", "Ship parcel", 0, 160, manual(`{"orderId": "A-1001", "weightKg": 3}`)),
					n("budget", flow.TypeHTTP, "budget", "Budget Freight", 300, 0, quote(s, "budget")),
					n("swift", flow.TypeHTTP, "swift", "Swift Couriers", 300, 160, quote(s, "swift")),
					n("parcel", flow.TypeHTTP, "parcel_post", "Parcel Post", 300, 320, quote(s, "parcel-post")),
					n("race", flow.TypeFirstOf, "best_quote", "First quote", 600, 160, flow.FirstOfConfig{Wins: flow.FirstOfSucceeds}),
					n("book", flow.TypeHTTP, "book", "Book shipment", 900, 80, notify("#shipping", "Booked {{ input.orderId }} with {{ steps.best_quote.output.body.carrier }} at ${{ steps.best_quote.output.body.price }}.")),
					n("none", flow.TypeHTTP, "no_carrier", "Alert ops", 900, 260, notify("#shipping", "No carrier could quote {{ input.orderId }}: {{ steps.best_quote.error }}")),
				},
				[]flow.Edge{
					e("trigger", "budget", ""), e("trigger", "swift", ""), e("trigger", "parcel", ""),
					e("budget", "race", ""), e("swift", "race", ""), e("parcel", "race", ""),
					e("race", "book", ""), e("race", "none", flow.HandleError),
				}),
			DocsURL: docs + "pick-first", Related: []string{"parallel-execution", "fanout-child-workflows"},
		},

		// ---- Workflow messaging ----
		{
			ID: "signal-with-start", Name: "Signal with Start", Category: "Workflow messaging", Support: Visual,
			Summary: "Deliver an event to the run for a key, starting that run first if there isn't one.",
			UseFor: []string{
				"Events that should all land in one place per customer, cart, or device, without the sender knowing whether that place exists yet.",
				"Avoiding the race between \"check if it's running\" and \"start it\".",
			},
			Build: []Step{
				{Text: "In flow settings, set Keyed by to an expression over the start input, here input.cartId. That makes one run per key."},
				{Node: "on_add", Text: "Add an On signal lane for the event (add_item). Its steps run once per event and can change the run's variables."},
				{Node: "init", Text: "Add an On start lane to set the run up: here it looks the shopper up. It runs first, so the add_item sent with the start waits for it and is counted against a cart that's ready."},
				{Text: "Send events to the flow's key URL: POST /hooks/<token>/keys/<cartId>/events/add_item, or choose Signal-With-Start in the Run dialog. The app sends Signal-With-Start: it starts the cart's run if needed, and delivers the event either way."},
				{Text: "Flow settings decide what a start does when the key's Workflow ID is taken: the ID conflict policy (a run is running) and the ID reuse policy (a run has closed)."},
				{Node: "hold", Text: "A cart isn't an entity, so its run completes when the main path does. A Delay on the main path holds it open for 30 minutes, and lanes handle events meanwhile."},
				{Node: "abandoned", Text: "If the timer runs out, the cart was abandoned: the main path carries on to a reminder, and the run completes."},
				{Node: "end", Text: "Checking out ends the run early with an End run step, which cancels the timer, so no reminder goes out."},
			},
			Watch: []string{
				"Events for one key are handled one at a time, in order. Different keys run in parallel.",
				"Once the run completes, the key's next event starts a fresh run with fresh variables. For state that must outlive quiet spells, turn on Entity workflow in flow settings.",
				"Without an On start lane, the event's lane and the main path both begin as the run starts, in no order you can rely on. Put set-up that handlers depend on in On start.",
				"Signal-With-Start is atomic. Update-With-Start (an On update lane, POST .../keys/<key>/updates/<name>) also returns a result, but isn't: if no worker answers in time, the run may have started without the Update.",
			},
			TryIt: "Save it, copy the key URL pattern from the trigger's webhook URL, and POST two add_item events for the same cartId: one run handles both.",
			Flow: keyed(def("pattern-signal-with-start", "Pattern: signal with start", "One cart run per cartId; add_item events start it or reach it.",
				[]flow.Node{
					n("trigger", flow.TypeTrigger, "start", "Cart opened", 0, 0, flow.TriggerConfig{Kind: "webhook", SampleInput: `{"cartId": "cart-7"}`}),
					n("hold", flow.TypeDelay, "hold", "Hold cart 30 min", 300, 0, flow.DelayConfig{Mode: "duration", Amount: 30, Unit: "minutes"}),
					n("abandoned", flow.TypeHTTP, "abandoned", "Abandoned cart email", 600, 0, notify("email:{{ key }}", "You left {{ vars.items }} items in your cart ({{ vars.tier }} member).")),
					n("on_add", flow.TypeOnSignal, "add_item", "On add_item", 0, 200, flow.OnSignalConfig{SignalName: "add_item"}),
					n("count", flow.TypeSetVariable, "count_item", "Count item", 300, 200, flow.SetVariableConfig{Assignments: []flow.Assignment{
						{Name: "items", Value: "{{ vars.items + 1 }}"}, {Name: "last_sku", Value: "{{ steps.add_item.sku }}"},
					}}),
					n("on_checkout", flow.TypeOnSignal, "checkout", "On checkout", 0, 380, flow.OnSignalConfig{SignalName: "checkout"}),
					n("order", flow.TypeHTTP, "order", "Place order", 300, 380, flow.HTTPConfig{Method: "POST", URL: s + "/orders", Body: `{"item": "cart {{ key }}: {{ vars.items }} items"}`}),
					n("end", flow.TypeEnd, "end", "End run", 600, 380, flow.EndConfig{}),
					n("init", flow.TypeOnStart, "init", "On start", 0, 560, flow.OnStartConfig{}),
					n("shopper", flow.TypeHTTP, "shopper", "Look up shopper", 300, 560, flow.HTTPConfig{Method: "GET", URL: s + "/customers/{{ key }}"}),
					n("set_tier", flow.TypeSetVariable, "set_tier", "Remember tier", 600, 560, flow.SetVariableConfig{Assignments: []flow.Assignment{{Name: "tier", Value: "{{ steps.shopper.body.tier }}"}}}),
				},
				[]flow.Edge{e("trigger", "hold", ""), e("hold", "abandoned", ""), e("on_add", "count", ""), e("on_checkout", "order", ""), e("order", "end", ""), e("init", "shopper", ""), e("shopper", "set_tier", "")}),
				"input.cartId", flow.VariableDef{Name: "items", Initial: "0", Visible: true}, flow.VariableDef{Name: "last_sku", Initial: `""`, Visible: true}, flow.VariableDef{Name: "tier", Initial: `""`, Visible: true}),
			DocsURL: docs + "signal-with-start", Related: []string{"entity-workflow", "event-accumulator"},
		},
		{
			ID: "request-response-updates", Name: "Request-Response via Updates", Category: "Workflow messaging", Support: Visual,
			Summary: "Validate a request, change a running Workflow's state, and return a result to the caller.",
			UseFor: []string{
				"Changing a long-running process and needing to know it worked (add to cart, extend a hold, change a limit).",
				"Rejecting bad requests before they touch the run: a rejected Update leaves no trace in history.",
			},
			Build: []Step{
				{Node: "on_add", Text: "Add an On update lane. The Validator is an expression over request and the run's data; false rejects the Update."},
				{Node: "add", Text: "Steps in the lane change variables like any other step."},
				{Node: "reply", Text: "End the lane with a Respond step: its JSON is returned to the caller."},
				{Text: "Callers send the Update from the run view, POST /hooks/<token>/runs/<id>/updates/add_item, or the Temporal UI."},
			},
			Watch: []string{
				"An accepted Update is durable: it's in history and runs to completion even if a worker dies.",
				"Updates to one lane run one at a time, so a second request sees the first one's changes.",
				"To reach a run that may not exist yet, key the flow and send Update-With-Start: POST /hooks/<token>/keys/<key>/updates/<name>. Add an On start lane if the lane needs the run set up first.",
			},
			TryIt: "Run it, then send add_item with {\"qty\": 3} from the run view, and again with {\"qty\": 50} to see the validator reject it.",
			Flow: withVars(def("pattern-updates", "Pattern: request-response via updates", "An open basket that accepts validated add_item Updates and answers with the new total.",
				[]flow.Node{
					n("trigger", flow.TypeTrigger, "start", "Basket opened", 0, 0, manual(`{}`)),
					n("open", flow.TypeWaitCondition, "checkout", "Wait for checkout", 300, 0, flow.WaitConditionConfig{Condition: "vars.checked_out", TimeoutSeconds: 1800}),
					n("on_add", flow.TypeOnUpdate, "add_item", "On add_item", 0, 200, flow.OnUpdateConfig{UpdateName: "add_item", Validator: "request.qty > 0 && request.qty <= 10"}),
					n("add", flow.TypeSetVariable, "add", "Add to basket", 300, 200, flow.SetVariableConfig{Assignments: []flow.Assignment{{Name: "items", Value: "{{ vars.items + steps.add_item.qty }}"}}}),
					n("reply", flow.TypeRespond, "reply", "Respond", 600, 200, flow.RespondConfig{Value: `{"items": "{{ vars.items }}"}`}),
					n("on_checkout", flow.TypeOnSignal, "checkout_now", "On checkout", 0, 380, flow.OnSignalConfig{SignalName: "checkout_now"}),
					n("close", flow.TypeSetVariable, "close", "Close basket", 300, 380, flow.SetVariableConfig{Assignments: []flow.Assignment{{Name: "checked_out", Value: "{{ true }}"}}}),
				},
				[]flow.Edge{e("trigger", "open", ""), e("on_add", "add", ""), e("add", "reply", ""), e("on_checkout", "close", "")}),
				flow.VariableDef{Name: "items", Initial: "0", Visible: true}, flow.VariableDef{Name: "checked_out", Initial: "false"}),
			DocsURL: docs + "request-response-via-updates", Related: []string{"updatable-timer", "early-return"},
		},
		{
			ID: "event-accumulator", Name: "Event Accumulator", Category: "Workflow messaging", Support: Visual,
			Summary: "Collect events from many senders, drop duplicates, and process them as one batch once they stop arriving.",
			UseFor: []string{
				"Digests: one email for a burst of orders instead of one each.",
				"Debouncing noisy senders, or merging retries of the same event.",
			},
			Build: []Step{
				{Node: "batch", Text: "Add a Wait for signal step and switch on Collect a batch."},
				{Node: "batch", Text: "Until quiet for closes the batch after a gap with no events. At most events and At most wait cap it."},
				{Node: "batch", Text: "Drop duplicates by is an expression over event, such as event.orderId."},
				{Node: "digest", Text: "Later steps read {{ steps.batch.events }}, {{ steps.batch.count }}, and {{ steps.batch.duplicates }}."},
			},
			Watch: []string{
				"Every event is a Signal in history. For a stream that never ends, put this in a keyed flow so it continues as new.",
			},
			TryIt: "Run it, then send order_placed a few times from the run view (repeat an orderId). Fifteen seconds after the last one, the digest goes out.",
			Flow: def("pattern-event-accumulator", "Pattern: event accumulator", "Collect order_placed events until things go quiet, then send one digest.",
				[]flow.Node{
					n("trigger", flow.TypeTrigger, "start", "Open digest", 0, 120, manual(`{}`)),
					n("batch", flow.TypeWaitSignal, "batch", "Collect orders", 300, 120, flow.WaitSignalConfig{SignalName: "order_placed", TimeoutSeconds: 600, Collect: &flow.CollectConfig{QuietSeconds: 15, MaxEvents: 50, MaxWaitSeconds: 300, DedupeBy: "event.orderId"}}),
					n("digest", flow.TypeHTTP, "digest", "Send digest", 600, 120, notify("#sales", "{{ steps.batch.count }} new orders ({{ steps.batch.duplicates }} duplicates dropped).")),
				},
				[]flow.Edge{e("trigger", "batch", ""), e("batch", "digest", flow.HandleReceived)}),
			DocsURL: docs + "event-accumulator", Related: []string{"signal-with-start", "entity-workflow"},
		},

		// ---- Entity & lifecycle ----
		{
			ID: "entity-workflow", Name: "Entity Workflow", Category: "Entity & lifecycle", Support: Visual,
			Summary: "Model one long-lived business entity, such as a customer or device, as one Workflow that lives as long as the entity does, driven by Signals and Updates.",
			UseFor: []string{
				"State that belongs to one thing and changes over months or years: loyalty points, a device's config, an account's limits.",
				"Serializing changes to that thing without a database lock.",
			},
			Build: []Step{
				{Text: "Flow settings: Keyed by input.customerId (one run per customer), turn on Entity workflow, and declare the entity's variables (points, tier)."},
				{Node: "welcome", Text: "The main path is the entity's creation: it runs once in the entity's life, not once per event."},
				{Node: "on_purchase", Text: "Each kind of change is a lane: On signal for fire-and-forget events, On update when the caller needs an answer."},
				{Node: "gold", Text: "Branch on the entity's own state in the lane, with a Condition."},
				{Node: "on_balance", Text: "Expose reads with an On update lane and Respond, or mark variables visible to read them with a Query."},
				{Node: "end", Text: "An End run step is the entity's end of life (the account closes). Nothing else ends it: not a quiet year, not a deploy. It's optional: an entity without one lives for ever."},
			},
			Watch: []string{
				"Waiting costs nothing and adds no history, so an idle entity can wait indefinitely. Its history grows only with events; when Temporal suggests it, the run continues as new, carrying its state, and moves to the newest worker build.",
				"Without Entity workflow, a keyed run completes when its main path does, and the key's next event starts over with fresh variables.",
			},
			TryIt: "Save it and send purchase events for one customer through the key URL; open the run to see points accumulate. Send close_account to end it.",
			Flow: entity(def("pattern-entity", "Pattern: entity workflow", "One run per customer holds their loyalty points and tier.",
				[]flow.Node{
					n("trigger", flow.TypeTrigger, "start", "Customer joined", 0, 0, flow.TriggerConfig{Kind: "webhook", SampleInput: `{"customerId": "C-42"}`}),
					n("welcome", flow.TypeHTTP, "welcome", "Welcome email", 300, 0, notify("email:{{ key }}", "Welcome to the loyalty programme!")),
					n("on_purchase", flow.TypeOnSignal, "purchase", "On purchase", 0, 200, flow.OnSignalConfig{SignalName: "purchase"}),
					n("add", flow.TypeSetVariable, "add_points", "Add points", 300, 200, flow.SetVariableConfig{Assignments: []flow.Assignment{{Name: "points", Value: "{{ vars.points + int(steps.purchase.amount) }}"}}}),
					n("check", flow.TypeCondition, "is_gold", "500+ points?", 600, 200, flow.ConditionConfig{Expression: `vars.points >= 500 && vars.tier != "gold"`}),
					n("gold", flow.TypeSetVariable, "make_gold", "Make gold", 900, 160, flow.SetVariableConfig{Assignments: []flow.Assignment{{Name: "tier", Value: "gold"}}}),
					n("on_balance", flow.TypeOnUpdate, "balance", "On balance", 0, 400, flow.OnUpdateConfig{UpdateName: "balance"}),
					n("reply", flow.TypeRespond, "reply", "Respond", 300, 400, flow.RespondConfig{Value: `{"points": "{{ vars.points }}", "tier": "{{ vars.tier }}"}`}),
					n("on_close", flow.TypeOnSignal, "close_account", "On close_account", 0, 580, flow.OnSignalConfig{SignalName: "close_account"}),
					n("end", flow.TypeEnd, "end", "End run", 300, 580, flow.EndConfig{}),
				},
				[]flow.Edge{e("trigger", "welcome", ""), e("on_purchase", "add", ""), e("add", "check", ""), e("check", "gold", flow.HandleTrue), e("on_balance", "reply", ""), e("on_close", "end", "")}),
				"input.customerId", flow.VariableDef{Name: "points", Initial: "0", Visible: true}, flow.VariableDef{Name: "tier", Initial: `"member"`, Visible: true}),
			DocsURL: docs + "entity-workflow", Related: []string{"signal-with-start", "continue-as-new"},
		},
		{
			ID: "continue-as-new", Name: "Continue-As-New", Category: "Entity & lifecycle", Support: BuiltIn,
			Summary: "Restart a long-running Workflow with fresh history, carrying its state, so history never grows without bound.",
			UseFor: []string{
				"Entities that live for months and handle thousands of events.",
				"Long loops and big batches: paging through a large dataset, a For each over thousands of items.",
				"Moving a long-lived run onto new code: the new run starts on the newest worker build.",
			},
			Build: []Step{
				{Text: "Nothing to draw: every flow does it, on the main path and after it. The run continues as new when Temporal suggests it (GetContinueAsNewSuggested), and only then; it never counts its own events."},
				{Text: "At a safe point: the main path stops starting steps; waits (for Signals, conditions, approvals, timers, batches, fixes) are interrupted and resume in the next run with their original deadlines; Activities and child Workflows already running finish first; a For each stops between children and the next run starts from the next item."},
				{Text: "Lanes already running finish, and every Signal that has arrived is recorded, so nothing in flight is lost. Variables, step outputs, unconsumed events, loop passes, and the undo stack carry over."},
				{Text: "The Workflow ID stays the same; the run view carries straight on and shows the generation in the header."},
			},
			Watch: []string{
				"It's history, not time, that drives it: an idle entity adds no history, so it can wait for months without continuing as new.",
				"A lane that is waiting on a person delays the hand-off until it finishes; waits on the main path don't.",
			},
			TryIt:   "Open the Batch Iterator or Polling example: any loop or batch long enough continues as new part way, and carries on in the next run.",
			DocsURL: docs + "continue-as-new", Related: []string{"entity-workflow", "batch-iterator"},
		},
		{
			ID: "updatable-timer", Name: "Updatable Timer", Category: "Entity & lifecycle", Support: Visual,
			Summary: "A durable timer whose deadline moves when something outside changes it.",
			UseFor: []string{
				"Holds and grace periods that can be extended or cut short.",
				"Reminders whose time a person can change while the run waits.",
			},
			Build: []Step{
				{Node: "hold", Text: "Add a Delay step in Expression mode, with an expression over a variable: vars.hold_minutes * 60."},
				{Node: "on_extend", Text: "Add a lane that changes the variable. Here an On update lane with a validator, so the caller learns the new length."},
				{Node: "set_hold", Text: "When the Set variable step runs, the timer recalculates from when it started; no restart and no polling."},
			},
			Watch: []string{"To cancel instead of extend, race the timer against a Wait for condition with a First of step (the Editable order example does)."},
			TryIt: "Run it, then send extend_hold with {\"minutes\": 2} from the run view: the timer's resume time moves.",
			Flow: withVars(def("pattern-updatable-timer", "Pattern: updatable timer", "A hold whose length an Update can extend while it runs.",
				[]flow.Node{
					n("trigger", flow.TypeTrigger, "start", "Hold placed", 0, 0, manual(`{}`)),
					n("hold", flow.TypeDelay, "hold", "Hold", 300, 0, flow.DelayConfig{Mode: "expression", Expression: "vars.hold_minutes * 60"}),
					n("release", flow.TypeHTTP, "release", "Release hold", 600, 0, notify("#ops", "Hold released after {{ vars.hold_minutes }} minutes.")),
					n("on_extend", flow.TypeOnUpdate, "extend_hold", "On extend_hold", 0, 200, flow.OnUpdateConfig{UpdateName: "extend_hold", Validator: "request.minutes > 0 && request.minutes <= 30"}),
					n("set_hold", flow.TypeSetVariable, "set_hold", "Extend", 300, 200, flow.SetVariableConfig{Assignments: []flow.Assignment{{Name: "hold_minutes", Value: "{{ vars.hold_minutes + steps.extend_hold.minutes }}"}}}),
					n("reply", flow.TypeRespond, "reply", "Respond", 600, 200, flow.RespondConfig{Value: `{"holdMinutes": "{{ vars.hold_minutes }}"}`}),
				},
				[]flow.Edge{e("trigger", "hold", ""), e("hold", "release", ""), e("on_extend", "set_hold", ""), e("set_hold", "reply", "")}),
				flow.VariableDef{Name: "hold_minutes", Initial: "1", Visible: true}),
			DocsURL: docs + "updatable-timer", Related: []string{"request-response-updates", "delayed-start"},
		},

		// ---- External interaction ----
		{
			ID: "polling", Name: "Polling External Services", Category: "External interaction", Support: Visual,
			Summary: "Check a system that can't call back, on a schedule, until it reports what you're waiting for.",
			UseFor: []string{
				"Third-party APIs with no webhooks: job status, shipment tracking, report generation.",
				"Prefer a callback when the system offers one; see Delayed Callback.",
			},
			Build: []Step{
				{Node: "poll", Text: "Add a Loop. Repeat until is checked after each pass: steps.status.body.status == \"shipped\"."},
				{Node: "status", Text: "Connect the loop's body outcome to the check, and the check back into the loop."},
				{Node: "poll", Text: "Pause between passes is a durable timer: free while it waits. Use an expression such as steps.poll.pass * 5 to back off."},
				{Node: "late", Text: "At most passes bounds it; past that the loop takes its limit outcome."},
			},
			Watch: []string{
				"Every pass adds to history; a long poll continues as new when Temporal suggests it and carries on in the next run. Leave At most passes empty for no limit.",
				"For infrequent polling (minutes apart) an HTTP step whose retry policy keeps retrying also works: the attempts live in one Activity.",
			},
			TryIt: "Run it: the order ships after about 25 seconds, so the loop takes five or six passes.",
			Flow: def("pattern-polling", "Pattern: polling", "Polls an order every 5 seconds until it ships.",
				[]flow.Node{
					n("trigger", flow.TypeTrigger, "start", "Order placed", 0, 120, manual(`{"item": "Espresso grinder"}`)),
					n("create", flow.TypeHTTP, "create_order", "Create order", 300, 120, flow.HTTPConfig{Method: "POST", URL: s + "/orders", Body: `{"item": "{{ input.item }}"}`}),
					n("poll", flow.TypeLoop, "poll", "Until shipped", 600, 120, flow.LoopConfig{Until: `steps.status.body.status == "shipped"`, MaxPasses: 20, Pause: &flow.DelayConfig{Mode: "duration", Amount: 5, Unit: "seconds"}}),
					n("status", flow.TypeHTTP, "status", "Check status", 600, 300, flow.HTTPConfig{Method: "GET", URL: s + "/orders/{{ steps.create_order.body.id }}"}),
					n("email", flow.TypeHTTP, "email", "Email customer", 900, 60, notify("email:customer", "Your {{ input.item }} has shipped.")),
					n("late", flow.TypeHTTP, "late", "Alert ops", 900, 220, notify("#warehouse", "{{ steps.create_order.body.id }} hasn't shipped after {{ steps.poll.passes }} checks.")),
				},
				[]flow.Edge{e("trigger", "create", ""), e("create", "poll", ""), e("poll", "status", flow.HandleBody), e("status", "poll", ""), e("poll", "email", flow.HandleDone), e("poll", "late", flow.HandleLimit)}),
			DocsURL: docs + "polling", Related: []string{"delayed-callback", "batch-iterator"},
		},
		{
			ID: "long-running-activity", Name: "Long-Running Activity", Category: "External interaction", Support: BuiltIn,
			Summary: "A step that runs for a long time reports it's alive with heartbeats, is retried quickly if its worker dies, and stops when cancelled.",
			UseFor:  []string{"Slow calls: report generation, large uploads, model calls."},
			Build: []Step{
				{Text: "Nothing to draw: HTTP and AI agent steps heartbeat every few seconds while they wait, with a 10-second heartbeat timeout."},
				{Node: "report", Text: "Give the step an Attempt timeout long enough for one call (up to 600 seconds, to fit a serverless invocation)."},
				{Text: "In the run view, the step's inspector shows its last heartbeat. If a worker dies mid-call, the attempt is retried within seconds rather than at the end of its timeout."},
			},
			Watch: []string{"Steps don't resume halfway: a retried attempt starts the call again. Make the call idempotent (the Add idempotency key button helps)."},
			TryIt: "Run it and cancel the run while the report is building: the sandbox logs that the request was abandoned.",
			Flow: def("pattern-long-running", "Pattern: long-running activity", "A 45-second report call that heartbeats while it runs.",
				[]flow.Node{
					n("trigger", flow.TypeTrigger, "start", "Report requested", 0, 120, manual(`{}`)),
					n("report", flow.TypeHTTP, "report", "Build report", 300, 120, flow.HTTPConfig{Method: "POST", URL: s + "/jobs/slow?seconds=45", Body: `{}`, Activity: flow.ActivityOptions{StartToCloseSeconds: 120}}),
					n("send", flow.TypeHTTP, "send", "Send report", 600, 120, notify("#finance", "Report {{ steps.report.body.report }} is ready.")),
				},
				[]flow.Edge{e("trigger", "report", ""), e("report", "send", "")}),
			DocsURL: docs + "long-running-activity", Related: []string{"pick-first", "fixed-wall-time-retries"},
		},
		{
			ID: "delayed-start", Name: "Delayed Start", Category: "External interaction", Support: Visual,
			Summary: "Create the Workflow now, and have it start working later.",
			UseFor: []string{
				"Reminders and follow-ups (\"email in 30 minutes\") that should exist now, with an ID you can signal or cancel.",
				"Spreading load: accept the request now, do the work after a pause.",
			},
			Build: []Step{
				{Node: "trigger", Text: "On the trigger, switch on Delay the start and set how long. It applies to manual and webhook starts."},
				{Text: "The run exists immediately: it has its ID and callback URL, and Signals sent to it are kept. Its first step runs when the delay is up. Temporal holds it; there's no timer in the flow."},
			},
			Watch: []string{"Use a Delay step instead when the wait depends on data the run computes, or must change while it waits."},
			TryIt: "Run it: the run view says when it will begin; 30 seconds later the reminder goes out.",
			Flow: def("pattern-delayed-start", "Pattern: delayed start", "Created now, sends its reminder 30 seconds later.",
				[]flow.Node{
					n("trigger", flow.TypeTrigger, "start", "Cart abandoned", 0, 120, flow.TriggerConfig{Kind: "manual", SampleInput: `{"customer": "C-42"}`, StartDelay: &flow.DelayConfig{Mode: "duration", Amount: 30, Unit: "seconds"}}),
					n("remind", flow.TypeHTTP, "remind", "Send reminder", 300, 120, notify("email:{{ input.customer }}", "You left something in your cart.")),
				},
				[]flow.Edge{e("trigger", "remind", "")}),
			DocsURL: docs + "delayed-start", Related: []string{"updatable-timer"},
		},
		{
			ID: "delayed-callback", Name: "Delayed Callback (Webhooks)", Category: "External interaction", Support: Visual,
			Summary: "Make webhooks durable: inbound calls arrive as Signals, and outbound calls go out after a durable sleep.",
			UseFor: []string{
				"Systems that report back later (payment settled, order shipped) instead of being polled.",
				"Calling someone back at a set time, reliably, even if workers restart in between.",
			},
			Build: []Step{
				{Node: "create", Text: "Send the called system this run's callback URL: {{ run.callbackUrl }}/events/order_status. Each POST to it becomes an order_status Signal on the run."},
				{Node: "wait", Text: "Wait for condition (or Wait for signal) holds the run, for free, until the right event arrives."},
				{Node: "cool_off", Text: "A Delay step is the durable sleep before the outbound call."},
			},
			Watch: []string{"Events that arrive before the wait starts aren't lost: every Signal is recorded as it arrives."},
			TryIt: "Run it: the store calls back on each status change; about 25 seconds in, the order ships, and 20 seconds later the follow-up goes out.",
			Flow: def("pattern-delayed-callback", "Pattern: delayed callback", "Waits for the store's shipped callback, sleeps, then calls out.",
				[]flow.Node{
					n("trigger", flow.TypeTrigger, "start", "Order placed", 0, 120, manual(`{"item": "Espresso grinder"}`)),
					n("create", flow.TypeHTTP, "create_order", "Create order", 300, 120, flow.HTTPConfig{Method: "POST", URL: s + "/orders", Body: `{"item": "{{ input.item }}", "callbackUrl": "{{ run.callbackUrl }}/events/order_status"}`}),
					n("wait", flow.TypeWaitCondition, "shipped", "Wait until shipped", 600, 120, flow.WaitConditionConfig{SignalName: "order_status", Condition: `events.order_status.status == "shipped"`, TimeoutSeconds: 300}),
					n("cool_off", flow.TypeDelay, "cool_off", "Wait 20 s", 900, 120, flow.DelayConfig{Mode: "duration", Amount: 20, Unit: "seconds"}),
					n("follow", flow.TypeHTTP, "follow_up", "Follow up", 1200, 120, notify("email:customer", "How is your {{ input.item }}?")),
				},
				[]flow.Edge{e("trigger", "create", ""), e("create", "wait", ""), e("wait", "cool_off", ""), e("cool_off", "follow", "")}),
			DocsURL: docs + "delayed-callback", Related: []string{"polling", "approval"},
		},
		{
			ID: "approval", Name: "Approval", Category: "External interaction", Support: Visual,
			Summary: "Hold the run until a person decides, with a timeout that isn't mistaken for a decision.",
			UseFor:  []string{"Refunds, access requests, publishing: anything a person signs off on."},
			Build: []Step{
				{Node: "approve", Text: "Add an Approval step with a question and details; templates show the run's data."},
				{Node: "approve", Text: "It has three outcomes: approved, rejected, and timeout. Route timeout separately: nobody deciding is not a rejection."},
				{Text: "The decision is an Update with a validator, so a double click or a late decision for a step that's no longer waiting is refused."},
			},
			Watch: []string{"In a For each body each item asks on its own, and a decision names its item: approve a batch of refunds one by one without a child flow per refund."},
			TryIt: "Run it, click the Approval step, and approve or reject; or wait five minutes for the escalation.",
			Flow: def("pattern-approval", "Pattern: approval", "A refund waits for a manager; no answer escalates.",
				[]flow.Node{
					n("trigger", flow.TypeTrigger, "start", "Refund requested", 0, 120, manual(`{"orderId": "A-1001", "amount": 180}`)),
					n("approve", flow.TypeApproval, "manager", "Manager approval", 300, 120, flow.ApprovalConfig{Title: "Refund ${{ input.amount }} on {{ input.orderId }}?", TimeoutSeconds: 300}),
					n("refund", flow.TypeHTTP, "refund", "Issue refund", 600, 0, flow.HTTPConfig{Method: "POST", URL: s + "/refunds", Body: `{"orderId": "{{ input.orderId }}", "amount": {{ input.amount }}}`}),
					n("declined", flow.TypeHTTP, "declined", "Tell customer", 600, 140, notify("email:customer", "We couldn't approve this refund.")),
					n("escalate", flow.TypeHTTP, "escalate", "Escalate", 600, 280, notify("#cs-leads", "Refund for {{ input.orderId }} is waiting on a decision.")),
				},
				[]flow.Edge{e("trigger", "approve", ""), e("approve", "refund", flow.HandleApproved), e("approve", "declined", flow.HandleRejected), e("approve", "escalate", flow.HandleTimeout)}),
			DocsURL: docs + "approval", Related: []string{"resumable-activity", "request-response-updates"},
		},

		// ---- Distributed transactions ----
		{
			ID: "saga-pattern", Name: "Saga Pattern", Category: "Distributed transactions", Support: Visual,
			Summary: "Give each step an undo; if a later step fails, undo the completed ones in reverse order.",
			UseFor:  []string{"Bookings, orders, and transfers across services with no shared transaction."},
			Build: []Step{
				{Node: "flight", Text: "On each HTTP step that changes something, switch on Undo and give the compensating request. It can use the step's own output: {{ steps.flight.body.id }}."},
				{Node: "charge", Text: "When a step fails and the run fails, completed steps' undos run newest first."},
				{Text: "Undo runs even if the run is cancelled, and each undo is retried like any other request."},
			},
			Watch: []string{
				"Undo requests must be safe to repeat; a retried cancel shouldn't fail because the booking is already gone.",
				"In a For each body every item's undo joins the run's stack, so a failure after the For each undoes every item. To put one item right when it fails, draw the compensation on its error outcome.",
			},
			TryIt: "Run it: the card always declines, so the hotel is cancelled, then the flight.",
			Flow: def("pattern-saga", "Pattern: saga", "Books a flight and hotel; when the card declines, both are undone.",
				[]flow.Node{
					n("trigger", flow.TypeTrigger, "start", "Book trip", 0, 120, manual(`{"traveler": "C-42", "destination": "Lisbon"}`)),
					n("flight", flow.TypeHTTP, "flight", "Book flight", 300, 120, flow.HTTPConfig{Method: "POST", URL: s + "/bookings/flight", Body: `{"traveler": "{{ input.traveler }}"}`,
						Undo: &flow.HTTPRequestSpec{Method: "POST", URL: s + "/bookings/flight/{{ steps.flight.body.id }}/cancel"}}),
					n("hotel", flow.TypeHTTP, "hotel", "Book hotel", 600, 120, flow.HTTPConfig{Method: "POST", URL: s + "/bookings/hotel", Body: `{"traveler": "{{ input.traveler }}"}`,
						Undo: &flow.HTTPRequestSpec{Method: "POST", URL: s + "/bookings/hotel/{{ steps.hotel.body.id }}/cancel"}}),
					n("charge", flow.TypeHTTP, "charge", "Charge card", 900, 120, flow.HTTPConfig{Method: "POST", URL: s + "/cards/charge?decline=true", Body: `{}`}),
				},
				[]flow.Edge{e("trigger", "flight", ""), e("flight", "hotel", ""), e("hotel", "charge", "")}),
			DocsURL: docs + "saga-pattern", Related: []string{"non-retryable-errors"},
		},
		{
			ID: "early-return", Name: "Early Return", Category: "Distributed transactions", Support: Visual,
			Summary: "Answer the caller as soon as the first part is done, and keep processing in the background.",
			UseFor: []string{
				"APIs that must answer fast (\"order accepted, id 123\") while fulfilment takes minutes.",
				"Webhooks whose sender needs an answer in the HTTP response.",
			},
			Build: []Step{
				{Node: "accepted", Text: "Put a Respond step on the main path. That makes the flow respond early."},
				{Text: "Starting the run (the Run button, or a POST to the webhook) uses Update-With-Start: one call starts the run and waits for the Respond step's value, which comes back in the same response."},
				{Node: "wait", Text: "Everything after Respond carries on in the background, durably."},
				{Node: "rejected", Text: "Several Respond steps on different branches are fine; the first one reached answers."},
			},
			Watch: []string{"A flow that responds early can't also delay its start: the caller is waiting.", "If the run fails or ends before a Respond, the caller gets that error."},
			TryIt: "Run it: the toast and the run view show the response within a second, while the run goes on to wait for shipping.",
			Flow: def("pattern-early-return", "Pattern: early return", "Accepts an order immediately, then waits for it to ship in the background.",
				[]flow.Node{
					n("trigger", flow.TypeTrigger, "start", "Order submitted", 0, 120, flow.TriggerConfig{Kind: "webhook", SampleInput: `{"item": "Espresso grinder", "qty": 1}`}),
					n("valid", flow.TypeCondition, "valid", "Valid order?", 300, 120, flow.ConditionConfig{Expression: "input.qty > 0 && input.qty <= 5"}),
					n("create", flow.TypeHTTP, "create_order", "Create order", 600, 40, flow.HTTPConfig{Method: "POST", URL: s + "/orders", Body: `{"item": "{{ input.item }}", "callbackUrl": "{{ run.callbackUrl }}/events/order_status"}`}),
					n("accepted", flow.TypeRespond, "accepted", "Respond: accepted", 900, 40, flow.RespondConfig{Value: `{"accepted": true, "orderId": "{{ steps.create_order.body.id }}"}`}),
					n("wait", flow.TypeWaitCondition, "shipped", "Wait until shipped", 1200, 40, flow.WaitConditionConfig{SignalName: "order_status", Condition: `events.order_status.status == "shipped"`, TimeoutSeconds: 300}),
					n("email", flow.TypeHTTP, "email", "Email customer", 1500, 40, notify("email:customer", "Your {{ input.item }} has shipped.")),
					n("rejected", flow.TypeRespond, "rejected", "Respond: rejected", 600, 220, flow.RespondConfig{Value: `{"accepted": false, "reason": "quantity must be 1 to 5"}`}),
				},
				[]flow.Edge{e("trigger", "valid", ""), e("valid", "create", flow.HandleTrue), e("valid", "rejected", flow.HandleFalse), e("create", "accepted", ""), e("accepted", "wait", ""), e("wait", "email", "")}),
			DocsURL: docs + "early-return", Related: []string{"early-return-local-activities", "request-response-updates"},
		},

		// ---- Error handling & retries ----
		{
			ID: "fixed-count-retries", Name: "Fixed Count of Retries", Category: "Error handling & retries", Support: Visual,
			Summary: "Cap how many times a step is tried, when each attempt costs money or quota.",
			UseFor:  []string{"Paid APIs, SMS, anything rate-limited by attempts rather than time."},
			Build: []Step{
				{Node: "charge", Text: "Open Retries & timeouts on the step and set Max attempts (here 3). First wait and Backoff space them out."},
				{Node: "charge", Text: "The preview under the settings shows each wait. Temporal schedules every attempt; the flow has no loop."},
				{Node: "failed", Text: "Connect the error outcome to handle the step giving up, instead of failing the run."},
			},
			TryIt: "Run it: the gateway fails every time, so the step shows Attempt 3 of 3, then the alert goes out.",
			Flow: def("pattern-fixed-count", "Pattern: fixed count of retries", "Three attempts at a failing gateway, then an alert.",
				[]flow.Node{
					n("trigger", flow.TypeTrigger, "start", "Charge", 0, 120, manual(`{}`)),
					n("charge", flow.TypeHTTP, "charge", "Charge (3 tries)", 300, 120, flow.HTTPConfig{Method: "POST", URL: s + "/flaky/{{ run.id }}?failures=10", Body: `{}`, Activity: retry(3, 2)}),
					n("done", flow.TypeHTTP, "done", "Confirm", 600, 40, notify("#payments", "Charged.")),
					n("failed", flow.TypeHTTP, "failed", "Alert ops", 600, 200, notify("#payments", "Gave up after 3 attempts: {{ steps.charge.error }}")),
				},
				[]flow.Edge{e("trigger", "charge", ""), e("charge", "done", ""), e("charge", "failed", flow.HandleError)}),
			DocsURL: docs + "fixed-count-retries", Related: []string{"fixed-wall-time-retries", "fast-slow-retries"},
		},
		{
			ID: "fixed-wall-time-retries", Name: "Fixed Wall-Time Retries", Category: "Error handling & retries", Support: Visual,
			Summary: "Retry as often as needed, but give up after a fixed amount of time, to keep an SLA.",
			UseFor:  []string{"Steps with a deadline: a quote valid for 30 seconds, a checkout that must finish in a minute."},
			Build: []Step{
				{Node: "quote", Text: "In Retries & timeouts, set All attempts s (schedule-to-close) to the budget, here 20. Leave Max attempts empty for no count limit."},
				{Node: "quote", Text: "Attempt timeout s bounds each try; All attempts bounds the whole step, waits included."},
				{Node: "expired", Text: "Route the error outcome for when the time runs out."},
			},
			TryIt: "Run it: the service never recovers, so after 20 seconds the step gives up and takes the error outcome.",
			Flow: def("pattern-fixed-wall-time", "Pattern: fixed wall-time retries", "Retries for at most 20 seconds, however many attempts that takes.",
				[]flow.Node{
					n("trigger", flow.TypeTrigger, "start", "Quote requested", 0, 120, manual(`{}`)),
					n("quote", flow.TypeHTTP, "quote", "Get quote (20 s)", 300, 120, flow.HTTPConfig{Method: "POST", URL: s + "/flaky/{{ run.id }}?failures=100", Body: `{}`,
						Activity: flow.ActivityOptions{ScheduleToCloseSeconds: 20, Retry: flow.RetryConfig{MaxAttempts: 0, InitialIntervalSeconds: 2, BackoffCoefficient: 1}}}),
					n("ok", flow.TypeHTTP, "ok", "Show quote", 600, 40, notify("#sales", "Quote ready.")),
					n("expired", flow.TypeHTTP, "expired", "Quote unavailable", 600, 200, notify("#sales", "No quote within 20 seconds: {{ steps.quote.error }}")),
				},
				[]flow.Edge{e("trigger", "quote", ""), e("quote", "ok", ""), e("quote", "expired", flow.HandleError)}),
			DocsURL: docs + "fixed-wall-time-retries", Related: []string{"fixed-count-retries"},
		},
		{
			ID: "non-retryable-errors", Name: "Non-Retryable Errors", Category: "Error handling & retries", Support: Visual,
			Summary: "Fail fast on errors that can never succeed on retry, instead of retrying them.",
			UseFor:  []string{"Declined cards, validation errors, missing records: answers, not outages."},
			Build: []Step{
				{Node: "charge", Text: "HTTP steps already treat most 4xx responses as non-retryable (408 and 429 are retried). A 402 decline fails on the first attempt."},
				{Node: "charge", Text: "Add more statuses under Don't retry these HTTP statuses (here 409), or error types under Don't retry these error types."},
				{Node: "declined", Text: "Route the error outcome to handle it as a business result."},
			},
			TryIt: "Run it: one attempt, then the declined branch, with no retries in between.",
			Flow: def("pattern-non-retryable", "Pattern: non-retryable errors", "A declined card fails at once and takes the error branch.",
				[]flow.Node{
					n("trigger", flow.TypeTrigger, "start", "Checkout", 0, 120, manual(`{}`)),
					n("charge", flow.TypeHTTP, "charge", "Charge card", 300, 120, flow.HTTPConfig{Method: "POST", URL: s + "/cards/charge?decline=true", Body: `{}`,
						Activity: flow.ActivityOptions{Retry: flow.RetryConfig{MaxAttempts: 10, NonRetryableStatusCodes: []int{409}}}}),
					n("ok", flow.TypeHTTP, "ok", "Confirm", 600, 40, notify("email:customer", "Payment received.")),
					n("declined", flow.TypeHTTP, "declined", "Ask for another card", 600, 200, notify("email:customer", "Your card was declined: {{ steps.charge.error }}")),
				},
				[]flow.Edge{e("trigger", "charge", ""), e("charge", "ok", ""), e("charge", "declined", flow.HandleError)}),
			DocsURL: docs + "non-retryable-errors", Related: []string{"fixed-count-retries", "resumable-activity"},
		},
		{
			ID: "delayed-retry", Name: "Delayed Retry", Category: "Error handling & retries", Support: BuiltIn,
			Summary: "Let the failure decide when to try again, overriding the retry policy's backoff.",
			UseFor:  []string{"APIs that say when to come back: 429 or 503 with a Retry-After header."},
			Build: []Step{
				{Text: "Nothing to configure: when a response has a Retry-After header (seconds or a date), the HTTP step sets the next retry's delay to it, up to 10 minutes. The retry policy still decides how many attempts."},
				{Node: "call", Text: "The step's last failure says how long it is waiting, as asked."},
			},
			TryIt: "Run it: the API asks to wait 8 seconds, twice; the first wait setting is 1 second, but each retry waits 8.",
			Flow: def("pattern-delayed-retry", "Pattern: delayed retry", "A rate-limited API that sets its own retry delay with Retry-After.",
				[]flow.Node{
					n("trigger", flow.TypeTrigger, "start", "Sync", 0, 120, manual(`{}`)),
					n("call", flow.TypeHTTP, "call", "Call rate-limited API", 300, 120, flow.HTTPConfig{Method: "POST", URL: s + "/ratelimited/{{ run.id }}?times=2&after=8", Body: `{}`, Activity: retry(5, 1)}),
					n("done", flow.TypeHTTP, "done", "Done", 600, 120, notify("#ops", "Synced after the API's requested waits.")),
				},
				[]flow.Edge{e("trigger", "call", ""), e("call", "done", "")}),
			DocsURL: docs + "delayed-retry", Related: []string{"fast-slow-retries"},
		},
		{
			ID: "fast-slow-retries", Name: "Fast/Slow Retries", Category: "Error handling & retries", Support: Visual,
			Summary: "Retry quickly a few times for blips, then slowly and without limit to outlast a long outage.",
			UseFor:  []string{"Dependencies that usually recover in seconds but occasionally go down for hours."},
			Build: []Step{
				{Node: "sync", Text: "In Retries & timeouts, set a small Max attempts and short First wait: the fast phase."},
				{Node: "sync", Text: "Switch on Then retry slowly and set Every s; leave Slow attempts empty for unlimited."},
				{Text: "When the fast attempts are used up, a second Activity with the slow policy takes over. Both appear in history; neither the flow nor the Activity loops."},
			},
			TryIt: "Run it: three quick failures, then the step notes it's retrying slowly and succeeds on a slow attempt about 20 seconds later.",
			Flow: def("pattern-fast-slow", "Pattern: fast/slow retries", "Three quick tries, then a retry every 10 seconds.",
				[]flow.Node{
					n("trigger", flow.TypeTrigger, "start", "Sync", 0, 120, manual(`{}`)),
					n("sync", flow.TypeHTTP, "sync", "Sync inventory", 300, 120, flow.HTTPConfig{Method: "POST", URL: s + "/flaky/{{ run.id }}?failures=5", Body: `{}`,
						Activity: flow.ActivityOptions{Retry: flow.RetryConfig{MaxAttempts: 3, InitialIntervalSeconds: 1, BackoffCoefficient: 1}, SlowRetry: &flow.RetryConfig{InitialIntervalSeconds: 10, BackoffCoefficient: 1}}}),
					n("done", flow.TypeHTTP, "done", "Done", 600, 120, notify("#ops", "Inventory synced.")),
				},
				[]flow.Edge{e("trigger", "sync", ""), e("sync", "done", "")}),
			DocsURL: docs + "fast-slow-retries", Related: []string{"delayed-retry", "fixed-count-retries"},
		},
		{
			ID: "retry-metrics", Name: "Retry Alerting via Metrics", Category: "Error handling & retries", Support: NotInBuilder,
			Summary: "Emit a metric when retries pass a threshold, so people act before an SLA is missed.",
			UseFor:  []string{"Catching a dependency that's quietly failing while retries hide it."},
			Build: []Step{
				{Text: "This lives in the worker, not in a flow: the Temporal SDK's metrics (activity attempt counts, schedule-to-start latency) go to your metrics system, and alerts are set there."},
				{Text: "In the builder you can see the same thing per run: a retrying step turns pink and shows Attempt n of m with the last failure, and Executions filters by status."},
				{Text: "To alert from a flow, route the step's error outcome to a notification, as in Fixed Count of Retries."},
			},
			DocsURL: docs + "retry-metrics", Related: []string{"fixed-count-retries"},
		},
		{
			ID: "resumable-activity", Name: "Resumable Activity", Category: "Error handling & retries", Support: Visual,
			Summary: "When retries run out, pause the run until a person corrects the input, then carry on from that step.",
			UseFor:  []string{"Bad data that only a person can fix: an invalid address, a wrong account number."},
			Build: []Step{
				{Node: "validate", Text: "On the HTTP step, switch on Pause for a fix when retries run out."},
				{Text: "When it gives up, the run waits (for free) and the step's inspector shows the error and the request, with the URL and body editable."},
				{Text: "Retry sends a fix_step Update; the step runs again with its full retry policy. Let it fail takes the error outcome."},
			},
			Watch: []string{"The corrected request is in the run's history, so there's a record of what was changed and by whom."},
			TryIt: "Run it: the ZIP 0000 is rejected. Click the paused step, change the ZIP to 94107, and Retry.",
			Flow: def("pattern-resumable", "Pattern: resumable activity", "An address check that pauses for a person to fix the ZIP.",
				[]flow.Node{
					n("trigger", flow.TypeTrigger, "start", "Ship order", 0, 120, manual(`{"street": "1 Market St", "zip": "0000"}`)),
					n("validate", flow.TypeHTTP, "validate", "Validate address", 300, 120, flow.HTTPConfig{Method: "POST", URL: s + "/addresses/validate", Body: `{"street": "{{ input.street }}", "zip": "{{ input.zip }}"}`, PauseForFix: true}),
					n("ship", flow.TypeHTTP, "ship", "Create label", 600, 120, notify("#warehouse", "Label for zone {{ steps.validate.body.zone }}.")),
				},
				[]flow.Edge{e("trigger", "validate", ""), e("validate", "ship", "")}),
			DocsURL: docs + "resumable-activity", Related: []string{"approval", "non-retryable-errors"},
		},

		// ---- Batch processing ----
		{
			ID: "fanout-child-workflows", Name: "Fan-Out with Child Workflows", Category: "Batch processing", Support: Visual,
			Summary: "Run a flow once per record, as parallel child Workflows, and collect the results.",
			UseFor:  []string{"Per-customer or per-order work over a list: notifications, migrations, recalculations."},
			Build: []Step{
				{Node: "each", Text: "Add a For each step. Items is an expression returning a list: input.customers."},
				{Node: "each", Text: "Choose the flow to run per item; its input template sees {{ item }} and {{ index }}."},
				{Node: "each", Text: "Finish when: every child (default), the first to succeed, or a number of them. The output is every result, or the winners."},
			},
			Watch: []string{
				"Each child has its own history; the parent records a few events per child, so a long list continues as new part way, letting running children finish and starting the rest in the next run.",
				"One For each runs up to 10,000 items (results are kept for up to 200; beyond that the output has counts). Larger sets page through a Loop.",
				"No second flow needed when the work per item is a few steps: a For each can run the steps on its body outcome inside this run (see Sliding Window). Children suit bigger work per item, since each keeps its own history.",
			},
			TryIt: "Run it: five children run at once, and the step counts them done.",
			Flow: def("pattern-fanout", "Pattern: fan-out with child workflows", "Notifies each customer through a child flow.",
				[]flow.Node{
					n("trigger", flow.TypeTrigger, "start", "Campaign", 0, 120, manual(`{"customers": ["C-1", "C-2", "C-3", "C-4", "C-5", "C-6"]}`)),
					n("each", flow.TypeForEach, "notify_all", "Notify each", 300, 120, flow.ForEachConfig{Items: "input.customers", FlowID: "pattern-notify-one", Input: `{"customer": "{{ item }}"}`, Concurrency: 5}),
					n("report", flow.TypeHTTP, "report", "Report", 600, 120, notify("#marketing", "Sent {{ steps.notify_all.count }} notifications.")),
				},
				[]flow.Edge{e("trigger", "each", ""), e("each", "report", "")}),
			Children: []flow.Definition{notifyOne(s, notify)},
			DocsURL:  docs + "fanout-child-workflows", Related: []string{"sliding-window", "child-workflows", "batch-iterator"},
		},
		{
			ID: "batch-iterator", Name: "Batch Iterator", Category: "Batch processing", Support: Visual,
			Summary: "Page through a dataset with a cursor, one page per pass, keeping exactly-once progress.",
			UseFor:  []string{"Datasets too big to load at once: exports, backfills, reconciliations."},
			Build: []Step{
				{Text: "Declare variables for the cursor and the running totals in flow settings."},
				{Node: "pages", Text: "Add a Loop with Repeat until vars.cursor == \"\" (the API returns no next cursor on the last page)."},
				{Node: "page", Text: "In the body, fetch a page using the cursor: /records?cursor={{ vars.cursor }}."},
				{Node: "tally", Text: "Set variable stores the next cursor and adds up the page; the last step leads back into the loop."},
			},
			Watch: []string{
				"The cursor is in the run's state: if a worker dies mid-page, the run resumes at that page, never skipping or repeating one.",
				"However many pages there are, the run continues as new when Temporal suggests it, part way through the loop; the next run carries on from the same cursor.",
				"To process a page's records in parallel, put a For each that runs steps in the loop body: each pass fans that page out.",
			},
			TryIt: "Run it: five passes, one per page, and the total ends at 25 records.",
			Flow: withVars(def("pattern-batch-iterator", "Pattern: batch iterator", "Pages through 25 records, five at a time.",
				[]flow.Node{
					n("trigger", flow.TypeTrigger, "start", "Backfill", 0, 120, manual(`{}`)),
					n("pages", flow.TypeLoop, "pages", "Each page", 300, 120, flow.LoopConfig{Until: `vars.cursor == ""`, MaxPasses: 100}),
					n("page", flow.TypeHTTP, "page", "Fetch page", 300, 300, flow.HTTPConfig{Method: "GET", URL: s + "/records?cursor={{ vars.cursor }}"}),
					n("tally", flow.TypeSetVariable, "tally", "Next cursor", 600, 300, flow.SetVariableConfig{Assignments: []flow.Assignment{
						{Name: "cursor", Value: "{{ steps.page.body.next }}"}, {Name: "records", Value: "{{ vars.records + len(steps.page.body.items) }}"},
					}}),
					n("done", flow.TypeHTTP, "done", "Report", 600, 120, notify("#data", "Backfilled {{ vars.records }} records in {{ steps.pages.passes }} pages.")),
				},
				[]flow.Edge{e("trigger", "pages", ""), e("pages", "page", flow.HandleBody), e("page", "tally", ""), e("tally", "pages", ""), e("pages", "done", flow.HandleDone)}),
				flow.VariableDef{Name: "cursor", Initial: `""`, Visible: true}, flow.VariableDef{Name: "records", Initial: "0", Visible: true}),
			DocsURL: docs + "batch-iterator", Related: []string{"polling", "fanout-child-workflows", "continue-as-new"},
		},
		{
			ID: "sliding-window", Name: "Sliding Window", Category: "Batch processing", Support: Visual,
			Summary: "Keep a fixed number of items in flight, starting the next as each one finishes.",
			UseFor:  []string{"Large fan-outs that must not overwhelm a downstream system.", "Running a few steps per item of a list without a child Workflow per item."},
			Build: []Step{
				{Node: "each", Text: "Add a For each step, set Run for each item to the steps on its body outcome, and set At most at once to the window size (here 2)."},
				{Node: "work", Text: "Draw the steps for one item on the body outcome; they see {{ item }} and {{ index }}. Connect the last back into the For each."},
				{Text: "As each item finishes, the next starts, so exactly two run at any moment until the list is done. The output has each item's step outputs: steps.jobs.results[0].work."},
			},
			Watch: []string{
				"The body runs inside this run, so its Activities are in this run's history. A long list continues as new part way: running items finish and the rest start in the next run.",
				"A body step's card sums up every item (\"4 done · 2 running\"); pick an item in the inspector to see its own steps.",
				"Approvals and fixes in a body wait per item; pick the item in the run view to decide it. A Wait for signal there needs a match (event.orderId == item.id) to say which events are whose.",
			},
			TryIt: "Run it: six 10-second jobs two at a time take about 30 seconds; watch the job step's count go up.",
			Flow: def("pattern-sliding-window", "Pattern: sliding window", "Six jobs, two at a time, inside the run.",
				[]flow.Node{
					n("trigger", flow.TypeTrigger, "start", "Process", 0, 120, manual(`{"jobs": [1, 2, 3, 4, 5, 6]}`)),
					n("each", flow.TypeForEach, "jobs", "Two at a time", 300, 120, flow.ForEachConfig{Run: flow.ForEachRunSteps, Items: "input.jobs", Concurrency: 2}),
					n("work", flow.TypeHTTP, "work", "Run job (10 s)", 300, 300, flow.HTTPConfig{Method: "POST", URL: s + "/jobs/slow?seconds=10", Body: `{"job": {{ item }}}`}),
					n("done", flow.TypeHTTP, "done", "Report", 600, 120, notify("#ops", "Ran {{ steps.jobs.succeeded }} jobs.")),
				},
				[]flow.Edge{e("trigger", "each", ""), e("each", "work", flow.HandleBody), e("work", "each", ""), e("each", "done", "")}),
			DocsURL: docs + "sliding-window", Related: []string{"fanout-child-workflows", "downstream-rate-limiting"},
		},
		{
			ID: "mapreduce-tree", Name: "MapReduce Tree", Category: "Batch processing", Support: NotInBuilder,
			Summary: "Split data into a tree of child Workflows, process the leaves in parallel, and aggregate upward.",
			UseFor:  []string{"Very large datasets where one parent can't hold every child."},
			Build: []Step{
				{Text: "Not supported as a pattern: it needs a flow that splits its input and runs itself recursively, and the builder doesn't design for recursion."},
				{Text: "For each covers one level of fan-out, as child flows or as steps run inside the run; a Loop that pages and fans out per page covers much larger sets."},
			},
			DocsURL: docs + "mapreduce-tree", Related: []string{"fanout-child-workflows", "batch-iterator"},
		},

		// ---- QoS & throughput ----
		{
			ID: "downstream-rate-limiting", Name: "Downstream Rate Limiting", Category: "QoS & throughput", Support: Visual,
			Summary: "Cap how fast a flow calls a downstream service, so it stays under the service's quota.",
			UseFor:  []string{"Protecting a fragile or quota-limited dependency.", "Bulk calls to an API that answers 429 when called too fast."},
			Build: []Step{
				{Node: "each", Text: "Add a For each and set Start at most to the service's rate (here 5 per second). Items start no faster than that, spaced out by durable timers, however much room the window has."},
				{Node: "call", Text: "The body calls the service once per item. It answers 429 beyond 5 calls a second, which never happens here."},
				{Text: "Without the start rate, ten at once would trip the quota. The 429s would be retried after Retry-After (Delayed Retry), which works, but each one costs a failed attempt."},
			},
			Watch: []string{
				"The start rate applies to this step in this run. For a cap shared by every run, pick a Shared rate limit on the HTTP step: its requests go to that limit's task queue, which Temporal dispatches no faster than the rate across all runs and workers. Shared limits are deployment configuration (RATE_LIMITS; make dev has metered=5/s).",
				"At most at once limits how many calls run concurrently; Start at most limits how often they start. A slow service may need both.",
			},
			TryIt: "Run it: twelve calls take about two seconds, with no 429s. Set Start at most off and run it again to see retries.",
			Flow: def("pattern-rate-limit", "Pattern: downstream rate limiting", "Twelve calls to a metered API, five a second.",
				[]flow.Node{
					n("trigger", flow.TypeTrigger, "start", "Sync", 0, 120, manual(`{"records": [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12]}`)),
					n("each", flow.TypeForEach, "sync", "Five a second", 300, 120, flow.ForEachConfig{Run: flow.ForEachRunSteps, Items: "input.records", Concurrency: 10,
						Rate: &flow.RateLimit{Count: 5, Per: "second"}}),
					n("call", flow.TypeHTTP, "call", "Call metered API", 300, 300, flow.HTTPConfig{Method: "POST", URL: s + "/metered/{{ run.id }}?perSecond=5", Body: `{"record": {{ item }}}`}),
					n("done", flow.TypeHTTP, "done", "Report", 600, 120, notify("#ops", "Synced {{ steps.sync.succeeded }} records.")),
				},
				[]flow.Edge{e("trigger", "each", ""), e("each", "call", flow.HandleBody), e("call", "each", ""), e("each", "done", "")}),
			DocsURL: docs + "downstream-rate-limiting", Related: []string{"sliding-window", "delayed-retry"},
		},
		{
			ID: "priority", Name: "Priority", Category: "QoS & throughput", Support: Visual,
			Summary: "When a task queue is backed up, dispatch time-sensitive work first.",
			UseFor:  []string{"Interactive flows that share workers with batch jobs."},
			Build: []Step{
				{Text: "Open flow settings and pick Priority 1 (first) to 5 (last); 3 is the default."},
				{Text: "Runs start with that priority, and their Activities and child Workflows inherit it. It only matters when tasks are queued; an idle queue serves everything at once."},
			},
			Watch: []string{"Task queue priority is a newer Temporal feature; the server must support it for the ordering to apply."},
			TryIt: "Open the example's flow settings to see Priority 1.",
			Flow: withPriority(def("pattern-priority", "Pattern: priority", "An interactive lookup that runs ahead of batch work.",
				[]flow.Node{
					n("trigger", flow.TypeTrigger, "start", "Customer waiting", 0, 120, manual(`{"customerId": "C-42"}`)),
					n("lookup", flow.TypeHTTP, "lookup", "Load customer", 300, 120, flow.HTTPConfig{Method: "GET", URL: s + "/customers/{{ input.customerId }}"}),
				},
				[]flow.Edge{e("trigger", "lookup", "")}), 1),
			DocsURL: docs + "priority-task-queues", Related: []string{"fairness"},
		},
		{
			ID: "fairness", Name: "Fairness", Category: "QoS & throughput", Support: BuiltIn,
			Summary: "Share task dispatch between tenants, so one tenant's burst doesn't starve the others.",
			UseFor:  []string{"Multi-tenant platforms where everyone shares workers."},
			Build: []Step{
				{Text: "Nothing to draw: every run is started with a fairness key of its user, so a user who starts 200 children takes turns with everyone else rather than going first."},
				{Text: "Activities and child Workflows inherit the key from their run."},
			},
			Watch:   []string{"Fairness needs server support and is enabled per namespace; where it isn't, the key is ignored."},
			DocsURL: docs + "fairness", Related: []string{"priority"},
		},

		// ---- Performance & latency ----
		{
			ID: "local-activities", Name: "Local Activities", Category: "Performance & latency", Support: BuiltIn,
			Summary: "Run short, idempotent work inside the worker, skipping the server round trip.",
			UseFor:  []string{"Small computations and validations between real calls."},
			Build: []Step{
				{Text: "Transform, Condition, and Set variable steps run as deterministic Workflow code inside the worker: no Activity and no round trip at all, which is cheaper still than a Local Activity."},
				{Text: "Anything with I/O is an HTTP step, a real Activity, so it gets retries, timeouts, and heartbeats."},
			},
			DocsURL: docs + "local-activities", Related: []string{"early-return-local-activities"},
		},
		{
			ID: "early-return-local-activities", Name: "Early Return + Local Activities", Category: "Performance & latency", Support: Visual,
			Summary: "Answer the caller from in-process work only, then do the slow work after responding.",
			UseFor:  []string{"APIs that need a fast first answer computed from the request itself."},
			Build: []Step{
				{Node: "price", Text: "Compute the answer with in-Workflow steps (Condition, Transform): no Activities before the Respond."},
				{Node: "reply", Text: "Respond on the main path answers the caller through Update-With-Start."},
				{Node: "save", Text: "Calls out come after the Respond, in the background."},
			},
			TryIt: "Run it: the price comes back immediately, before the order is saved.",
			Flow: def("pattern-early-return-local", "Pattern: early return + local work", "Prices the order in-process, answers, then saves it.",
				[]flow.Node{
					n("trigger", flow.TypeTrigger, "start", "Price order", 0, 120, flow.TriggerConfig{Kind: "webhook", SampleInput: `{"qty": 3, "unit": 12.5}`}),
					n("price", flow.TypeTransform, "price", "Price it", 300, 120, flow.TransformConfig{Output: `{"total": "{{ input.qty * input.unit }}"}`}),
					n("reply", flow.TypeRespond, "reply", "Respond", 600, 120, flow.RespondConfig{Value: `{"total": "{{ steps.price.total }}"}`}),
					n("save", flow.TypeHTTP, "save", "Save order", 900, 120, flow.HTTPConfig{Method: "POST", URL: s + "/orders", Body: `{"item": "order of {{ input.qty }}"}`}),
				},
				[]flow.Edge{e("trigger", "price", ""), e("price", "reply", ""), e("reply", "save", "")}),
			DocsURL: docs + "early-return-local-activities", Related: []string{"early-return", "local-activities"},
		},
		{
			ID: "eager-workflow-start", Name: "Eager Workflow Start", Category: "Performance & latency", Support: NotInBuilder,
			Summary: "Hand the first Workflow Task straight to a co-located worker to cut start latency.",
			UseFor:  []string{"Latency-critical starts where the starter and a worker share a process."},
			Build: []Step{
				{Text: "A starter and worker deployment choice, not a flow's. Here the app starts runs and serverless workers run them, so they are never in the same process."},
				{Text: "For a fast answer to the caller, use Early Return."},
			},
			DocsURL: docs + "eager-workflow-start", Related: []string{"early-return"},
		},

		// ---- Worker configuration ----
		{
			ID: "worker-specific-taskqueue", Name: "Worker-Specific Task Queues", Category: "Worker configuration", Support: NotInBuilder,
			Summary: "Route Activities to one particular worker, for host affinity such as files on a local disk.",
			UseFor:  []string{"Multi-step file processing on one machine."},
			Build: []Step{
				{Text: "A worker topology choice: each worker listens on its own queue as well as the shared one. Serverless workers have no lasting host to pin work to, so flows here don't route by worker."},
				{Text: "Pass data between steps through their outputs, or through object storage referenced by URL."},
			},
			DocsURL: docs + "worker-specific-taskqueue",
		},
		{
			ID: "activity-dependency-injection", Name: "Activity Dependency Injection", Category: "Worker configuration", Support: BuiltIn,
			Summary: "Give Activities their clients and configuration at worker start, keeping Workflows deterministic and Activities testable.",
			UseFor:  []string{"Every worker."},
			Build: []Step{
				{Text: "Already how the platform works: the worker builds one Activities value holding the HTTP client and the Claude client, and registers its methods. Flows never see a client."},
				{Text: "Tests register the same Activities with a test HTTP server, which is how this project's Workflow tests run without a network."},
			},
			DocsURL: docs + "activity-dependency-injection",
		},
	}
}

// quote asks one carrier for a price; two quick tries, so a carrier that is
// down drops out of the race in seconds.
func quote(sandbox, carrier string) flow.HTTPConfig {
	return flow.HTTPConfig{
		Method: "POST", URL: sandbox + "/quotes/" + carrier,
		Body:     `{"orderId": "{{ input.orderId }}", "weightKg": {{ input.weightKg }}}`,
		Activity: flow.ActivityOptions{Retry: flow.RetryConfig{MaxAttempts: 2, InitialIntervalSeconds: 1, BackoffCoefficient: 1}},
	}
}

func notifyOne(s string, notify func(string, string) flow.HTTPConfig) flow.Definition {
	return *def("pattern-notify-one", "Pattern: notify one customer", "The child: notify one customer.",
		[]flow.Node{
			n("trigger", flow.TypeTrigger, "start", "Notify", 0, 120, flow.TriggerConfig{Kind: "manual", SampleInput: `{"customer": "C-1"}`}),
			n("send", flow.TypeHTTP, "send", "Send", 300, 120, notify("email:{{ input.customer }}", "Our spring sale starts today.")),
		},
		[]flow.Edge{e("trigger", "send", "")})
}

func def(id, name, description string, nodes []flow.Node, edges []flow.Edge) *flow.Definition {
	return &flow.Definition{ID: id, Name: name, Description: description, Version: 1, Nodes: nodes, Edges: edges}
}

// entity is a keyed flow whose run outlives its main path.
func entity(d *flow.Definition, by string, vars ...flow.VariableDef) *flow.Definition {
	d = keyed(d, by, vars...)
	d.Settings.Entity = true
	return d
}

func keyed(d *flow.Definition, by string, vars ...flow.VariableDef) *flow.Definition {
	d.Settings.KeyedBy = by
	d.Settings.Variables = vars
	return d
}

func withVars(d *flow.Definition, vars ...flow.VariableDef) *flow.Definition {
	d.Settings.Variables = vars
	return d
}

func withPriority(d *flow.Definition, p int) *flow.Definition {
	d.Settings.Priority = p
	return d
}

func n(id string, t flow.NodeType, key, label string, x, y float64, config any) flow.Node {
	raw, err := json.Marshal(config)
	if err != nil {
		panic(err)
	}
	return flow.Node{ID: id, Type: t, Key: key, Label: label, Position: flow.Position{X: x, Y: y}, Config: raw}
}

func e(from, to, handle string) flow.Edge {
	id := from + "-" + to
	if handle != "" {
		id += "-" + handle
	}
	return flow.Edge{ID: id, Source: from, Target: to, SourceHandle: handle}
}
