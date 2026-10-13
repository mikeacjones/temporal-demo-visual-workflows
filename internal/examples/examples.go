// Package examples holds the flows every new session starts with, which are
// also the built-in templates. Each one is built around a Temporal behavior a
// presenter can show.
package examples

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
)

// Flows returns the example flows, pointing at the sandbox service, as if
// saved at now.
func Flows(sandboxURL string, now time.Time) map[string]flow.Definition {
	s := strings.TrimRight(sandboxURL, "/")
	notify := func(channel, text string) flow.HTTPConfig {
		body, _ := json.Marshal(map[string]string{"channel": channel, "text": text})
		return flow.HTTPConfig{Method: "POST", URL: s + "/notify", Body: string(body)}
	}

	list := []flow.Definition{
		{
			ID:          "order-shipped",
			Name:        "Order shipped notification",
			Description: "Places an order and hands the store this run's callback URL. Each status change comes back as an order_status Signal; the run sleeps until one says shipped, then emails the customer.",
			Nodes: []flow.Node{
				n("trigger", flow.TypeTrigger, "start", "Manual start", 0, 120, flow.TriggerConfig{Kind: "manual", SampleInput: `{"item": "Espresso grinder", "customer": "C-42"}`}),
				n("create", flow.TypeHTTP, "create_order", "Create order", 300, 120, flow.HTTPConfig{
					Method: "POST", URL: s + "/orders",
					// The store calls this URL on every status change; the app
					// turns each call into an order_status Signal on this run.
					Body: `{"item": "{{ input.item }}", "customer": "{{ input.customer }}", "callbackUrl": "{{ run.callbackUrl }}/events/order_status"}`,
				}),
				n("wait", flow.TypeWaitCondition, "shipped", "Wait until shipped", 600, 120, flow.WaitConditionConfig{
					SignalName: "order_status", Condition: `events.order_status.status == "shipped"`, TimeoutSeconds: 300,
				}),
				n("email", flow.TypeHTTP, "email_customer", "Email customer", 900, 40,
					notify("email:{{ input.customer }}", "Good news! Your {{ input.item }} has shipped (order {{ steps.create_order.body.id }}).")),
				n("late", flow.TypeHTTP, "alert_ops", "Alert ops", 900, 220,
					notify("#warehouse", "Order {{ steps.create_order.body.id }} has not shipped within 5 minutes.")),
			},
			Edges: []flow.Edge{
				e("trigger", "create", ""), e("create", "wait", ""),
				e("wait", "email", ""), e("wait", "late", flow.HandleTimeout),
			},
		},
		{
			ID:          "payment-capture",
			Name:        "Resilient payment capture",
			Description: "Charges through a flaky gateway (Temporal retries with the step's retry policy until it succeeds), then waits for the bank's settlement callback, which arrives as a payment_settled Signal.",
			Nodes: []flow.Node{
				n("trigger", flow.TypeTrigger, "start", "Manual start", 0, 120, flow.TriggerConfig{Kind: "manual", SampleInput: `{"orderId": "A-1001", "amount": 129}`}),
				n("charge", flow.TypeHTTP, "charge", "Charge card", 300, 120, flow.HTTPConfig{
					Method: "POST", URL: s + "/flaky/{{ run.id }}?failures=3",
					Body: `{"orderId": "{{ input.orderId }}", "amount": {{ input.amount }}, "callbackUrl": "{{ run.callbackUrl }}/events/payment_settled"}`,
					Activity: flow.ActivityOptions{Retry: flow.RetryConfig{
						MaxAttempts: 6, InitialIntervalSeconds: 2, BackoffCoefficient: 1.5, MaximumIntervalSeconds: 10,
					}},
				}),
				n("settle", flow.TypeWaitSignal, "settlement", "Wait for settlement", 600, 60, flow.WaitSignalConfig{SignalName: "payment_settled", TimeoutSeconds: 600}),
				n("receipt", flow.TypeTransform, "receipt", "Build receipt", 900, 0, flow.TransformConfig{
					Output: `{"orderId": "{{ input.orderId }}", "transactionId": "{{ steps.charge.body.transactionId }}", "settlement": "{{ steps.settlement.payload }}", "gatewayAttempts": "{{ steps.charge.body.attempt }}"}`,
				}),
				n("done", flow.TypeHTTP, "notify_finance", "Notify finance", 1200, 0,
					notify("#payments", "Captured {{ input.orderId }} ({{ steps.receipt.transactionId }}) after {{ steps.receipt.gatewayAttempts }} gateway attempts.")),
				n("unsettled", flow.TypeHTTP, "flag_unsettled", "Flag unsettled", 900, 160,
					notify("#payments", "{{ input.orderId }} charged but not settled within 10 minutes.")),
				n("failed", flow.TypeHTTP, "alert_ops", "Alert ops", 600, 260,
					notify("#ops", "Charging {{ input.orderId }} failed: {{ steps.charge.error }}")),
			},
			Edges: []flow.Edge{
				e("trigger", "charge", ""), e("charge", "settle", ""), e("charge", "failed", flow.HandleError),
				e("settle", "receipt", flow.HandleReceived), e("settle", "unsettled", flow.HandleTimeout),
				e("receipt", "done", ""),
			},
		},
		{
			ID:          "refund-approval",
			Name:        "Refund approval",
			Description: "Webhook-triggered refunds. Small refunds go straight through; large ones wait for a manager's decision (an Update) with a timeout that escalates rather than declining.",
			Nodes: []flow.Node{
				n("trigger", flow.TypeTrigger, "start", "Refund requested", 0, 140, flow.TriggerConfig{Kind: "webhook", SampleInput: `{"orderId": "A-1001", "amount": 180, "reason": "Arrived damaged"}`}),
				n("check", flow.TypeCondition, "needs_review", "Over $100?", 300, 140, flow.ConditionConfig{Expression: "input.amount > 100"}),
				n("approve", flow.TypeApproval, "manager", "Manager approval", 600, 40, flow.ApprovalConfig{
					Title:   "Refund ${{ input.amount }} on order {{ input.orderId }}?",
					Details: "Customer reason: {{ input.reason }}", TimeoutSeconds: 300,
				}),
				n("refund", flow.TypeHTTP, "issue_refund", "Issue refund", 950, 160, flow.HTTPConfig{
					Method: "POST", URL: s + "/refunds",
					// The Workflow ID is stable across retries, so a retried
					// refund is recognised and not paid twice.
					Headers: map[string]string{"Idempotency-Key": "{{ run.id }}-issue_refund"},
					Body:    `{"orderId": "{{ input.orderId }}", "amount": {{ input.amount }}, "reason": "{{ input.reason }}"}`,
				}),
				n("confirm", flow.TypeHTTP, "confirm", "Confirm to customer", 1250, 160,
					notify("email:customer", "Your refund {{ steps.issue_refund.body.refundId }} for ${{ input.amount }} is on its way.")),
				n("declined", flow.TypeHTTP, "declined", "Tell customer no", 950, 0,
					notify("email:customer", "We couldn't approve a refund for order {{ input.orderId }}. A specialist will be in touch.")),
				n("escalate", flow.TypeHTTP, "escalate", "Escalate", 950, -120,
					notify("#cs-leads", "Refund for {{ input.orderId }} has waited 5 minutes for a decision.")),
			},
			Edges: []flow.Edge{
				e("trigger", "check", ""),
				e("check", "approve", flow.HandleTrue), e("check", "refund", flow.HandleFalse),
				e("approve", "refund", flow.HandleApproved), e("approve", "declined", flow.HandleRejected),
				e("approve", "escalate", flow.HandleTimeout),
				e("refund", "confirm", ""),
			},
		},
		{
			ID:          "editable-order",
			Name:        "Editable order",
			Description: "An order with a hold window you can change while it runs. Signal lanes change the address or cancel; an Update extends the hold and answers with the new length; the hold timer recalculates, and a First of race ships or cancels.",
			Settings: flow.FlowSettings{
				Direction: "TB",
				Variables: []flow.VariableDef{
					{Name: "address", Initial: `""`, Visible: true},
					{Name: "hold_minutes", Initial: `2`, Visible: true},
					{Name: "cancelled", Initial: `false`, Visible: true},
				},
			},
			Nodes: []flow.Node{
				n("trigger", flow.TypeTrigger, "start", "Order placed", 0, 0, flow.TriggerConfig{Kind: "manual", SampleInput: `{"orderId": "A-7001", "address": "1 Main St, Portland"}`}),
				n("init", flow.TypeSetVariable, "init", "Remember address", 0, 160, flow.SetVariableConfig{Assignments: []flow.Assignment{{Name: "address", Value: "{{ input.address }}"}}}),
				n("hold", flow.TypeDelay, "hold", "Hold window", -170, 320, flow.DelayConfig{Mode: "expression", Expression: "vars.hold_minutes * 60"}),
				n("cancel_wait", flow.TypeWaitCondition, "cancel_requested", "Cancel requested?", 170, 320, flow.WaitConditionConfig{Condition: "vars.cancelled == true"}),
				n("race", flow.TypeFirstOf, "outcome", "Hold ends or cancel", 0, 480, flow.FirstOfConfig{}),
				n("which", flow.TypeCondition, "was_cancelled", "Cancelled?", 0, 640, flow.ConditionConfig{Expression: `steps.outcome.winner == "cancel_requested"`}),
				n("ship", flow.TypeHTTP, "ship", "Ship order", -170, 800, notify("#warehouse", "Ship {{ input.orderId }} to {{ vars.address }}")),
				n("cancelled", flow.TypeHTTP, "confirm_cancel", "Confirm cancellation", 170, 800, notify("email:customer", "Order {{ input.orderId }} is cancelled. Nothing was shipped.")),

				n("on_address", flow.TypeOnSignal, "change_address", "On change_address", 560, 0, flow.OnSignalConfig{SignalName: "change_address"}),
				n("set_address", flow.TypeSetVariable, "new_address", "Update address", 560, 160, flow.SetVariableConfig{Assignments: []flow.Assignment{{Name: "address", Value: "{{ steps.change_address.address }}"}}}),
				n("on_cancel", flow.TypeOnSignal, "cancel_order", "On cancel_order", 860, 0, flow.OnSignalConfig{SignalName: "cancel_order"}),
				n("set_cancel", flow.TypeSetVariable, "mark_cancelled", "Mark cancelled", 860, 160, flow.SetVariableConfig{Assignments: []flow.Assignment{{Name: "cancelled", Value: "{{ true }}"}}}),
				n("on_extend", flow.TypeOnUpdate, "extend_hold", "On extend_hold", 1160, 0, flow.OnUpdateConfig{
					UpdateName: "extend_hold", Validator: "request.minutes > 0 && request.minutes <= 60 && !vars.cancelled",
				}),
				n("set_hold", flow.TypeSetVariable, "longer_hold", "Extend hold", 1160, 160, flow.SetVariableConfig{Assignments: []flow.Assignment{{Name: "hold_minutes", Value: "{{ vars.hold_minutes + steps.extend_hold.minutes }}"}}}),
				n("reply", flow.TypeRespond, "reply", "Respond", 1160, 320, flow.RespondConfig{Value: `{"holdMinutes": "{{ vars.hold_minutes }}", "address": "{{ vars.address }}"}`}),
			},
			Edges: []flow.Edge{
				e("trigger", "init", ""), e("init", "hold", ""), e("init", "cancel_wait", ""),
				e("hold", "race", ""), e("cancel_wait", "race", ""), e("race", "which", ""),
				e("which", "ship", flow.HandleFalse), e("which", "cancelled", flow.HandleTrue),
				e("on_address", "set_address", ""), e("on_cancel", "set_cancel", ""),
				e("on_extend", "set_hold", ""), e("set_hold", "reply", ""),
			},
		},
		{
			ID:          "trip-booking",
			Name:        "Trip booking (saga)",
			Description: "Books a flight and a hotel, then charges the card. The card always declines here, so the run fails and Temporal runs each completed step's undo request in reverse: hotel cancelled, then flight.",
			Nodes: []flow.Node{
				n("trigger", flow.TypeTrigger, "start", "Book trip", 0, 120, flow.TriggerConfig{Kind: "manual", SampleInput: `{"traveler": "C-42", "destination": "Lisbon"}`}),
				n("flight", flow.TypeHTTP, "flight", "Book flight", 300, 120, flow.HTTPConfig{
					Method: "POST", URL: s + "/bookings/flight", Body: `{"traveler": "{{ input.traveler }}", "to": "{{ input.destination }}"}`,
					Undo: &flow.HTTPRequestSpec{Method: "POST", URL: s + "/bookings/flight/{{ steps.flight.body.id }}/cancel"},
				}),
				n("hotel", flow.TypeHTTP, "hotel", "Book hotel", 600, 120, flow.HTTPConfig{
					Method: "POST", URL: s + "/bookings/hotel", Body: `{"traveler": "{{ input.traveler }}", "city": "{{ input.destination }}"}`,
					Undo: &flow.HTTPRequestSpec{Method: "POST", URL: s + "/bookings/hotel/{{ steps.hotel.body.id }}/cancel"},
				}),
				n("charge", flow.TypeHTTP, "charge", "Charge card", 900, 120, flow.HTTPConfig{
					Method: "POST", URL: s + "/cards/charge?decline=true", Body: `{"traveler": "{{ input.traveler }}"}`,
				}),
				n("confirm", flow.TypeHTTP, "confirm", "Send itinerary", 1200, 120, notify("email:{{ input.traveler }}", "You're going to {{ input.destination }}!")),
			},
			Edges: []flow.Edge{e("trigger", "flight", ""), e("flight", "hotel", ""), e("hotel", "charge", ""), e("charge", "confirm", "")},
		},
		{
			ID:          "carrier-quote",
			Name:        "Fastest carrier quote",
			Description: "Asks three carriers for a quote at once and books the first good answer. Budget Freight is down, so its branch fails and drops out; Swift answers first and wins; Temporal cancels the slow Parcel Post request.",
			Nodes: []flow.Node{
				n("trigger", flow.TypeTrigger, "start", "Ship parcel", 0, 160, flow.TriggerConfig{Kind: "manual", SampleInput: `{"orderId": "A-1001", "weightKg": 3}`}),
				n("budget", flow.TypeHTTP, "budget", "Budget Freight", 320, 0, quote(s, "budget")),
				n("swift", flow.TypeHTTP, "swift", "Swift Couriers", 320, 160, quote(s, "swift")),
				n("parcel", flow.TypeHTTP, "parcel_post", "Parcel Post", 320, 320, quote(s, "parcel-post")),
				n("race", flow.TypeFirstOf, "best_quote", "First quote", 640, 160, flow.FirstOfConfig{Wins: flow.FirstOfSucceeds}),
				n("book", flow.TypeHTTP, "book", "Book shipment", 960, 80,
					notify("#shipping", "Booked {{ input.orderId }} with {{ steps.best_quote.output.body.carrier }}: ${{ steps.best_quote.output.body.price }}, {{ steps.best_quote.output.body.days }} days.")),
				n("none", flow.TypeHTTP, "no_carrier", "Alert ops", 960, 260,
					notify("#shipping", "No carrier could quote {{ input.orderId }}: {{ steps.best_quote.error }}")),
			},
			Edges: []flow.Edge{
				e("trigger", "budget", ""), e("trigger", "swift", ""), e("trigger", "parcel", ""),
				e("budget", "race", ""), e("swift", "race", ""), e("parcel", "race", ""),
				e("race", "book", ""), e("race", "none", flow.HandleError),
			},
		},
		{
			ID:          "daily-stock-report",
			Name:        "Daily stock report",
			Description: "Runs on a Temporal Schedule every weekday at 9:00 New York time. Checks stock and tells purchasing when it's low. Turn the schedule on from the trigger or the Schedules page.",
			Nodes: []flow.Node{
				n("trigger", flow.TypeTrigger, "start", "Weekdays 9:00", 0, 120, flow.TriggerConfig{Kind: "schedule", SampleInput: `{"sku": "BLND-500"}`, Schedule: &flow.ScheduleConfig{
					Calendar: &flow.CalendarSpec{Days: []string{"mon", "tue", "wed", "thu", "fri"}, Hour: 9}, Timezone: "America/New_York",
				}}),
				n("stock", flow.TypeHTTP, "stock", "Check stock", 300, 120, flow.HTTPConfig{Method: "GET", URL: s + "/inventory/{{ input.sku }}"}),
				n("low", flow.TypeCondition, "is_low", "Low stock?", 600, 120, flow.ConditionConfig{Expression: "steps.stock.body.inStock < 15"}),
				n("tell", flow.TypeHTTP, "tell_purchasing", "Tell purchasing", 900, 40, notify("#purchasing", "Only {{ steps.stock.body.inStock }} left of {{ input.sku }}.")),
			},
			Edges: []flow.Edge{e("trigger", "stock", ""), e("stock", "low", ""), e("low", "tell", flow.HandleTrue)},
		},
		{
			ID:          "customer-loyalty",
			Name:        "Customer loyalty (entity)",
			Description: "One run per customer for the life of their account: an entity Workflow. Purchases arrive as Signals; points and tier live in variables, however long the customer goes quiet. The run continues as new when Temporal suggests it, picking up new worker builds at that boundary, and ends only when the account closes.",
			Settings: flow.FlowSettings{
				KeyedBy: "input.customerId",
				Entity:  true,
				Variables: []flow.VariableDef{
					{Name: "points", Initial: `0`, Visible: true},
					{Name: "tier", Initial: `"member"`, Visible: true},
				},
			},
			Nodes: []flow.Node{
				n("trigger", flow.TypeTrigger, "start", "Customer joins", 0, 0, flow.TriggerConfig{Kind: "webhook", SampleInput: `{"customerId": "C-42"}`}),
				n("welcome", flow.TypeHTTP, "welcome", "Welcome email", 300, 0, notify("email:{{ key }}", "Welcome to rewards!")),
				n("on_purchase", flow.TypeOnSignal, "purchase", "On purchase", 0, 220, flow.OnSignalConfig{SignalName: "purchase"}),
				n("add", flow.TypeSetVariable, "add_points", "Add points", 300, 220, flow.SetVariableConfig{Assignments: []flow.Assignment{{Name: "points", Value: "{{ vars.points + int(steps.purchase.amount) }}"}}}),
				n("gold", flow.TypeCondition, "reached_gold", "Reached 500?", 600, 220, flow.ConditionConfig{Expression: `vars.points >= 500 && vars.tier != "gold"`}),
				n("upgrade", flow.TypeSetVariable, "upgrade", "Make gold", 900, 160, flow.SetVariableConfig{Assignments: []flow.Assignment{{Name: "tier", Value: `gold`}}}),
				n("congrats", flow.TypeHTTP, "congrats", "Congratulate", 1200, 160, notify("email:{{ key }}", "You're gold! {{ vars.points }} points.")),
				n("on_close", flow.TypeOnSignal, "close_account", "On close_account", 0, 420, flow.OnSignalConfig{SignalName: "close_account"}),
				n("goodbye", flow.TypeHTTP, "goodbye", "Goodbye email", 300, 420, notify("email:{{ key }}", "Your account is closed. You had {{ vars.points }} points.")),
				n("end", flow.TypeEnd, "end", "End run", 600, 420, flow.EndConfig{}),
			},
			Edges: []flow.Edge{
				e("trigger", "welcome", ""),
				e("on_purchase", "add", ""), e("add", "gold", ""), e("gold", "upgrade", flow.HandleTrue), e("upgrade", "congrats", ""),
				e("on_close", "goodbye", ""), e("goodbye", "end", ""),
			},
		},
		{
			ID:          "bulk-notify",
			Name:        "Ship to many customers",
			Description: "For each customer in the list, runs the Order shipped flow as a child Workflow, two at a time. Each child has its own history and can be opened on its own.",
			Nodes: []flow.Node{
				n("trigger", flow.TypeTrigger, "start", "Manual start", 0, 120, flow.TriggerConfig{Kind: "manual", SampleInput: `{"customers": ["C-1", "C-2", "C-3", "C-4"]}`}),
				n("each", flow.TypeForEach, "shipments", "Ship to each customer", 300, 120, flow.ForEachConfig{
					Items: "input.customers", FlowID: "order-shipped", Concurrency: 2,
					Input: `{"item": "Gift box", "customer": "{{ item }}"}`,
				}),
				n("summary", flow.TypeHTTP, "summary", "Post summary", 600, 120, notify("#ops", "Shipped {{ steps.shipments.count }} gift boxes.")),
			},
			Edges: []flow.Edge{e("trigger", "each", ""), e("each", "summary", "")},
		},
		{
			ID:          "support-triage",
			Name:        "AI support triage",
			Description: "A Claude agent investigates a support ticket with HTTP tools, can ask a person for help, and proposes a resolution. Each model turn and tool call is a durable step in a child Workflow.",
			Nodes: []flow.Node{
				n("trigger", flow.TypeTrigger, "start", "New ticket", 0, 140, flow.TriggerConfig{Kind: "manual", SampleInput: `{"ticketId": "T-1001"}`}),
				n("agent", flow.TypeAgent, "triage", "Triage agent", 300, 140, flow.AgentConfig{
					Instructions: "Triage support ticket {{ input.ticketId }}. Read the ticket, then look up the customer, the order, and stock for the product. " +
						"Decide how to resolve it: a refund, a replacement (only if in stock), or a reply. If you would refund more than $150, ask the human supervisor first. " +
						`Reply with JSON: {"action": "refund" | "replace" | "reply", "priority": "high" | "normal" | "low", "orderId": string, "customerId": string, "refundAmount": number, "summary": string, "customerMessage": string}`,
					Tools: []flow.AgentTool{
						{Name: "get_ticket", Description: "Fetch a support ticket by ID.", Method: "GET", URL: s + "/tickets/{{ args.ticket_id }}", Params: []flow.AgentToolParam{{Name: "ticket_id", Description: "Ticket ID, e.g. T-1001"}}},
						{Name: "get_customer", Description: "Fetch a customer's profile, tier, and lifetime value.", Method: "GET", URL: s + "/customers/{{ args.customer_id }}", Params: []flow.AgentToolParam{{Name: "customer_id", Description: "Customer ID, e.g. C-42"}}},
						{Name: "get_order", Description: "Fetch an order's status and total.", Method: "GET", URL: s + "/orders/{{ args.order_id }}", Params: []flow.AgentToolParam{{Name: "order_id", Description: "Order ID, e.g. A-1001"}}},
						{Name: "check_inventory", Description: "Check stock for a product SKU.", Method: "GET", URL: s + "/inventory/{{ args.sku }}", Params: []flow.AgentToolParam{{Name: "sku", Description: "Product SKU"}}},
					},
					AllowAskHuman: true,
					MaxTurns:      10,
				}),
				n("route", flow.TypeCondition, "is_refund", "Refund?", 600, 140, flow.ConditionConfig{Expression: `steps.triage.data.action == "refund"`}),
				n("approve", flow.TypeApproval, "approve_refund", "Approve refund", 900, 40, flow.ApprovalConfig{
					Title:   "Refund ${{ steps.triage.data.refundAmount }} on {{ steps.triage.data.orderId }}?",
					Details: "{{ steps.triage.data.summary }}",
				}),
				n("refund", flow.TypeHTTP, "refund", "Issue refund", 1200, 40, flow.HTTPConfig{
					Method: "POST", URL: s + "/refunds",
					Headers: map[string]string{"Idempotency-Key": "{{ run.id }}-refund"},
					Body:    `{"orderId": "{{ steps.triage.data.orderId }}", "amount": {{ steps.triage.data.refundAmount }}, "reason": "support ticket {{ input.ticketId }}"}`,
				}),
				n("reply", flow.TypeHTTP, "reply", "Reply to customer", 1500, 160,
					notify("email:{{ steps.triage.data.customerId }}", "{{ steps.triage.data.customerMessage }}")),
				n("ops", flow.TypeHTTP, "alert_ops", "Hand to a person", 600, 320,
					notify("#support", "Agent could not triage {{ input.ticketId }}: {{ steps.triage.error }}")),
			},
			Edges: []flow.Edge{
				e("trigger", "agent", ""), e("agent", "route", ""), e("agent", "ops", flow.HandleError),
				e("route", "approve", flow.HandleTrue), e("route", "reply", flow.HandleFalse),
				e("approve", "refund", flow.HandleApproved), e("approve", "reply", flow.HandleRejected),
				e("refund", "reply", ""),
			},
		},
	}

	out := map[string]flow.Definition{}
	for i, d := range list {
		d.Version = 1
		if d.Settings.Direction == "" {
			d.Settings.Direction = "LR"
		}
		// Keep the list in this order in the builder: newest first.
		d.UpdatedAt = now.Add(-time.Duration(i) * time.Second)
		out[d.ID] = d
	}
	return out
}

// quote asks one carrier for a price. Two quick retries: a carrier that is
// down should drop out of the race in seconds, not hold it up.
func quote(sandbox, carrier string) flow.HTTPConfig {
	return flow.HTTPConfig{
		Method: "POST", URL: sandbox + "/quotes/" + carrier,
		Body:     `{"orderId": "{{ input.orderId }}", "weightKg": {{ input.weightKg }}}`,
		Activity: flow.ActivityOptions{Retry: flow.RetryConfig{MaxAttempts: 2, InitialIntervalSeconds: 1, BackoffCoefficient: 1}},
	}
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
