package connectors

const sobject = `^[A-Za-z][A-Za-z0-9_]*$`

// Salesforce reads and writes records in a Salesforce org over its REST API.
var Salesforce = Connector{
	Type:       "salesforce",
	Label:      "Salesforce",
	Connection: "Uses the worker's connection to Salesforce: SALESFORCE_URL and its OAuth client credentials. An expired session gets a new token within the attempt.",
	Errors:     "Failures keep Salesforce's error code as their type. Lock contention (UNABLE_TO_LOCK_ROW), API limits and outages are retried; a missing field, a validation rule or a bad query would fail again, so they aren't.",
	Operations: []Operation{
		{
			Name: "query", Label: "Query — SOQL", Activity: "SalesforceQuery", Summary: "SOQL query",
			Params: []Param{
				{Key: "query", Label: "SOQL", Kind: Code, Required: true,
					Default: "SELECT Id, Name FROM Account WHERE Name = '{{ input.company }}'",
					Hint:    "Templates allowed, e.g. WHERE AccountId = '{{ steps.account.id }}'. To read on from an earlier query, put its nextRecordsUrl here."},
				{Key: "allPages", Label: "Read every page", Kind: Bool,
					Hint: "Follows nextRecordsUrl, up to 2,000 records, so the result stays a comfortable size for history. For more, page through them in a Loop."},
			},
			Output: "{{ {out}.records }} (a list of records), {{ {out}.totalSize }}, and {{ {out}.nextRecordsUrl }} while more pages remain",
		},
		{
			Name: "get", Label: "Get a record", Activity: "SalesforceGetRecord", Summary: "Get {object}",
			Params: []Param{sfObject, sfRecordID,
				{Key: "fields", Label: "Fields", Kind: List, Placeholder: "Name, Industry, OwnerId", Hint: "Comma-separated. Empty returns every field."}},
			Output: "each field, e.g. {{ {out}.Name }}",
		},
		{
			Name: "create", Label: "Create a record", Activity: "SalesforceCreateRecord", Summary: "Create {object}",
			Params:   []Param{sfObject, sfValues},
			Output:   "the new record's {{ {out}.id }}",
			UndoNote: "If the run fails later, the record this step created is deleted, along with the rest of the saga, in reverse order.",
			Undo: func(p, out map[string]any) (string, map[string]any) {
				return "delete", with(keep(p, "object"), "recordId", out["id"])
			},
		},
		{
			Name: "update", Label: "Update a record", Activity: "SalesforceUpdateRecord", Summary: "Update {object}",
			Params:   []Param{sfObject, sfRecordID, sfValues},
			Output:   "{{ {out}.id }} and the values it replaced, {{ {out}.previous }}",
			UndoNote: "If the run fails later, the fields this step changed get back the values they had before it.",
			Undo: func(p, out map[string]any) (string, map[string]any) {
				return "update", with(keep(p, "object", "recordId"), "values", out["previous"])
			},
		},
		{
			Name: "upsert", Label: "Upsert by external ID", Activity: "SalesforceUpsertRecord", Summary: "Upsert {object} by {externalIdField}",
			Params: []Param{sfObject,
				{Key: "externalIdField", Label: "External ID field", Kind: Text, Required: true, Default: "Domain__c", Pattern: sobject, PatternHint: "a field's API name"},
				{Key: "externalId", Label: "External ID value", Kind: Text, Required: true, Placeholder: "{{ input.domain }}"},
				sfValues},
			Output: "{{ {out}.id }}, and {{ {out}.created }}: true if no record had the external ID",
			Note:   "Updates the record whose external ID field has this value, or creates one when none does. Running it twice changes nothing, so retries are always safe.",
		},
		{
			Name: "delete", Label: "Delete a record", Activity: "SalesforceDeleteRecord", Summary: "Delete {object}",
			Params: []Param{sfObject, sfRecordID},
			Output: "{{ {out}.deleted }}",
			Note:   "A record that is already gone counts as deleted, so a retried delete succeeds.",
		},
	},
}

