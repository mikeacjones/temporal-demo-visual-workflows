package connectors

const sobjectPattern = `^[A-Za-z][A-Za-z0-9_]*$`

// Salesforce reads and writes records in a Salesforce org over its REST API.
var Salesforce = Connector{
	Type:       "salesforce",
	Label:      "Salesforce",
	Connection: "Uses the worker's connection to Salesforce: SALESFORCE_URL and its OAuth client credentials. An expired session gets a new token within the attempt.",
	Errors:     "Failures keep Salesforce's error code as their type. Lock contention (UNABLE_TO_LOCK_ROW), API limits and outages are retried; a missing field, a validation rule or a bad query would fail again, so they aren't.",
	Catalog:    salesforceCatalog,
	CatalogOf:  "object",
	Operations: []Operation{
		{
			Name: "query", Label: "Query (SOQL)", Activity: "SalesforceQuery", Summary: "SOQL query",
			Settings: []Field{{Name: "allPages", Label: "Read every page", Type: Boolean,
				Description: "Follows nextRecordsUrl, up to 2,000 records, so the result stays a comfortable size for history. For more, page through them in a Loop."}},
			Inputs: []Field{{Name: "query", Label: "SOQL", Type: String, Required: true, Multiline: true,
				Default:     "SELECT Id, Name FROM Account WHERE Name = '{{ input.company }}'",
				Description: "To read on from an earlier query, map its nextRecordsUrl here."}},
			Outputs: []Field{
				{Name: "records", Type: Array, Open: true, Description: "The records, with the fields the query selects."},
				{Name: "totalSize", Type: Integer}, {Name: "done", Type: Boolean},
				{Name: "nextRecordsUrl", Type: String, Description: "Set while more records remain."},
			},
		},
		{
			Name: "get", Label: "Get record", Activity: "SalesforceGetRecord", Summary: "Get {object}",
			Settings: []Field{sfObjectSetting,
				{Name: "fields", Label: "Fields", Type: Array, Items: String, Placeholder: "Name, Industry, OwnerId", Description: "Comma-separated. Empty returns every field."}},
			Inputs:  []Field{sfRecordID},
			Outputs: []Field{{Name: "record", Type: Object, Catalog: "object", Open: true}},
		},
		{
			Name: "create", Label: "Create record", Activity: "SalesforceCreateRecord", Summary: "Create {object}",
			Settings: []Field{sfObjectSetting},
			Inputs:   []Field{{Name: "values", Label: "Fields", Type: Object, Required: true, Catalog: "object", Creating: true, Open: true}},
			Outputs:  []Field{{Name: "id", Type: String}, {Name: "success", Type: Boolean}},
			UndoNote: "If the run fails later, the record this step created is deleted.",
			Undo: func(arg, out map[string]any) (string, map[string]any) {
				return "delete", with(keep(arg, "object"), "recordId", out["id"])
			},
		},
		{
			Name: "update", Label: "Update record", Activity: "SalesforceUpdateRecord", Summary: "Update {object}",
			Settings: []Field{sfObjectSetting},
			Inputs:   []Field{sfRecordID, {Name: "values", Label: "Fields", Type: Object, Required: true, Catalog: "object", Open: true}},
			Outputs: []Field{{Name: "id", Type: String}, {Name: "success", Type: Boolean},
				{Name: "previous", Type: Object, Catalog: "object", Open: true, Description: "The values the update replaced."}},
			UndoNote: "If the run fails later, the fields this step changed get back the values they had before it.",
			Undo: func(arg, out map[string]any) (string, map[string]any) {
				return "update", with(keep(arg, "object", "recordId"), "values", out["previous"])
			},
		},
		{
			Name: "upsert", Label: "Upsert record", Activity: "SalesforceUpsertRecord", Summary: "Upsert {object} by {externalIdField}",
			Settings: []Field{sfObjectSetting,
				{Name: "externalIdField", Label: "External ID field", Type: String, Required: true, Default: "Domain__c", Pattern: sobjectPattern, PatternHint: "a field's API name"}},
			Inputs: []Field{
				{Name: "externalId", Label: "External ID value", Type: String, Required: true, Placeholder: "{{ input.domain }}"},
				{Name: "values", Label: "Fields", Type: Object, Required: true, Catalog: "object", Open: true},
			},
			Outputs: []Field{{Name: "id", Type: String}, {Name: "created", Type: Boolean, Description: "True if no record had the external ID."}},
			Note:    "Updates the record whose external ID field has this value, or creates one when none does. Running it twice changes nothing, so retries are always safe.",
		},
		{
			Name: "delete", Label: "Delete record", Activity: "SalesforceDeleteRecord", Summary: "Delete {object}",
			Settings: []Field{sfObjectSetting},
			Inputs:   []Field{sfRecordID},
			Outputs:  []Field{{Name: "id", Type: String}, {Name: "deleted", Type: Boolean}, {Name: "alreadyDeleted", Type: Boolean}},
			Note:     "A record that is already gone counts as deleted, so a retried delete succeeds.",
		},
	},
}

