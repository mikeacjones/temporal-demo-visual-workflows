// Package examples holds the flows every new session starts with, which are
// also the built-in templates. Each one is built around a Temporal behavior a
// presenter can show.
package examples

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/connectors"
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
				n("trigger", flow.TypeTrigger, "start", "Manual start", 159, 0, flow.TriggerConfig{Kind: "manual", SampleInput: `{"item": "Espresso grinder", "customer": "C-42"}`}),
				n("create", flow.TypeHTTP, "create_order", "Create order", 159, 174, flow.HTTPConfig{
					Method: "POST", URL: s + "/orders",
					// The store calls this URL on every status change; the app
					// turns each call into an order_status Signal on this run.
					Body: `{"item": "{{ input.item }}", "customer": "{{ input.customer }}", "callbackUrl": "{{ run.callbackUrl }}/events/order_status"}`,
				}),
				n("wait", flow.TypeWaitCondition, "shipped", "Wait until shipped", 159, 348, flow.WaitConditionConfig{
					SignalName: "order_status", Condition: `events.order_status.status == "shipped"`, TimeoutSeconds: 300,
				}),
				n("email", flow.TypeHTTP, "email_customer", "Email customer", 0, 522,
					notify("email:{{ input.customer }}", "Good news! Your {{ input.item }} has shipped (order {{ steps.create_order.body.id }}).")),
				n("late", flow.TypeHTTP, "alert_ops", "Alert ops", 318, 522,
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
				n("trigger", flow.TypeTrigger, "start", "Manual start", 318, 0, flow.TriggerConfig{Kind: "manual", SampleInput: `{"orderId": "A-1001", "amount": 129}`}),
				n("charge", flow.TypeHTTP, "charge", "Charge card", 318, 174, flow.HTTPConfig{
					Method: "POST", URL: s + "/flaky/{{ run.id }}?failures=3",
					Body: `{"orderId": "{{ input.orderId }}", "amount": {{ input.amount }}, "callbackUrl": "{{ run.callbackUrl }}/events/payment_settled"}`,
					Activity: flow.ActivityOptions{Retry: flow.RetryConfig{
						MaxAttempts: 6, InitialIntervalSeconds: 2, BackoffCoefficient: 1.5, MaximumIntervalSeconds: 10,
					}},
				}),
				n("settle", flow.TypeWaitSignal, "settlement", "Wait for settlement", 159, 348, flow.WaitSignalConfig{SignalName: "payment_settled", TimeoutSeconds: 600}),
				n("receipt", flow.TypeTransform, "receipt", "Build receipt", 0, 522, flow.TransformConfig{
					Output: `{"orderId": "{{ input.orderId }}", "transactionId": "{{ steps.charge.body.transactionId }}", "settlement": "{{ steps.settlement.payload }}", "gatewayAttempts": "{{ steps.charge.body.attempt }}"}`,
				}),
				n("done", flow.TypeHTTP, "notify_finance", "Notify finance", 0, 696,
					notify("#payments", "Captured {{ input.orderId }} ({{ steps.receipt.transactionId }}) after {{ steps.receipt.gatewayAttempts }} gateway attempts.")),
				n("unsettled", flow.TypeHTTP, "flag_unsettled", "Flag unsettled", 318, 522,
					notify("#payments", "{{ input.orderId }} charged but not settled within 10 minutes.")),
				n("failed", flow.TypeHTTP, "alert_ops", "Alert ops", 477, 348,
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
				n("trigger", flow.TypeTrigger, "start", "Refund requested", 85, 0, flow.TriggerConfig{Kind: "webhook", SampleInput: `{"orderId": "A-1001", "amount": 180, "reason": "Arrived damaged"}`}),
				n("check", flow.TypeCondition, "needs_review", "Over $100?", 85, 174, flow.ConditionConfig{Expression: "input.amount > 100"}),
				n("approve", flow.TypeApproval, "manager", "Manager approval", 0, 348, flow.ApprovalConfig{
					Title:   "Refund ${{ input.amount }} on order {{ input.orderId }}?",
					Details: "Customer reason: {{ input.reason }}", TimeoutSeconds: 300,
				}),
				n("refund", flow.TypeHTTP, "issue_refund", "Issue refund", 85, 522, flow.HTTPConfig{
					Method: "POST", URL: s + "/refunds",
					// The Workflow ID is stable across retries, so a retried
					// refund is recognised and not paid twice.
					Headers: map[string]string{"Idempotency-Key": "{{ run.id }}-issue_refund"},
					Body:    `{"orderId": "{{ input.orderId }}", "amount": {{ input.amount }}, "reason": "{{ input.reason }}"}`,
				}),
				n("confirm", flow.TypeHTTP, "confirm", "Confirm to customer", 85, 696,
					notify("email:customer", "Your refund {{ steps.issue_refund.body.refundId }} for ${{ input.amount }} is on its way.")),
				n("declined", flow.TypeHTTP, "declined", "Tell customer no", 721, 522,
					notify("email:customer", "We couldn't approve a refund for order {{ input.orderId }}. A specialist will be in touch.")),
				n("escalate", flow.TypeHTTP, "escalate", "Escalate", 403, 522,
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
				n("trigger", flow.TypeTrigger, "start", "Order placed", 159, 0, flow.TriggerConfig{Kind: "manual", SampleInput: `{"orderId": "A-7001", "address": "1 Main St, Portland"}`}),
				n("init", flow.TypeSetVariable, "init", "Remember address", 159, 174, flow.SetVariableConfig{Assignments: []flow.Assignment{{Name: "address", Value: "{{ input.address }}"}}}),
				n("hold", flow.TypeDelay, "hold", "Hold window", 0, 348, flow.DelayConfig{Mode: "expression", Expression: "vars.hold_minutes * 60"}),
				n("cancel_wait", flow.TypeWaitCondition, "cancel_requested", "Cancel requested?", 318, 348, flow.WaitConditionConfig{Condition: "vars.cancelled == true"}),
				n("race", flow.TypeFirstOf, "outcome", "Hold ends or cancel", 159, 522, flow.FirstOfConfig{}),
				n("which", flow.TypeCondition, "was_cancelled", "Cancelled?", 159, 696, flow.ConditionConfig{Expression: `steps.outcome.winner == "cancel_requested"`}),
				n("ship", flow.TypeHTTP, "ship", "Ship order", 318, 870, notify("#warehouse", "Ship {{ input.orderId }} to {{ vars.address }}")),
				n("cancelled", flow.TypeHTTP, "confirm_cancel", "Confirm cancellation", 0, 870, notify("email:customer", "Order {{ input.orderId }} is cancelled. Nothing was shipped.")),

				n("on_address", flow.TypeOnSignal, "change_address", "On change_address", 672, 0, flow.OnSignalConfig{SignalName: "change_address"}),
				n("set_address", flow.TypeSetVariable, "new_address", "Update address", 672, 174, flow.SetVariableConfig{Assignments: []flow.Assignment{{Name: "address", Value: "{{ steps.change_address.address }}"}}}),
				n("on_cancel", flow.TypeOnSignal, "cancel_order", "On cancel_order", 1042, 0, flow.OnSignalConfig{SignalName: "cancel_order"}),
				n("set_cancel", flow.TypeSetVariable, "mark_cancelled", "Mark cancelled", 1042, 174, flow.SetVariableConfig{Assignments: []flow.Assignment{{Name: "cancelled", Value: "{{ true }}"}}}),
				n("on_extend", flow.TypeOnUpdate, "extend_hold", "On extend_hold", 1412, 0, flow.OnUpdateConfig{
					UpdateName: "extend_hold", Validator: "request.minutes > 0 && request.minutes <= 60 && !vars.cancelled",
				}),
				n("set_hold", flow.TypeSetVariable, "longer_hold", "Extend hold", 1412, 174, flow.SetVariableConfig{Assignments: []flow.Assignment{{Name: "hold_minutes", Value: "{{ vars.hold_minutes + steps.extend_hold.minutes }}"}}}),
				n("reply", flow.TypeRespond, "reply", "Respond", 1412, 348, flow.RespondConfig{Value: `{"holdMinutes": "{{ vars.hold_minutes }}", "address": "{{ vars.address }}"}`}),
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
			Description: "Books a flight and a hotel, then charges the card. The three are a saga: the flight and the hotel each link to the step that cancels them, and the charge links in with no undo of its own, because its failure should unwind the trip. The card always declines here, so the hotel is cancelled, then the flight, and the saga's compensated outcome tells the traveler.",
			Nodes: []flow.Node{
				n("trigger", flow.TypeTrigger, "start", "Book trip", 0, 0, flow.TriggerConfig{Kind: "manual", SampleInput: `{"traveler": "C-42", "destination": "Lisbon"}`}),
				n("flight", flow.TypeHTTP, "flight", "Book flight", 0, 332, flow.HTTPConfig{
					Method: "POST", URL: s + "/bookings/flight", Body: `{"traveler": "{{ input.traveler }}", "to": "{{ input.destination }}"}`,
				}),
				n("hotel", flow.TypeHTTP, "hotel", "Book hotel", 0, 506, flow.HTTPConfig{
					Method: "POST", URL: s + "/bookings/hotel", Body: `{"traveler": "{{ input.traveler }}", "city": "{{ input.destination }}"}`,
				}),
				n("charge", flow.TypeHTTP, "charge", "Charge card", 0, 680, flow.HTTPConfig{
					Method: "POST", URL: s + "/cards/charge?decline=true", Body: `{"traveler": "{{ input.traveler }}"}`,
				}),
				n("confirm", flow.TypeHTTP, "confirm", "Send itinerary", 0, 854, notify("email:{{ input.traveler }}", "You're going to {{ input.destination }}!")),
				n("saga", flow.TypeSaga, "trip", "Trip saga", 338, 174, flow.SagaConfig{Members: []flow.SagaMember{
					{Step: "flight", Unwinds: true, Compensation: "cancel_flight"},
					{Step: "hotel", Unwinds: true, Compensation: "cancel_hotel"},
					{Step: "charge", Unwinds: true},
				}}),
				// Compensations: each runs only if the saga unwinds, and reads
				// its booking's output.
				n("cancel_flight", flow.TypeHTTP, "cancel_flight", "Cancel flight", 338, 332, flow.HTTPConfig{
					Method: "POST", URL: s + "/bookings/flight/{{ steps.flight.body.id }}/cancel",
				}),
				n("cancel_hotel", flow.TypeHTTP, "cancel_hotel", "Cancel hotel", 338, 506, flow.HTTPConfig{
					Method: "POST", URL: s + "/bookings/hotel/{{ steps.hotel.body.id }}/cancel",
				}),
				n("sorry", flow.TypeHTTP, "sorry", "Tell traveler", 676, 174,
					notify("email:{{ input.traveler }}", "We couldn't book your trip to {{ input.destination }} ({{ steps.trip.error }}). Nothing was charged, and the flight and hotel are cancelled.")),
			},
			Edges: []flow.Edge{
				e("trigger", "flight", ""), e("flight", "hotel", ""), e("hotel", "charge", ""), e("charge", "confirm", ""),
				e("saga", "sorry", flow.HandleCompensated),
			},
		},
		{
			ID:          "carrier-quote",
			Name:        "Fastest carrier quote",
			Description: "Asks three carriers for a quote at once and books the first good answer. Budget Freight is down, so its branch fails and drops out; Swift answers first and wins; Temporal cancels the slow Parcel Post request.",
			Nodes: []flow.Node{
				n("trigger", flow.TypeTrigger, "start", "Ship parcel", 328, 0, flow.TriggerConfig{Kind: "manual", SampleInput: `{"orderId": "A-1001", "weightKg": 3}`}),
				n("budget", flow.TypeHTTP, "budget", "Budget Freight", 318, 174, quote(s, "budget")),
				n("swift", flow.TypeHTTP, "swift", "Swift Couriers", 0, 174, quote(s, "swift")),
				n("parcel", flow.TypeHTTP, "parcel_post", "Parcel Post", 636, 174, quote(s, "parcel-post")),
				n("race", flow.TypeFirstOf, "best_quote", "First quote", 308, 348, flow.FirstOfConfig{Wins: flow.FirstOfSucceeds}),
				n("book", flow.TypeHTTP, "book", "Book shipment", 159, 522,
					notify("#shipping", "Booked {{ input.orderId }} with {{ steps.best_quote.output.body.carrier }}: ${{ steps.best_quote.output.body.price }}, {{ steps.best_quote.output.body.days }} days.")),
				n("none", flow.TypeHTTP, "no_carrier", "Alert ops", 477, 522,
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
				n("trigger", flow.TypeTrigger, "start", "Weekdays 9:00", 0, 0, flow.TriggerConfig{Kind: "schedule", SampleInput: `{"sku": "BLND-500"}`, Schedule: &flow.ScheduleConfig{
					Calendar: &flow.CalendarSpec{Days: []string{"mon", "tue", "wed", "thu", "fri"}, Hour: 9}, Timezone: "America/New_York",
				}}),
				n("stock", flow.TypeHTTP, "stock", "Check stock", 0, 174, flow.HTTPConfig{Method: "GET", URL: s + "/inventory/{{ input.sku }}"}),
				n("low", flow.TypeCondition, "is_low", "Low stock?", 0, 348, flow.ConditionConfig{Expression: "steps.stock.body.inStock < 15"}),
				n("tell", flow.TypeHTTP, "tell_purchasing", "Tell purchasing", 0, 522, notify("#purchasing", "Only {{ steps.stock.body.inStock }} left of {{ input.sku }}.")),
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
				n("welcome", flow.TypeHTTP, "welcome", "Welcome email", 0, 174, notify("email:{{ key }}", "Welcome to rewards!")),
				n("on_purchase", flow.TypeOnSignal, "purchase", "On purchase", 354, 0, flow.OnSignalConfig{SignalName: "purchase"}),
				n("add", flow.TypeSetVariable, "add_points", "Add points", 354, 174, flow.SetVariableConfig{Assignments: []flow.Assignment{{Name: "points", Value: "{{ vars.points + int(steps.purchase.amount) }}"}}}),
				n("gold", flow.TypeCondition, "reached_gold", "Reached 500?", 354, 348, flow.ConditionConfig{Expression: `vars.points >= 500 && vars.tier != "gold"`}),
				n("upgrade", flow.TypeSetVariable, "upgrade", "Make gold", 354, 522, flow.SetVariableConfig{Assignments: []flow.Assignment{{Name: "tier", Value: `gold`}}}),
				n("congrats", flow.TypeHTTP, "congrats", "Congratulate", 354, 696, notify("email:{{ key }}", "You're gold! {{ vars.points }} points.")),
				n("on_close", flow.TypeOnSignal, "close_account", "On close_account", 724, 0, flow.OnSignalConfig{SignalName: "close_account"}),
				n("goodbye", flow.TypeHTTP, "goodbye", "Goodbye email", 724, 174, notify("email:{{ key }}", "Your account is closed. You had {{ vars.points }} points.")),
				n("end", flow.TypeEnd, "end", "End run", 724, 348, flow.EndConfig{}),
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
				n("trigger", flow.TypeTrigger, "start", "Manual start", 0, 0, flow.TriggerConfig{Kind: "manual", SampleInput: `{"customers": ["C-1", "C-2", "C-3", "C-4"]}`}),
				n("each", flow.TypeForEach, "shipments", "Ship to each customer", 0, 174, flow.ForEachConfig{
					Items: "input.customers", FlowID: "order-shipped", Concurrency: 2,
					Input: `{"item": "Gift box", "customer": "{{ item }}"}`,
				}),
				n("summary", flow.TypeHTTP, "summary", "Post summary", 0, 348, notify("#ops", "Shipped {{ steps.shipments.count }} gift boxes.")),
			},
			Edges: []flow.Edge{e("trigger", "each", ""), e("each", "summary", "")},
		},
		{
			ID:          "support-triage",
			Name:        "AI support triage",
			Description: "A Claude agent investigates a support ticket with HTTP tools, can ask a person for help, and proposes a resolution. Each model turn and tool call is a durable step in a child Workflow.",
			Nodes: []flow.Node{
				n("trigger", flow.TypeTrigger, "start", "New ticket", 328, 0, flow.TriggerConfig{Kind: "manual", SampleInput: `{"ticketId": "T-1001"}`}),
				n("agent", flow.TypeAgent, "triage", "Triage agent", 328, 174, flow.AgentConfig{
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
				n("route", flow.TypeCondition, "is_refund", "Refund?", 169, 348, flow.ConditionConfig{Expression: `steps.triage.data.action == "refund"`}),
				n("approve", flow.TypeApproval, "approve_refund", "Approve refund", 85, 522, flow.ApprovalConfig{
					Title:   "Refund ${{ steps.triage.data.refundAmount }} on {{ steps.triage.data.orderId }}?",
					Details: "{{ steps.triage.data.summary }}",
				}),
				n("refund", flow.TypeHTTP, "refund", "Issue refund", 0, 696, flow.HTTPConfig{
					Method: "POST", URL: s + "/refunds",
					Headers: map[string]string{"Idempotency-Key": "{{ run.id }}-refund"},
					Body:    `{"orderId": "{{ steps.triage.data.orderId }}", "amount": {{ steps.triage.data.refundAmount }}, "reason": "support ticket {{ input.ticketId }}"}`,
				}),
				n("reply", flow.TypeHTTP, "reply", "Reply to customer", 169, 870,
					notify("email:{{ steps.triage.data.customerId }}", "{{ steps.triage.data.customerMessage }}")),
				n("ops", flow.TypeHTTP, "alert_ops", "Hand to a person", 487, 348,
					notify("#support", "Agent could not triage {{ input.ticketId }}: {{ steps.triage.error }}")),
			},
			Edges: []flow.Edge{
				e("trigger", "agent", ""), e("agent", "route", ""), e("agent", "ops", flow.HandleError),
				e("route", "approve", flow.HandleTrue), e("route", "reply", flow.HandleFalse),
				e("approve", "refund", flow.HandleApproved), e("approve", "reply", flow.HandleRejected),
				e("refund", "reply", ""),
			},
		},
		{
			ID:          "new-customer",
			Name:        "New customer: Salesforce, Stripe, Slack",
			Description: "Business connectors: each step is an Activity, and the worker holds every service's credentials. The Account is upserted by domain; then the Contact, the won Opportunity and the Stripe customer are created in parallel. Contact and Opportunity both lock the Account, so one hits UNABLE_TO_LOCK_ROW and Temporal retries it. The card is charged with an idempotency key, so a retry never charges twice. The writes are a saga, each linked to its connector's built-in rollback: use paymentMethod pm_card_chargeDeclined and the decline unwinds it (the Stripe customer, the Opportunity and the Contact are deleted, newest first), then the saga's compensated outcome tells sales.",
			Nodes: []flow.Node{
				n("trigger", flow.TypeTrigger, "start", "Customer signed up", 328, 0, flow.TriggerConfig{Kind: "webhook",
					SampleInput: `{"company": "Globex", "domain": "globex.com", "firstName": "Hank", "lastName": "Scorpio", "email": "hank@globex.com", "plan": "Enterprise", "amount": 480, "paymentMethod": "pm_card_visa"}`}),
				n("account", salesforce, "account", "Upsert account", 328, 174, op("upsert",
					map[string]any{"object": "Account", "externalIdField": "Domain__c"},
					map[string]string{"externalId": "{{ input.domain }}", "values.Name": "{{ input.company }}", "values.Type": "Customer - Direct"})),
				n("contact", salesforce, "contact", "Create contact", 318, 506, op("create",
					map[string]any{"object": "Contact"},
					map[string]string{"values.AccountId": "{{ steps.account.id }}", "values.FirstName": "{{ input.firstName }}", "values.LastName": "{{ input.lastName }}", "values.Email": "{{ input.email }}"})),
				n("opportunity", salesforce, "opportunity", "Create won opportunity", 0, 506, op("create",
					map[string]any{"object": "Opportunity"},
					map[string]string{
						"values.AccountId": "{{ steps.account.id }}", "values.Name": "{{ input.company }} — {{ input.plan }}",
						"values.StageName": "Closed Won", "values.Amount": "{{ input.amount }}", "values.CloseDate": "{{ run.startedAt.Format('2006-01-02') }}",
					})),
				n("customer", stripe, "customer", "Create Stripe customer", 636, 506, op("create_customer", nil,
					map[string]string{
						"email": "{{ input.email }}", "name": "{{ input.firstName }} {{ input.lastName }}", "description": "{{ input.company }}",
						"metadata.salesforceAccountId": "{{ steps.account.id }}",
					})),
				n("charge", stripe, "charge", "Charge card", 308, 680, op("charge", nil,
					map[string]string{
						"amount": "{{ input.amount * 100 }}", "currency": "usd", "customer": "{{ steps.customer.id }}",
						"paymentMethod": "{{ input.paymentMethod }}", "description": "{{ input.company }} — {{ input.plan }}",
						"metadata.opportunityId": "{{ steps.opportunity.id }}",
					})),
				n("cases", salesforce, "open_cases", "Find open cases", 308, 854, op("query", nil,
					map[string]string{"query": "SELECT Id, Subject, Priority, Status FROM Case WHERE AccountId = '{{ steps.account.id }}' AND IsClosed = false"})),
				n("announce", slack, "announce", "Tell sales", 308, 1028, op("post", nil, map[string]string{
					"channel": "#sales",
					"text":    ":tada: *{{ input.company }}* signed up for {{ input.plan }} (${{ input.amount }}). Opportunity {{ steps.opportunity.id }}, payment {{ steps.charge.id }}. {{ steps.open_cases.totalSize }} open support cases.",
				})),
				// The signup is a saga: each write links to its built-in
				// rollback, and a declined card unwinds them all.
				n("saga", flow.TypeSaga, "signup", "Signup saga", 974, 348, flow.SagaConfig{Members: []flow.SagaMember{
					{Step: "contact", Unwinds: true, BuiltIn: true},
					{Step: "opportunity", Unwinds: true, BuiltIn: true},
					{Step: "customer", Unwinds: true, BuiltIn: true},
					{Step: "charge", Unwinds: true, BuiltIn: true},
				}}),
				n("rolled_back", slack, "rolled_back", "Tell sales it fell through", 1312, 348, op("post", nil, map[string]string{
					"channel": "#sales",
					"text":    ":warning: {{ input.company }}'s signup fell through at {{ steps.signup.failedStep }} ({{ steps.signup.error }}). {{ steps.signup.compensated }} changes were rolled back in Salesforce and Stripe.",
				})),
			},
			Edges: []flow.Edge{
				e("trigger", "account", ""), e("account", "contact", ""), e("account", "opportunity", ""), e("account", "customer", ""),
				e("contact", "charge", ""), e("opportunity", "charge", ""), e("customer", "charge", ""),
				e("charge", "cases", ""), e("cases", "announce", ""),
				e("saga", "rolled_back", flow.HandleCompensated),
			},
		},
		{
			ID:          "salesforce-pipeline-sweep",
			Name:        "Salesforce pipeline sweep",
			Description: "Every weekday morning, a SOQL query finds open opportunities whose close date has passed. For each one, five at a time inside the run, the Salesforce connector updates its next step and opens a follow-up task. Meanwhile a Transform, mapped in the mapper, builds a digest (the count, the total, and the big deals, element by element), and Slack hears it. Two of the deals belong to Acme, and updating both at once locks Acme, so one update is retried.",
			Nodes: []flow.Node{
				n("trigger", flow.TypeTrigger, "start", "Weekdays 8:00", 489, 0, flow.TriggerConfig{Kind: "schedule", SampleInput: `{}`, Schedule: &flow.ScheduleConfig{
					Calendar: &flow.CalendarSpec{Days: []string{"mon", "tue", "wed", "thu", "fri"}, Hour: 8}, Timezone: "America/New_York",
				}}),
				n("overdue", salesforce, "overdue", "Find overdue deals", 489, 174, op("query",
					map[string]any{"allPages": true},
					map[string]string{"query": "SELECT Id, Name, Amount, StageName, CloseDate, Account.Name FROM Opportunity WHERE IsClosed = false AND CloseDate < TODAY ORDER BY Amount DESC"})),
				n("each", flow.TypeForEach, "deals", "Each deal", 220, 348, flow.ForEachConfig{Run: flow.ForEachRunSteps, Items: "steps.overdue.records", Concurrency: 5}),
				n("flag", salesforce, "flag", "Set next step", 416, 546, op("update",
					map[string]any{"object": "Opportunity"},
					map[string]string{"recordId": "{{ item.Id }}", "values.NextStep": "Close date {{ item.CloseDate }} has passed: agree a new one with {{ item.Account.Name }}"})),
				n("task", salesforce, "task", "Open follow-up task", 416, 720, op("create",
					map[string]any{"object": "Task"},
					map[string]string{
						"values.WhatId": "{{ item.Id }}", "values.Subject": "Overdue: {{ item.Name }}",
						"values.Priority": "{{ item.Amount >= 100000 ? 'High' : 'Normal' }}", "values.Status": "Not Started",
						"values.ActivityDate": "{{ run.startedAt.AddDate(0, 0, 2).Format('2006-01-02') }}",
					})),
				// A Transform in the mapper: typed fields, and a list built
				// element by element from the query's records.
				n("digest", flow.TypeTransform, "digest", "Build digest", 758, 544, flow.TransformConfig{
					Fields: []connectors.Field{
						{Name: "count", Type: connectors.Integer, Required: true},
						{Name: "total", Type: connectors.Number},
						{Name: "top", Type: connectors.Array, Fields: []connectors.Field{
							{Name: "title", Type: connectors.String}, {Name: "account", Type: connectors.String},
							{Name: "dollars", Type: connectors.Number}, {Name: "due", Type: connectors.Date},
						}},
					},
					Mapping: map[string]string{
						"count": "{{ steps.overdue.totalSize }}", "total": "{{ sum(map(steps.overdue.records, .Amount)) }}",
						"top[].title": "{{ it.Name }}", "top[].account": "{{ it.Account.Name }}",
						"top[].dollars": "{{ it.Amount }}", "top[].due": "{{ it.CloseDate }}",
					},
					Lists: map[string]flow.ListMapping{"top": {From: "steps.overdue.records", Where: "it.Amount >= 50000"}},
				}),
				n("tell", slack, "tell_sales", "Tell sales", 489, 914, op("post", nil, map[string]string{
					"channel": "#sales",
					"text":    ":alarm_clock: {{ steps.digest.count }} deals worth ${{ steps.digest.total }} are past their close date. Each has a follow-up task. Biggest: {{ join(map(steps.digest.top, .title + ' (' + .account + ')'), ', ') }}.",
				})),
			},
			Edges: []flow.Edge{
				e("trigger", "overdue", ""), e("overdue", "each", ""), e("overdue", "digest", ""),
				e("each", "flag", flow.HandleBody), e("flag", "task", ""), e("task", "each", ""),
				e("each", "tell", ""), e("digest", "tell", ""),
			},
		},
		{
			ID:          "incident-response",
			Name:        "Incident response: ServiceNow, Slack, GitHub",
			Description: "An alert opens a ServiceNow incident, announces it in Slack and files a GitHub issue, then runs the rollback.yml GitHub Actions workflow. The run waits, for free, until the workflow POSTs its result to the run's callback URL (a rollback_finished Signal). Then it resolves the incident (ServiceNow's data policy wants a resolution code and notes), closes the issue, and edits the Slack announcement. If the rollback doesn't report back in 10 minutes, the run pages people in the thread.",
			Nodes: []flow.Node{
				n("trigger", flow.TypeTrigger, "start", "Alert fired", 318, 0, flow.TriggerConfig{Kind: "webhook",
					SampleInput: `{"service": "Storefront", "summary": "Checkout error rate above 5%", "severity": 1, "version": "v2.14.0"}`}),
				n("incident", servicenow, "incident", "Open incident", 318, 174, op("create",
					map[string]any{"table": "incident"},
					map[string]string{
						"values.short_description": "{{ input.service }}: {{ input.summary }}",
						"values.urgency":           "{{ input.severity }}", "values.impact": "{{ input.severity }}",
						"values.category": "software", "values.cmdb_ci": "{{ input.service }}",
						"values.description": "Opened by Temporal run {{ run.id }}",
					})),
				n("announce", slack, "announce", "Announce", 159, 348, op("post", nil, map[string]string{
					"channel": "#incidents",
					"text":    ":rotating_light: *{{ steps.incident.record.number }}* (P{{ steps.incident.record.priority }}) {{ input.service }}: {{ input.summary }}. Rolling back {{ input.version }}.",
				})),
				n("issue", github, "issue", "File issue", 477, 348, op("create_issue",
					map[string]any{"repo": "acme/storefront"},
					map[string]string{
						"title":  "[{{ steps.incident.record.number }}] {{ input.summary }}",
						"body":   "ServiceNow incident {{ steps.incident.record.number }} for {{ input.service }}. Rolling back {{ input.version }}; this issue closes when the rollback finishes.",
						"labels": "incident, sev{{ input.severity }}",
					})),
				n("rollback", github, "rollback", "Run rollback.yml", 318, 522, op("dispatch_workflow",
					map[string]any{"repo": "acme/storefront", "workflow": "rollback.yml", "ref": "main"},
					map[string]string{
						"inputs.service": "{{ input.service }}", "inputs.reason": "{{ steps.incident.record.number }}",
						"inputs.callback_url": "{{ run.callbackUrl }}/events/rollback_finished",
					})),
				n("wait", flow.TypeWaitSignal, "rollback_done", "Wait for rollback", 318, 696, flow.WaitSignalConfig{SignalName: "rollback_finished", TimeoutSeconds: 600}),
				n("resolve", servicenow, "resolve", "Resolve incident", 159, 870, op("update",
					map[string]any{"table": "incident"},
					map[string]string{
						"sysId": "{{ steps.incident.record.sys_id }}", "values.state": "6", "values.close_code": "Solved (Work Around)",
						"values.close_notes": "Rolled back {{ input.version }}: {{ steps.rollback_done.payload.url }}",
					})),
				n("close", github, "close_issue", "Close issue", 0, 1044, op("update_issue",
					map[string]any{"repo": "acme/storefront"},
					map[string]string{"number": "{{ steps.issue.number }}", "state": "closed", "stateReason": "completed"})),
				n("all_clear", slack, "all_clear", "Edit announcement", 318, 1044, op("update", nil, map[string]string{
					"channel": "{{ steps.announce.channel }}", "ts": "{{ steps.announce.ts }}",
					"text": ":white_check_mark: *{{ steps.incident.record.number }}* resolved: {{ input.service }} rolled back from {{ input.version }} ({{ steps.issue.url }}).",
				})),
				n("page", slack, "page", "Page in thread", 477, 870, op("post", nil, map[string]string{
					"channel": "{{ steps.announce.channel }}", "threadTs": "{{ steps.announce.ts }}",
					"text": "<!here> The rollback hasn't reported back in 10 minutes. {{ steps.incident.record.number }} needs a person.",
				})),
			},
			Edges: []flow.Edge{
				e("trigger", "incident", ""), e("incident", "announce", ""), e("incident", "issue", ""),
				e("announce", "rollback", ""), e("issue", "rollback", ""), e("rollback", "wait", ""),
				e("wait", "resolve", flow.HandleReceived), e("wait", "page", flow.HandleTimeout),
				e("resolve", "close", ""), e("resolve", "all_clear", ""),
			},
		},
	}

	out := map[string]flow.Definition{}
	for i, d := range list {
		d.Version = 1
		if d.Settings.Direction == "" {
			d.Settings.Direction = "TB"
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

// The business connectors' step types.
const (
	salesforce flow.NodeType = "salesforce"
	stripe     flow.NodeType = "stripe"
	servicenow flow.NodeType = "servicenow"
	slack      flow.NodeType = "slack"
	github     flow.NodeType = "github"
)

// op is a connector step's config: an operation, its settings, and how its
// inputs are mapped.
func op(operation string, settings map[string]any, mapping map[string]string) flow.ConnectorConfig {
	return flow.ConnectorConfig{Operation: operation, Settings: settings, Mapping: mapping}
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