var (
	sfObject = Param{Key: "object", Label: "Object", Kind: Text, Required: true, Default: "Account",
		Hint:        "An sObject's API name: standard, like Account, or custom, like Invoice__c.",
		Suggestions: []string{"Account", "Contact", "Lead", "Opportunity", "Case", "Task"},
		Pattern:     sobject, PatternHint: "an sObject's API name, such as Account"}
	sfRecordID = Param{Key: "recordId", Label: "Record ID", Kind: Text, Required: true, Placeholder: "{{ steps.account.id }}"}
	sfValues   = Param{Key: "values", Label: "Field values (JSON template)", Kind: JSON, Required: true,
		Default: "{\n  \"Name\": \"{{ input.company }}\"\n}", Hint: `API names and values. "{{ input.amount }}" keeps its type.`}
)

// Stripe takes payments through the Stripe API.
var Stripe = Connector{
	Type:       "stripe",
	Label:      "Stripe",
	Connection: "Uses the worker's Stripe key (STRIPE_API_KEY; use a test-mode key). Every request carries an Idempotency-Key made from the run, the step and the item, so Stripe treats a retried attempt as the same request: a retry never charges twice.",
	Errors:     "Failures take Stripe's error code as their type (card_declined, resource_missing, …). Card errors and invalid requests aren't retried; rate limits, lock timeouts and outages are, as Stripe's Stripe-Should-Retry header says.",
	Operations: []Operation{
		{
			Name: "find_customer", Label: "Find customers by email", Activity: "StripeFindCustomers", Summary: "Find customer {email}",
			Params: []Param{{Key: "email", Label: "Email", Kind: Text, Required: true, Default: "{{ input.email }}"}},
			Output: "{{ {out}.found }}, the first match as {{ {out}.customer }} (with its id), and every match in {{ {out}.customers }}",
		},
		{
			Name: "create_customer", Label: "Create a customer", Activity: "StripeCreateCustomer", Summary: "Create customer {email}",
			Params: []Param{
				{Key: "email", Label: "Email", Kind: Text, Default: "{{ input.email }}"},
				{Key: "name", Label: "Name", Kind: Text, Default: "{{ input.name }}"},
				{Key: "description", Label: "Description", Kind: Text},
				stripeMetadata,
			},
			Output:   "the customer, with {{ {out}.id }}",
			UndoNote: "If the run fails later, the customer is deleted.",
			Undo: func(p, out map[string]any) (string, map[string]any) {
				return "delete_customer", map[string]any{"customer": out["id"]}
			},
		},
		{
			Name: "delete_customer", Label: "Delete a customer", Activity: "StripeDeleteCustomer", Summary: "Delete customer",
			Params: []Param{{Key: "customer", Label: "Customer ID", Kind: Text, Required: true, Placeholder: "{{ steps.customer.id }}"}},
			Output: "{{ {out}.deleted }}",
		},
		{
			Name: "charge", Label: "Charge a card (PaymentIntent)", Activity: "StripeCreatePayment", Summary: "Charge {amount} {currency}",
			Params: []Param{
				{Key: "amount", Label: "Amount", Kind: Number, Required: true, Default: "{{ input.amount * 100 }}", Hint: "In the currency's smallest unit: cents for usd."},
				{Key: "currency", Label: "Currency", Kind: Text, Required: true, Default: "usd", Suggestions: []string{"usd", "eur", "gbp"}},
				{Key: "customer", Label: "Customer ID", Kind: Text, Placeholder: "{{ steps.customer.id }}"},
				{Key: "paymentMethod", Label: "Payment method", Kind: Text, Required: true, Default: "pm_card_visa",
					Suggestions: []string{"pm_card_visa", "pm_card_chargeDeclined", "pm_card_chargeDeclinedInsufficientFunds"},
					Hint:        "A saved payment method. In test mode, pm_card_chargeDeclined is declined."},
				{Key: "description", Label: "Description", Kind: Text},
				stripeMetadata,
			},
			Output:   "the PaymentIntent: {{ {out}.id }}, {{ {out}.status }}, {{ {out}.amount }}",
			Note:     "Creates and confirms a PaymentIntent in one call. A decline is a business answer (card_declined): it takes the error outcome, or fails the run, without retrying.",
			UndoNote: "If the run fails later, the payment is refunded in full.",
			Undo: func(p, out map[string]any) (string, map[string]any) {
				return "refund", map[string]any{"paymentIntent": out["id"], "reason": "requested_by_customer"}
			},
		},
		{
			Name: "get_payment", Label: "Get a payment", Activity: "StripeGetPayment", Summary: "Get payment",
			Params: []Param{{Key: "paymentIntent", Label: "PaymentIntent ID", Kind: Text, Required: true, Placeholder: "{{ steps.charge.id }}"}},
			Output: "the PaymentIntent, e.g. {{ {out}.status }}",
		},
		{
			Name: "refund", Label: "Refund a payment", Activity: "StripeRefund", Summary: "Refund payment",
			Params: []Param{
				{Key: "paymentIntent", Label: "PaymentIntent ID", Kind: Text, Required: true, Placeholder: "{{ steps.charge.id }}"},
				{Key: "amount", Label: "Amount", Kind: Number, Hint: "In the smallest unit. Empty refunds everything that's left."},
				{Key: "reason", Label: "Reason", Kind: Text, Suggestions: []string{"requested_by_customer", "duplicate", "fraudulent"}},
			},
			Output: "the refund: {{ {out}.id }}, {{ {out}.status }}",
		},
	},
}