var (
	sfObjectSetting = Field{Name: "object", Label: "Object", Type: String, Required: true, Default: "Account",
		Description: "An sObject's API name: standard, like Account, or custom, like Invoice__c.",
		Suggestions: []string{"Account", "Contact", "Lead", "Opportunity", "Case", "Task"},
		Pattern:     sobjectPattern, PatternHint: "an sObject's API name, such as Account"}
	sfRecordID = Field{Name: "recordId", Label: "Record ID", Type: String, Required: true, Placeholder: "{{ steps.account.id }}"}
)

// Stripe takes payments through the Stripe API.
var Stripe = Connector{
	Type:       "stripe",
	Label:      "Stripe",
	Connection: "Uses the worker's Stripe key (STRIPE_API_KEY; use a test-mode key). Every request carries an Idempotency-Key made from the run, the step and the item, so Stripe treats a retried attempt as the same request: a retry never charges twice.",
	Errors:     "Failures take Stripe's error code as their type (card_declined, resource_missing, …). Card errors and invalid requests aren't retried; rate limits, lock timeouts and outages are, as Stripe's Stripe-Should-Retry header says.",
	Operations: []Operation{
		{
			Name: "find_customer", Label: "Find customers by email", Activity: "StripeFindCustomers", Summary: "Find customer by email",
			Inputs: []Field{{Name: "email", Label: "Email", Type: String, Required: true, Default: "{{ input.email }}"}},
			Outputs: []Field{{Name: "found", Type: Boolean},
				{Name: "customer", Type: Object, Fields: stripeCustomer, Description: "The first match, or null."},
				{Name: "customers", Type: Array, Fields: stripeCustomer}},
		},
		{
			Name: "create_customer", Label: "Create customer", Activity: "StripeCreateCustomer", Summary: "Create customer",
			Inputs: []Field{
				{Name: "email", Label: "Email", Type: String, Default: "{{ input.email }}"},
				{Name: "name", Label: "Name", Type: String, Default: "{{ input.name }}"},
				{Name: "description", Label: "Description", Type: String},
				stripeMetadata,
			},
			Outputs:  stripeCustomer,
			UndoNote: "If the run fails later, the customer is deleted.",
			Undo: func(arg, out map[string]any) (string, map[string]any) {
				return "delete_customer", map[string]any{"customer": out["id"]}
			},
		},
		{
			Name: "delete_customer", Label: "Delete customer", Activity: "StripeDeleteCustomer", Summary: "Delete customer",
			Inputs:  []Field{{Name: "customer", Label: "Customer ID", Type: String, Required: true, Placeholder: "{{ steps.customer.id }}"}},
			Outputs: []Field{{Name: "id", Type: String}, {Name: "deleted", Type: Boolean}},
		},
		{
			Name: "charge", Label: "Charge a card", Activity: "StripeCreatePayment", Summary: "Charge a card (PaymentIntent)",
			Inputs: []Field{
				{Name: "amount", Label: "Amount", Type: Integer, Required: true, Default: "{{ input.amount * 100 }}", Description: "In the currency's smallest unit: cents for usd."},
				{Name: "currency", Label: "Currency", Type: String, Required: true, Default: "usd", Suggestions: []string{"usd", "eur", "gbp"}},
				{Name: "customer", Label: "Customer ID", Type: String, Placeholder: "{{ steps.customer.id }}"},
				{Name: "paymentMethod", Label: "Payment method", Type: String, Required: true, Default: "pm_card_visa",
					Suggestions: []string{"pm_card_visa", "pm_card_chargeDeclined", "pm_card_chargeDeclinedInsufficientFunds"},
					Description: "A saved payment method. In test mode, pm_card_chargeDeclined is declined."},
				{Name: "description", Label: "Description", Type: String},
				stripeMetadata,
			},
			Outputs:  stripePaymentIntent,
			Note:     "Creates and confirms a PaymentIntent in one call. A decline is a business answer (card_declined): it takes the error outcome, or fails the run, without retrying.",
			UndoNote: "If the run fails later, the payment is refunded in full.",
			Undo: func(arg, out map[string]any) (string, map[string]any) {
				return "refund", map[string]any{"paymentIntent": out["id"], "reason": "requested_by_customer"}
			},
		},
		{
			Name: "get_payment", Label: "Get payment", Activity: "StripeGetPayment", Summary: "Get payment",
			Inputs:  []Field{stripePaymentIntentID},
			Outputs: stripePaymentIntent,
		},
		{
			Name: "refund", Label: "Refund payment", Activity: "StripeRefund", Summary: "Refund payment",
			Inputs: []Field{stripePaymentIntentID,
				{Name: "amount", Label: "Amount", Type: Integer, Description: "In the smallest unit. Empty refunds everything that's left."},
				{Name: "reason", Label: "Reason", Type: String, Enum: []string{"requested_by_customer", "duplicate", "fraudulent"}}},
			Outputs: []Field{{Name: "id", Type: String}, {Name: "amount", Type: Integer}, {Name: "currency", Type: String},
				{Name: "payment_intent", Type: String}, {Name: "reason", Type: String}, {Name: "status", Type: String}},
		},
	},
}

var (
	stripeMetadata        = Field{Name: "metadata", Label: "Metadata", Type: Object, Open: true, Description: "Key-value pairs Stripe keeps with the object."}
	stripePaymentIntentID = Field{Name: "paymentIntent", Label: "PaymentIntent ID", Type: String, Required: true, Placeholder: "{{ steps.charge.id }}"}
	stripeCustomer        = []Field{
		{Name: "id", Type: String}, {Name: "email", Type: String}, {Name: "name", Type: String},
		{Name: "description", Type: String}, {Name: "metadata", Type: Object, Open: true}, {Name: "created", Type: Integer},
	}
	stripePaymentIntent = []Field{
		{Name: "id", Type: String},
		{Name: "status", Type: String, Enum: []string{"requires_payment_method", "requires_confirmation", "requires_action", "processing", "succeeded", "canceled"}},
		{Name: "amount", Type: Integer}, {Name: "amount_received", Type: Integer}, {Name: "currency", Type: String},
		{Name: "customer", Type: String}, {Name: "description", Type: String}, {Name: "payment_method", Type: String},
		{Name: "latest_charge", Type: String}, {Name: "metadata", Type: Object, Open: true}, {Name: "created", Type: Integer},
	}
)

// ServiceNow works with records in a ServiceNow instance's tables.
var ServiceNow = Connector{
	Type:       "servicenow",
	Label:      "ServiceNow",
	Connection: "Uses the worker's ServiceNow instance: SERVICENOW_URL, with SERVICENOW_USER and SERVICENOW_PASSWORD. A free Personal Developer Instance works.",
	Errors:     "Rate limits (429) and outages are retried. A missing record, a rejected login, or a data policy (a field a state requires) would fail again, so they aren't.",
	Catalog:    serviceNowCatalog,
	CatalogOf:  "table",
	Operations: []Operation{
		{
			Name: "query", Label: "Query records", Activity: "ServiceNowQuery", Summary: "Query {table}",
			Settings: []Field{snTable,
				{Name: "fields", Label: "Fields", Type: Array, Items: String, Placeholder: "number, short_description, state", Description: "Comma-separated. Empty returns every field."},
				{Name: "limit", Label: "At most", Type: Integer, Default: "20"}},
			Inputs: []Field{{Name: "query", Label: "Encoded query", Type: String, Placeholder: "active=true^priority=1^ORDERBYDESCsys_created_on",
				Description: "ServiceNow's encoded query: conditions joined by ^ (and) or ^OR. Empty returns every record, up to the limit."}},
			Outputs: []Field{{Name: "records", Type: Array, Catalog: "table", Open: true}, {Name: "count", Type: Integer}},
		},
		{
			Name: "get", Label: "Get record", Activity: "ServiceNowGetRecord", Summary: "Get {table}",
			Settings: []Field{snTable},
			Inputs:   []Field{snSysID},
			Outputs:  []Field{{Name: "record", Type: Object, Catalog: "table", Open: true}},
		},
		{
			Name: "create", Label: "Create record", Activity: "ServiceNowCreateRecord", Summary: "Create {table}",
			Settings: []Field{snTable},
			Inputs:   []Field{{Name: "values", Label: "Fields", Type: Object, Required: true, Catalog: "table", Creating: true, Open: true}},
			Outputs:  []Field{{Name: "record", Type: Object, Catalog: "table", Open: true, Description: "The new record, with its sys_id and number."}},
			UndoNote: "If the run fails later, the record this step created is deleted.",
			Undo: func(arg, out map[string]any) (string, map[string]any) {
				record, _ := out["record"].(map[string]any)
				return "delete", with(keep(arg, "table"), "sysId", record["sys_id"])
			},
		},
		{
			Name: "update", Label: "Update record", Activity: "ServiceNowUpdateRecord", Summary: "Update {table}",
			Settings: []Field{snTable},
			Inputs:   []Field{snSysID, {Name: "values", Label: "Fields", Type: Object, Required: true, Catalog: "table", Open: true}},
			Outputs: []Field{{Name: "record", Type: Object, Catalog: "table", Open: true},
				{Name: "previous", Type: Object, Catalog: "table", Open: true, Description: "The values the update replaced."}},
			UndoNote: "If the run fails later, the fields this step changed get back the values they had before it.",
			Undo: func(arg, out map[string]any) (string, map[string]any) {
				return "update", with(keep(arg, "table", "sysId"), "values", out["previous"])
			},
		},
		{
			Name: "delete", Label: "Delete record", Activity: "ServiceNowDeleteRecord", Summary: "Delete {table}",
			Settings: []Field{snTable},
			Inputs:   []Field{snSysID},
			Outputs:  []Field{{Name: "sysId", Type: String}, {Name: "deleted", Type: Boolean}, {Name: "alreadyDeleted", Type: Boolean}},
			Note:     "A record that is already gone counts as deleted, so a retried delete succeeds.",
		},
	},
}