var stripeMetadata = Param{Key: "metadata", Label: "Metadata (JSON template)", Kind: JSON,
	Placeholder: `{"runId": "{{ run.id }}"}`, Hint: "Key-value pairs Stripe keeps with the object."}

// ServiceNow works with records in a ServiceNow instance's tables.
var ServiceNow = Connector{
	Type:       "servicenow",
	Label:      "ServiceNow",
	Connection: "Uses the worker's ServiceNow instance: SERVICENOW_URL, with SERVICENOW_USER and SERVICENOW_PASSWORD. A free Personal Developer Instance works.",
	Errors:     "Rate limits (429) and outages are retried. A missing record, a rejected login, or a data policy (a field a state requires) would fail again, so they aren't.",
	Operations: []Operation{
		{
			Name: "query", Label: "Query records", Activity: "ServiceNowQuery", Summary: "Query {table}",
			Params: []Param{snTable,
				{Key: "query", Label: "Encoded query", Kind: Text, Placeholder: "active=true^priority=1^ORDERBYDESCsys_created_on",
					Hint: "ServiceNow's encoded query: conditions joined by ^ (and) or ^OR. Empty returns every record, up to the limit."},
				{Key: "fields", Label: "Fields", Kind: List, Placeholder: "number, short_description, state", Hint: "Comma-separated. Empty returns every field."},
				{Key: "limit", Label: "At most", Kind: Number, Default: "20"},
			},
			Output: "{{ {out}.records }} and {{ {out}.count }}",
		},
		{
			Name: "get", Label: "Get a record", Activity: "ServiceNowGetRecord", Summary: "Get {table}",
			Params: []Param{snTable, snSysID},
			Output: "each field, e.g. {{ {out}.number }}",
		},
		{
			Name: "create", Label: "Create a record", Activity: "ServiceNowCreateRecord", Summary: "Create {table}",
			Params:   []Param{snTable, snValues},
			Output:   "the new record: {{ {out}.sys_id }}, {{ {out}.number }}",
			UndoNote: "If the run fails later, the record this step created is deleted.",
			Undo: func(p, out map[string]any) (string, map[string]any) {
				return "delete", with(keep(p, "table"), "sysId", out["sys_id"])
			},
		},
		{
			Name: "update", Label: "Update a record", Activity: "ServiceNowUpdateRecord", Summary: "Update {table}",
			Params:   []Param{snTable, snSysID, snValues},
			Output:   "the record, e.g. {{ {out}.record.state }}, and the values it replaced, {{ {out}.previous }}",
			UndoNote: "If the run fails later, the fields this step changed get back the values they had before it.",
			Undo: func(p, out map[string]any) (string, map[string]any) {
				return "update", with(keep(p, "table", "sysId"), "values", out["previous"])
			},
		},
		{
			Name: "delete", Label: "Delete a record", Activity: "ServiceNowDeleteRecord", Summary: "Delete {table}",
			Params: []Param{snTable, snSysID},
			Output: "{{ {out}.deleted }}",
			Note:   "A record that is already gone counts as deleted, so a retried delete succeeds.",
		},
	},
}