var (
	snTable = Field{Name: "table", Label: "Table", Type: String, Required: true, Default: "incident",
		Suggestions: []string{"incident", "change_request", "problem", "sys_user"},
		Pattern:     `^[a-z][a-z0-9_]*$`, PatternHint: "a table name, such as incident"}
	snSysID = Field{Name: "sysId", Label: "sys_id", Type: String, Required: true, Placeholder: "{{ steps.incident.record.sys_id }}"}
)

// Slack posts to Slack channels as the worker's bot.
var Slack = Connector{
	Type:       "slack",
	Label:      "Slack",
	Connection: "Posts as the worker's Slack bot (SLACK_BOT_TOKEN, a bot token with chat:write). Invite the bot to a channel before posting there.",
	Errors:     "Failures take Slack's error as their type (channel_not_found, not_in_channel, …) and aren't retried. Rate limiting is: the next attempt waits as long as Slack's Retry-After asks.",
	Operations: []Operation{
		{
			Name: "post", Label: "Post message", Activity: "SlackPostMessage", Summary: "Post a message",
			Inputs: []Field{slackChannel,
				{Name: "text", Label: "Text", Type: String, Required: true, Multiline: true, Default: "{{ input.message }}", Description: "Slack mrkdwn: *bold*, _italic_, <https://…|links>."},
				{Name: "blocks", Label: "Blocks", Type: JSON, Description: "A Block Kit layout, as a JSON template; text is then the notification's fallback."},
				{Name: "threadTs", Label: "Reply in thread", Type: String, Placeholder: "{{ steps.announce.ts }}", Description: "A message's ts, to reply in its thread."},
			},
			Outputs:  slackMessage,
			UndoNote: "If the run fails later, the message is deleted.",
			Undo: func(arg, out map[string]any) (string, map[string]any) {
				return "delete", map[string]any{"channel": out["channel"], "ts": out["ts"]}
			},
		},
		{
			Name: "update", Label: "Update message", Activity: "SlackUpdateMessage", Summary: "Update a message",
			Inputs: []Field{slackChannel, slackTS,
				{Name: "text", Label: "Text", Type: String, Required: true, Multiline: true},
				{Name: "blocks", Label: "Blocks", Type: JSON}},
			Outputs: slackMessage,
		},
		{
			Name: "react", Label: "Add reaction", Activity: "SlackAddReaction", Summary: "Add a reaction",
			Inputs: []Field{slackChannel, slackTS,
				{Name: "name", Label: "Emoji", Type: String, Required: true, Default: "white_check_mark", Suggestions: []string{"white_check_mark", "eyes", "rotating_light", "tada"}}},
			Outputs: []Field{{Name: "ok", Type: Boolean}},
			Note:    "Reacting twice with the same emoji is fine: a retry finds it already there.",
		},
		{
			Name: "delete", Label: "Delete message", Activity: "SlackDeleteMessage", Summary: "Delete a message",
			Inputs:  []Field{slackChannel, slackTS},
			Outputs: []Field{{Name: "ok", Type: Boolean}},
		},
		{
			Name: "find_user", Label: "Find user by email", Activity: "SlackFindUser", Summary: "Find a user by email",
			Inputs: []Field{{Name: "email", Label: "Email", Type: String, Required: true, Default: "{{ input.email }}"}},
			Outputs: []Field{{Name: "found", Type: Boolean},
				{Name: "user", Type: Object, Description: "Mention them as <@id>.", Fields: []Field{{Name: "id", Type: String}, {Name: "name", Type: String}, {Name: "realName", Type: String}}}},
		},
	},
}

var (
	slackChannel = Field{Name: "channel", Label: "Channel", Type: String, Required: true, Default: "#general",
		Suggestions: []string{"#general", "#sales", "#incidents", "#ops"}, Description: "A #name, or a channel ID such as C024BE91L."}
	slackTS      = Field{Name: "ts", Label: "Message ts", Type: String, Required: true, Placeholder: "{{ steps.announce.ts }}"}
	slackMessage = []Field{
		{Name: "channel", Type: String, Description: "The channel's ID, which updating or reacting to the message needs."},
		{Name: "ts", Type: String, Description: "The message's timestamp: its ID within the channel."},
	}
)

// GitHub works with a GitHub repository's issues and Actions.
var GitHub = Connector{
	Type:       "github",
	Label:      "GitHub",
	Connection: "Uses the worker's GitHub token (GITHUB_TOKEN, a fine-grained personal access token with access to the repositories).",
	Errors:     "Rate limits are retried when GitHub says the limit resets (or after its Retry-After), and outages are retried. A missing repository (often a token without access), a validation error or bad credentials aren't.",
	Operations: []Operation{
		{
			Name: "create_issue", Label: "Create issue", Activity: "GitHubCreateIssue", Summary: "Create issue in {repo}",
			Settings: []Field{ghRepo},
			Inputs: []Field{
				{Name: "title", Label: "Title", Type: String, Required: true, Default: "{{ input.title }}"},
				{Name: "body", Label: "Body", Type: String, Multiline: true, Description: "Markdown."},
				{Name: "labels", Label: "Labels", Type: Array, Items: String, Placeholder: "bug, incident"},
				{Name: "assignees", Label: "Assignees", Type: Array, Items: String, Placeholder: "octocat"},
			},
			Outputs:  ghIssue,
			UndoNote: "If the run fails later, the issue is closed as not planned (GitHub doesn't delete issues).",
			Undo: func(arg, out map[string]any) (string, map[string]any) {
				return "update_issue", map[string]any{"repo": arg["repo"], "number": out["number"], "state": "closed", "stateReason": "not_planned"}
			},
		},
		{
			Name: "comment", Label: "Comment on issue or PR", Activity: "GitHubComment", Summary: "Comment in {repo}",
			Settings: []Field{ghRepo},
			Inputs:   []Field{ghNumber, {Name: "body", Label: "Comment", Type: String, Required: true, Multiline: true}},
			Outputs:  []Field{{Name: "id", Type: Integer}, {Name: "url", Type: String}},
		},
		{
			Name: "update_issue", Label: "Update issue", Activity: "GitHubUpdateIssue", Summary: "Update issue in {repo}",
			Settings: []Field{ghRepo},
			Inputs: []Field{ghNumber,
				{Name: "state", Label: "State", Type: String, Enum: []string{"open", "closed"}},
				{Name: "stateReason", Label: "Reason", Type: String, Enum: []string{"completed", "not_planned", "reopened"}},
				{Name: "title", Label: "Title", Type: String},
				{Name: "body", Label: "Body", Type: String, Multiline: true},
				{Name: "labels", Label: "Labels", Type: Array, Items: String, Description: "Replaces the issue's labels."},
			},
			Outputs: ghIssue,
			Note:    "Only the fields you map change.",
		},
		{
			Name: "get_issue", Label: "Get issue or PR", Activity: "GitHubGetIssue", Summary: "Get issue in {repo}",
			Settings: []Field{ghRepo},
			Inputs:   []Field{ghNumber},
			Outputs:  append(append([]Field{}, ghIssue...), Field{Name: "body", Type: String}, Field{Name: "comments", Type: Integer}),
		},
		{
			Name: "search_issues", Label: "Search issues and PRs", Activity: "GitHubSearchIssues", Summary: "Search issues",
			Inputs: []Field{{Name: "query", Label: "Search", Type: String, Required: true, Default: "repo:acme/storefront is:issue is:open label:bug",
				Description: "GitHub search syntax: repo:, is:open, label:, author:, …"}},
			Outputs: []Field{{Name: "totalCount", Type: Integer}, {Name: "items", Type: Array, Fields: ghIssue}},
		},
		{
			Name: "dispatch_workflow", Label: "Run Actions workflow", Activity: "GitHubDispatchWorkflow", Summary: "Run {workflow} in {repo}",
			Settings: []Field{ghRepo,
				{Name: "workflow", Label: "Workflow file", Type: String, Required: true, Default: "deploy.yml"},
				{Name: "ref", Label: "Branch or tag", Type: String, Required: true, Default: "main"}},
			Inputs: []Field{{Name: "inputs", Label: "Workflow inputs", Type: Object, Open: true,
				Description: "The workflow's workflow_dispatch inputs, sent as strings. Add one per input it declares."}},
			Outputs: []Field{{Name: "dispatched", Type: Boolean}, {Name: "workflow", Type: String}, {Name: "ref", Type: String}},
			Note:    "GitHub starts the workflow and answers at once. To wait for it, pass a callback URL as an input, have the workflow's last step POST its result there, and follow this step with a Wait for signal.",
		},
	},
}

var (
	ghRepo = Field{Name: "repo", Label: "Repository", Type: String, Required: true, Default: "acme/storefront",
		Pattern: `^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`, PatternHint: "owner/name"}
	ghNumber = Field{Name: "number", Label: "Issue or PR number", Type: Integer, Required: true, Placeholder: "{{ steps.issue.number }}"}
	ghIssue  = []Field{
		{Name: "number", Type: Integer}, {Name: "url", Type: String}, {Name: "title", Type: String},
		{Name: "state", Type: String, Enum: []string{"open", "closed"}}, {Name: "stateReason", Type: String},
		{Name: "labels", Type: Array, Items: String}, {Name: "pullRequest", Type: Boolean},
	}
)