var (
	snTable = Param{Key: "table", Label: "Table", Kind: Text, Required: true, Default: "incident",
		Suggestions: []string{"incident", "change_request", "problem", "sc_request", "sys_user"},
		Pattern:     `^[a-z][a-z0-9_]*$`, PatternHint: "a table name, such as incident"}
	snSysID  = Param{Key: "sysId", Label: "sys_id", Kind: Text, Required: true, Placeholder: "{{ steps.incident.sys_id }}"}
	snValues = Param{Key: "values", Label: "Field values (JSON template)", Kind: JSON, Required: true,
		Default: "{\n  \"short_description\": \"{{ input.summary }}\",\n  \"urgency\": \"2\",\n  \"impact\": \"2\"\n}",
		Hint:    "Column names and values. Choice fields take their stored value, e.g. state 6 for Resolved."}
)

// Slack posts to Slack channels as the worker's bot.
var Slack = Connector{
	Type:       "slack",
	Label:      "Slack",
	Connection: "Posts as the worker's Slack bot (SLACK_BOT_TOKEN, a bot token with chat:write). Invite the bot to a channel before posting there.",
	Errors:     "Failures take Slack's error as their type (channel_not_found, not_in_channel, …) and aren't retried. Rate limiting is: the next attempt waits as long as Slack's Retry-After asks.",
	Operations: []Operation{
		{
			Name: "post", Label: "Post a message", Activity: "SlackPostMessage", Summary: "Post to {channel}",
			Params: []Param{slackChannel,
				{Key: "text", Label: "Text", Kind: Code, Required: true, Default: "{{ input.message }}", Hint: "Slack mrkdwn: *bold*, _italic_, <https://…|links>."},
				{Key: "blocks", Label: "Blocks (JSON template, optional)", Kind: JSON, Hint: "Block Kit layout; text is then the notification's fallback."},
				{Key: "threadTs", Label: "Reply in thread", Kind: Text, Placeholder: "{{ steps.announce.ts }}", Hint: "A message's ts, to reply in its thread."},
			},
			Output:   "{{ {out}.channel }} and {{ {out}.ts }}, which later steps use to update, react to, or reply to it",
			UndoNote: "If the run fails later, the message is deleted.",
			Undo: func(p, out map[string]any) (string, map[string]any) {
				return "delete", map[string]any{"channel": out["channel"], "ts": out["ts"]}
			},
		},
		{
			Name: "update", Label: "Update a message", Activity: "SlackUpdateMessage", Summary: "Update message in {channel}",
			Params: []Param{slackChannel, slackTS,
				{Key: "text", Label: "Text", Kind: Code, Required: true},
				{Key: "blocks", Label: "Blocks (JSON template, optional)", Kind: JSON}},
			Output: "{{ {out}.ts }}",
		},
		{
			Name: "react", Label: "Add a reaction", Activity: "SlackAddReaction", Summary: "React :{name}:",
			Params: []Param{slackChannel, slackTS,
				{Key: "name", Label: "Emoji", Kind: Text, Required: true, Default: "white_check_mark", Suggestions: []string{"white_check_mark", "eyes", "rotating_light", "tada"}}},
			Output: "{{ {out}.ok }}",
			Note:   "Reacting twice with the same emoji is fine: a retry finds it already there.",
		},
		{
			Name: "delete", Label: "Delete a message", Activity: "SlackDeleteMessage", Summary: "Delete message",
			Params: []Param{slackChannel, slackTS},
			Output: "{{ {out}.ok }}",
		},
		{
			Name: "find_user", Label: "Find a user by email", Activity: "SlackFindUser", Summary: "Find user {email}",
			Params: []Param{{Key: "email", Label: "Email", Kind: Text, Required: true, Default: "{{ input.email }}"}},
			Output: "{{ {out}.found }}, and {{ {out}.user.id }} to mention them as <@id>",
		},
	},
}

var (
	slackChannel = Param{Key: "channel", Label: "Channel", Kind: Text, Required: true, Default: "#general",
		Suggestions: []string{"#general", "#sales", "#incidents", "#ops"}, Hint: "A #name, or a channel ID such as C024BE91L."}
	slackTS = Param{Key: "ts", Label: "Message ts", Kind: Text, Required: true, Placeholder: "{{ steps.announce.ts }}"}
)

// GitHub works with a GitHub repository's issues and Actions.
var GitHub = Connector{
	Type:       "github",
	Label:      "GitHub",
	Connection: "Uses the worker's GitHub token (GITHUB_TOKEN, a fine-grained personal access token with access to the repositories).",
	Errors:     "Rate limits are retried when GitHub says the limit resets (or after its Retry-After), and outages are retried. A missing repository (often a token without access), a validation error or bad credentials aren't.",
	Operations: []Operation{
		{
			Name: "create_issue", Label: "Create an issue", Activity: "GitHubCreateIssue", Summary: "Create issue in {repo}",
			Params: []Param{ghRepo,
				{Key: "title", Label: "Title", Kind: Text, Required: true, Default: "{{ input.title }}"},
				{Key: "body", Label: "Body", Kind: Code, Hint: "Markdown."},
				{Key: "labels", Label: "Labels", Kind: List, Placeholder: "bug, incident"},
				{Key: "assignees", Label: "Assignees", Kind: List, Placeholder: "octocat"},
			},
			Output:   "{{ {out}.number }}, {{ {out}.url }} and {{ {out}.state }}",
			UndoNote: "If the run fails later, the issue is closed as not planned (GitHub doesn't delete issues).",
			Undo: func(p, out map[string]any) (string, map[string]any) {
				return "update_issue", map[string]any{"repo": p["repo"], "number": out["number"], "state": "closed", "stateReason": "not_planned"}
			},
		},
		{
			Name: "comment", Label: "Comment on an issue or PR", Activity: "GitHubComment", Summary: "Comment on {repo}#{number}",
			Params: []Param{ghRepo, ghNumber, {Key: "body", Label: "Comment", Kind: Code, Required: true}},
			Output: "{{ {out}.id }} and {{ {out}.url }}",
		},
		{
			Name: "update_issue", Label: "Update an issue", Activity: "GitHubUpdateIssue", Summary: "Update {repo}#{number}",
			Params: []Param{ghRepo, ghNumber,
				{Key: "state", Label: "State", Kind: Text, Suggestions: []string{"open", "closed"}},
				{Key: "stateReason", Label: "Reason", Kind: Text, Suggestions: []string{"completed", "not_planned", "reopened"}},
				{Key: "title", Label: "Title", Kind: Text},
				{Key: "body", Label: "Body", Kind: Code},
				{Key: "labels", Label: "Labels", Kind: List, Hint: "Replaces the issue's labels."},
			},
			Output: "the issue: {{ {out}.number }}, {{ {out}.state }}",
			Note:   "Only the fields you fill in change.",
		},
		{
			Name: "get_issue", Label: "Get an issue or PR", Activity: "GitHubGetIssue", Summary: "Get {repo}#{number}",
			Params: []Param{ghRepo, ghNumber},
			Output: "the issue: {{ {out}.title }}, {{ {out}.state }}, {{ {out}.labels }}",
		},
		{
			Name: "search_issues", Label: "Search issues and PRs", Activity: "GitHubSearchIssues", Summary: "Search issues",
			Params: []Param{{Key: "query", Label: "Search", Kind: Text, Required: true, Default: "repo:acme/storefront is:issue is:open label:bug",
				Hint: "GitHub search syntax: repo:, is:open, label:, author:, …"}},
			Output: "{{ {out}.totalCount }} and {{ {out}.items }}",
		},
		{
			Name: "dispatch_workflow", Label: "Run a GitHub Actions workflow", Activity: "GitHubDispatchWorkflow", Summary: "Run {workflow} in {repo}",
			Params: []Param{ghRepo,
				{Key: "workflow", Label: "Workflow file", Kind: Text, Required: true, Default: "deploy.yml", Placeholder: "deploy.yml"},
				{Key: "ref", Label: "Branch or tag", Kind: Text, Required: true, Default: "main"},
				{Key: "inputs", Label: "Inputs (JSON template)", Kind: JSON,
					Default: "{\n  \"callback_url\": \"{{ run.callbackUrl }}/events/workflow_finished\"\n}",
					Hint:    "The workflow's workflow_dispatch inputs. Values are strings."},
			},
			Output: "{{ {out}.dispatched }}",
			Note:   "GitHub starts the workflow and answers at once. To wait for it, pass a callback URL as an input, have the workflow's last step POST its result there, and follow this step with a Wait for signal.",
		},
	},
}

var (
	ghRepo = Param{Key: "repo", Label: "Repository", Kind: Text, Required: true, Default: "acme/storefront",
		Pattern: `^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`, PatternHint: "owner/name"}
	ghNumber = Param{Key: "number", Label: "Issue or PR number", Kind: Number, Required: true, Placeholder: "{{ steps.issue.number }}"}
)
