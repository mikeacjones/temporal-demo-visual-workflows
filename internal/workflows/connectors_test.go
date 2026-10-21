package workflows

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/connectors"
	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/examples"
	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/githubmock"
	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/salesforcemock"
	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/servicenowmock"
	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/slackmock"
	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/stripemock"
)

// sandbox is the connectors' stand-in services on one test server.
type sandbox struct {
	env        *testsuite.TestWorkflowEnvironment
	acts       *Activities
	salesforce *salesforcemock.Org
	stripe     *stripemock.Account
	servicenow *servicenowmock.Instance
	slack      *slackmock.Workspace
	github     *githubmock.GitHub
	// sfWrites counts the Salesforce org's write requests.
	sfWrites atomic.Int32
}

func newSandbox(t *testing.T) *sandbox {
	t.Helper()
	salesforcemock.ReadLatency, salesforcemock.WriteLatency = 0, 0
	stripemock.Latency, servicenowmock.Latency, slackmock.Latency, githubmock.Latency = 0, 0, 0, 0
	githubmock.RunDuration = 10 * time.Millisecond
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sb := &sandbox{
		salesforce: salesforcemock.New(logger), stripe: stripemock.New(logger), servicenow: servicenowmock.New(logger),
		slack: slackmock.New(logger), github: githubmock.New(logger),
	}
	sfAPI := sb.salesforce.Handler("/salesforce")
	mux := http.NewServeMux()
	mux.Handle("/salesforce/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && !strings.Contains(r.URL.Path, "oauth2") {
			sb.sfWrites.Add(1)
		}
		sfAPI.ServeHTTP(w, r)
	}))
	mux.Handle("/stripe/", sb.stripe.Handler("/stripe"))
	mux.Handle("/servicenow/", sb.servicenow.Handler("/servicenow"))
	mux.Handle("/slack/", sb.slack.Handler("/slack"))
	mux.Handle("/github/", sb.github.Handler("/github"))
	mux.HandleFunc("/fail", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusPaymentRequired) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	baseURL = srv.URL

	sb.acts = &Activities{HTTP: srv.Client()}
	connect(sb.acts, func(k string) string { return map[string]string{"SANDBOX_URL": srv.URL}[k] })
	var s testsuite.WorkflowTestSuite
	sb.env = s.NewTestWorkflowEnvironment()
	sb.env.RegisterActivity(sb.acts)
	sb.env.SetTestTimeout(10 * time.Second)
	return sb
}

// connector is a connector step.
func connector(id, stepType, operation string, params map[string]any) flow.Node {
	return node(id, flow.NodeType(stepType), flow.ConnectorConfig{Operation: operation, Params: params})
}

func withUndo(n flow.Node) flow.Node {
	var c flow.ConnectorConfig
	_ = json.Unmarshal(n.Config, &c)
	c.Undo = true
	n.Config, _ = json.Marshal(c)
	return n
}

func withRetry(n flow.Node, attempts int) flow.Node {
	var c flow.ConnectorConfig
	_ = json.Unmarshal(n.Config, &c)
	c.Activity.Retry = flow.RetryConfig{MaxAttempts: attempts, InitialIntervalSeconds: 1, BackoffCoefficient: 1}
	n.Config, _ = json.Marshal(c)
	return n
}

func chain(ids ...string) []flow.Edge {
	edges := []flow.Edge{edge("start", ids[0], "")}
	for i := 1; i < len(ids); i++ {
		edges = append(edges, edge(ids[i-1], ids[i], ""))
	}
	return edges
}

func definition(nodes ...flow.Node) flow.Definition {
	return flow.Definition{ID: "f", Name: "connectors", Nodes: append([]flow.Node{node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"})}, nodes...)}
}

func output(t *testing.T, st flow.RunState, id string) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(st.Nodes[id].Output, &out), id)
	return out
}

func (sb *sandbox) run(t *testing.T, def flow.Definition, input map[string]any) (flow.RunState, error) {
	t.Helper()
	require.Empty(t, def.Validate())
	sb.env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def, Input: input})
	var st flow.RunState
	if err := sb.env.GetWorkflowError(); err != nil {
		return st, err
	}
	require.NoError(t, sb.env.GetWorkflowResult(&st))
	return st, nil
}

func TestEveryOperationHasItsActivity(t *testing.T) {
	acts := reflect.TypeOf(&Activities{})
	for _, c := range connectors.All {
		for _, op := range c.Operations {
			m, ok := acts.MethodByName(op.Activity)
			require.True(t, ok, "%s %s: no Activity %s", c.Type, op.Name, op.Activity)
			require.Equal(t, 3, m.Type.NumIn(), op.Activity)
			arg := m.Type.In(2)
			for _, p := range op.Params {
				found := false
				for i := range arg.NumField() {
					if strings.Split(arg.Field(i).Tag.Get("json"), ",")[0] == p.Key {
						found = true
					}
				}
				require.True(t, found, "%s has no field for %s", arg.Name(), p.Key)
			}
			if op.Undo != nil {
				undoOp, _ := op.Undo(map[string]any{}, map[string]any{})
				_, ok := c.Operation(undoOp)
				require.True(t, ok, "%s %s undoes with unknown operation %s", c.Type, op.Name, undoOp)
			}
		}
	}
}

func TestSalesforceStepsReadAndWrite(t *testing.T) {
	sb := newSandbox(t)
	def := definition(
		connector("account", "salesforce", "upsert", map[string]any{"object": "Account", "externalIdField": "Domain__c", "externalId": "{{ input.domain }}", "values": `{"Name": "{{ input.company }}"}`}),
		connector("contact", "salesforce", "create", map[string]any{"object": "Contact", "values": `{"AccountId": "{{ steps.account.id }}", "LastName": "{{ input.lastName }}", "Email": "{{ input.email }}"}`}),
		connector("found", "salesforce", "query", map[string]any{"query": "SELECT Id, Email, Account.Name FROM Contact WHERE AccountId = '{{ steps.account.id }}'"}),
		connector("fetch", "salesforce", "get", map[string]any{"object": "Contact", "recordId": "{{ steps.contact.id }}", "fields": "LastName, Email"}),
		connector("rename", "salesforce", "update", map[string]any{"object": "Account", "recordId": "{{ steps.account.id }}", "values": `{"Name": "Globex Corporation"}`}),
	)
	def.Edges = chain("account", "contact", "found", "fetch", "rename")
	st, err := sb.run(t, def, map[string]any{"company": "Globex", "domain": "globex.com", "lastName": "Scorpio", "email": "hank@globex.com"})
	require.NoError(t, err)

	require.Equal(t, true, output(t, st, "account")["created"])
	found := output(t, st, "found")
	require.EqualValues(t, 1, found["totalSize"])
	// Records come without Salesforce's attributes, relationships included.
	require.Equal(t, []any{map[string]any{"Id": output(t, st, "contact")["id"], "Email": "hank@globex.com", "Account": map[string]any{"Name": "Globex"}}}, found["records"])
	require.Equal(t, map[string]any{"LastName": "Scorpio", "Email": "hank@globex.com"}, output(t, st, "fetch"))
	require.Equal(t, map[string]any{"Name": "Globex"}, output(t, st, "rename")["previous"])
	require.Equal(t, "Globex Corporation", sb.salesforce.Records("Account")[6]["Name"])
}

func TestSalesforceValidationErrorsAreNotRetried(t *testing.T) {
	sb := newSandbox(t)
	def := definition(
		withRetry(connector("opp", "salesforce", "create", map[string]any{"object": "Opportunity", "values": `{"Name": "No stage"}`}), 5),
		node("handled", flow.TypeTransform, flow.TransformConfig{Output: `{"error": "{{ steps.opp.error }}"}`}),
	)
	def.Edges = []flow.Edge{edge("start", "opp", ""), edge("opp", "handled", flow.HandleError)}
	st, err := sb.run(t, def, nil)
	require.NoError(t, err)
	require.Equal(t, flow.HandleError, st.Nodes["opp"].Handle)
	require.Contains(t, string(st.Nodes["handled"].Output), "REQUIRED_FIELD_MISSING")
	require.EqualValues(t, 1, sb.sfWrites.Load())
}

func TestSalesforceGetsANewTokenWhenTheSessionExpires(t *testing.T) {
	sb := newSandbox(t)
	sb.acts.Salesforce.token, sb.acts.Salesforce.instanceURL = "expired", sb.acts.Salesforce.URL
	def := definition(withRetry(connector("count", "salesforce", "query", map[string]any{"query": "SELECT COUNT() FROM Account"}), 1))
	def.Edges = chain("count")
	st, err := sb.run(t, def, nil)
	require.NoError(t, err)
	require.EqualValues(t, 6, output(t, st, "count")["totalSize"])
	require.NotEqual(t, "expired", sb.acts.Salesforce.token)
}

func TestStripeChargesOnceAndRefundsOnUndo(t *testing.T) {
	sb := newSandbox(t)
	def := definition(
		connector("find", "stripe", "find_customer", map[string]any{"email": "{{ input.email }}"}),
		withUndo(connector("customer", "stripe", "create_customer", map[string]any{"email": "{{ input.email }}", "name": "Hank", "metadata": `{"plan": "{{ input.plan }}"}`})),
		withUndo(connector("charge", "stripe", "charge", map[string]any{"amount": "{{ input.amount * 100 }}", "currency": "usd", "customer": "{{ steps.customer.id }}", "paymentMethod": "pm_card_visa"})),
		node("fail", flow.TypeHTTP, flow.HTTPConfig{Method: "POST", URL: baseURL + "/fail"}),
	)
	def.Edges = chain("find", "customer", "charge", "fail")
	_, err := sb.run(t, def, map[string]any{"email": "hank@globex.com", "plan": "pro", "amount": 48.55})
	require.Error(t, err)

	// The charge was refunded and the customer deleted, newest first.
	intents := sb.stripe.Objects("payment_intent")
	require.Len(t, intents, 1)
	require.EqualValues(t, 4855, intents[0]["amount"])
	require.EqualValues(t, 4855, sb.stripe.Refunded(intents[0]["id"].(string)))
	require.Empty(t, sb.stripe.Objects("customer"))
}

func TestStripeDeclineIsNotRetried(t *testing.T) {
	sb := newSandbox(t)
	def := definition(withRetry(connector("charge", "stripe", "charge", map[string]any{"amount": "5000", "currency": "usd", "paymentMethod": "pm_card_chargeDeclined"}), 5))
	def.Edges = chain("charge")
	_, err := sb.run(t, def, nil)
	require.ErrorContains(t, err, "card_declined")
	require.ErrorContains(t, err, "generic_decline")
	require.Len(t, sb.stripe.Objects("payment_intent"), 1, "one attempt")
}

func TestServiceNowIncidentLifecycle(t *testing.T) {
	sb := newSandbox(t)
	def := definition(
		withUndo(connector("incident", "servicenow", "create", map[string]any{"table": "incident", "values": `{"short_description": "{{ input.summary }}", "urgency": "{{ input.severity }}", "impact": 2}`})),
		connector("open", "servicenow", "query", map[string]any{"table": "incident", "query": "active=true^priority<=2^ORDERBYDESCnumber", "fields": "number, priority"}),
		withUndo(connector("resolve", "servicenow", "update", map[string]any{"table": "incident", "sysId": "{{ steps.incident.sys_id }}", "values": `{"state": "6", "close_code": "Solved (Permanently)", "close_notes": "Fixed"}`})),
		connector("again", "servicenow", "get", map[string]any{"table": "incident", "sysId": "{{ steps.incident.sys_id }}"}),
	)
	def.Edges = chain("incident", "open", "resolve", "again")
	st, err := sb.run(t, def, map[string]any{"summary": "Checkout down", "severity": 1})
	require.NoError(t, err)

	inc := output(t, st, "incident")
	require.Equal(t, "INC0010005", inc["number"])
	require.Equal(t, "2", inc["priority"], "urgency 1 and impact 2")
	open := output(t, st, "open")
	require.Equal(t, []any{map[string]any{"number": "INC0010005", "priority": "2"}}, open["records"], "the open P1s and P2s, newest first")
	require.Equal(t, map[string]any{"state": "1", "close_code": "", "close_notes": ""}, output(t, st, "resolve")["previous"])
	require.NotEmpty(t, output(t, st, "again")["resolved_at"])
}

func TestServiceNowDataPolicyIsNotRetried(t *testing.T) {
	sb := newSandbox(t)
	incident := sb.servicenow.Records("incident")[0]
	def := definition(withRetry(connector("resolve", "servicenow", "update", map[string]any{"table": "incident", "sysId": incident["sys_id"], "values": `{"state": "6"}`}), 5))
	def.Edges = chain("resolve")
	_, err := sb.run(t, def, nil)
	require.ErrorContains(t, err, "ServiceNowForbidden")
	require.ErrorContains(t, err, "Resolution code, Resolution notes")
}

func TestSlackPostsUpdatesAndUndoes(t *testing.T) {
	sb := newSandbox(t)
	def := definition(
		connector("who", "slack", "find_user", map[string]any{"email": "riley@example.com"}),
		withUndo(connector("post", "slack", "post", map[string]any{"channel": "#incidents", "text": "Hi <@{{ steps.who.user.id }}>"})),
		connector("react", "slack", "react", map[string]any{"channel": "{{ steps.post.channel }}", "ts": "{{ steps.post.ts }}", "name": "eyes"}),
		connector("edit", "slack", "update", map[string]any{"channel": "{{ steps.post.channel }}", "ts": "{{ steps.post.ts }}", "text": "Edited"}),
		withUndo(connector("other", "slack", "post", map[string]any{"channel": "#ops", "text": "To be undone"})),
		node("fail", flow.TypeHTTP, flow.HTTPConfig{Method: "POST", URL: baseURL + "/fail"}),
	)
	def.Edges = chain("who", "post", "react", "edit", "other", "fail")
	_, err := sb.run(t, def, nil)
	require.Error(t, err)
	// Both posts were undone, and only those.
	require.Empty(t, sb.slack.Messages())
}

func TestSlackErrorsKeepTheirCodes(t *testing.T) {
	sb := newSandbox(t)
	def := definition(
		withRetry(connector("post", "slack", "post", map[string]any{"channel": "#random", "text": "hello"}), 5),
		node("handled", flow.TypeTransform, flow.TransformConfig{Output: `{"error": "{{ steps.post.error }}"}`}),
	)
	def.Edges = []flow.Edge{edge("start", "post", ""), edge("post", "handled", flow.HandleError)}
	st, err := sb.run(t, def, nil)
	require.NoError(t, err)
	require.Contains(t, string(st.Nodes["handled"].Output), "not_in_channel")
}

func TestSlackRateLimitWaitsForRetryAfter(t *testing.T) {
	err := slackRateLimited(t)
	var appErr *temporal.ApplicationError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, "ratelimited", appErr.Type())
	require.Equal(t, time.Second, appErr.NextRetryDelay())
}

// slackRateLimited posts past a channel's burst and returns the error.
func slackRateLimited(t *testing.T) error {
	sb := newSandbox(t)
	slackmock.PerChannelBurst = 0
	t.Cleanup(func() { slackmock.PerChannelBurst = 5 })
	var s testsuite.WorkflowTestSuite
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(sb.acts)
	_, err := env.ExecuteActivity(sb.acts.SlackPostMessage, SlackRequest{Channel: "#ops", Text: "hi"})
	return err
}

func TestGitHubIssueAndWorkflowDispatch(t *testing.T) {
	sb := newSandbox(t)
	def := definition(
		withUndo(connector("issue", "github", "create_issue", map[string]any{"repo": "acme/storefront", "title": "Checkout down", "labels": "incident, sev1", "assignees": "riley"})),
		connector("comment", "github", "comment", map[string]any{"repo": "acme/storefront", "number": "{{ steps.issue.number }}", "body": "Rolling back"}),
		connector("search", "github", "search_issues", map[string]any{"query": "repo:acme/storefront is:open label:incident"}),
		connector("deploy", "github", "dispatch_workflow", map[string]any{"repo": "acme/storefront", "workflow": "rollback.yml", "ref": "main", "inputs": `{"service": "Storefront", "callback_url": "{{ run.callbackUrl }}/events/done"}`}),
		connector("close", "github", "update_issue", map[string]any{"repo": "acme/storefront", "number": "{{ steps.issue.number }}", "state": "closed"}),
	)
	def.Edges = chain("issue", "comment", "search", "deploy", "close")
	st, err := sb.run(t, def, nil)
	require.NoError(t, err)

	issue := output(t, st, "issue")
	require.EqualValues(t, 5, issue["number"])
	require.Equal(t, []any{"incident", "sev1"}, issue["labels"])
	require.Equal(t, "https://github.com/acme/storefront/issues/5", issue["url"])
	require.EqualValues(t, 1, output(t, st, "search")["totalCount"])
	require.Equal(t, true, output(t, st, "deploy")["dispatched"])
	closed := output(t, st, "close")
	require.Equal(t, "closed", closed["state"])
	require.Equal(t, "completed", closed["stateReason"])
}

func TestGitHubErrors(t *testing.T) {
	sb := newSandbox(t)
	def := definition(
		withRetry(connector("missing", "github", "create_issue", map[string]any{"repo": "acme/nope", "title": "x"}), 5),
		node("handled", flow.TypeTransform, flow.TransformConfig{Output: `{"error": "{{ steps.missing.error }}"}`}),
		withRetry(connector("bad_input", "github", "dispatch_workflow", map[string]any{"repo": "acme/storefront", "workflow": "deploy.yml", "ref": "main", "inputs": `{"colour": "blue"}`}), 5),
	)
	def.Edges = []flow.Edge{edge("start", "missing", ""), edge("missing", "handled", flow.HandleError), edge("handled", "bad_input", "")}
	_, err := sb.run(t, def, nil)
	require.ErrorContains(t, err, "GitHubValidationFailed")
	require.ErrorContains(t, err, `Unexpected inputs provided: ["colour"]`)

	resp := apiResponse{Status: 403, Header: http.Header{"X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset": {"1000030"}}, Body: []byte(`{"message": "API rate limit exceeded"}`)}
	var appErr *temporal.ApplicationError
	require.ErrorAs(t, gitHubError("GET", "/x", resp, time.Unix(1_000_000, 0)), &appErr)
	require.Equal(t, "GitHubRateLimited", appErr.Type())
	require.Equal(t, 30*time.Second, appErr.NextRetryDelay())
	require.False(t, appErr.NonRetryable())
}

func TestSalesforceErrorsKeepTheirCodes(t *testing.T) {
	for _, tc := range []struct {
		status    int
		body      string
		code      string
		retryable bool
	}{
		{400, `[{"errorCode":"UNABLE_TO_LOCK_ROW","message":"unable to obtain exclusive access"}]`, "UNABLE_TO_LOCK_ROW", true},
		{403, `[{"errorCode":"REQUEST_LIMIT_EXCEEDED","message":"TotalRequests Limit exceeded."}]`, "REQUEST_LIMIT_EXCEEDED", true},
		{503, `[{"errorCode":"SERVER_UNAVAILABLE","message":"down for maintenance"}]`, "SERVER_UNAVAILABLE", true},
		{400, `[{"errorCode":"REQUIRED_FIELD_MISSING","message":"Required fields are missing: [Name]","fields":["Name"]}]`, "REQUIRED_FIELD_MISSING", false},
		{404, `[{"errorCode":"NOT_FOUND","message":"The requested resource does not exist"}]`, "NOT_FOUND", false},
		{502, `bad gateway`, "SalesforceError", true},
	} {
		var appErr *temporal.ApplicationError
		require.ErrorAs(t, salesforceError("GET", "/x", tc.status, []byte(tc.body)), &appErr)
		require.Equal(t, tc.code, appErr.Type())
		require.Equal(t, !tc.retryable, appErr.NonRetryable(), tc.code)
	}
}

func TestConnectorValidation(t *testing.T) {
	for _, tc := range []struct {
		node    flow.Node
		problem string
	}{
		{connector("sf", "salesforce", "merge", nil), `no operation "merge"`},
		{connector("sf", "salesforce", "query", nil), "needs soql"},
		{connector("sf", "salesforce", "get", map[string]any{"object": "Account"}), "needs record id"},
		{connector("sf", "salesforce", "create", map[string]any{"object": "Account", "values": `{"Name": `}), "must be a JSON template"},
		{connector("sf", "salesforce", "create", map[string]any{"object": "Bad Name", "values": `{}`}), "must be an sObject's API name"},
		{withUndo(connector("sf", "salesforce", "delete", map[string]any{"object": "Account", "recordId": "x"})), "can't be undone"},
		{connector("gh", "github", "create_issue", map[string]any{"repo": "storefront", "title": "x"}), "must be owner/name"},
		{connector("st", "stripe", "charge", map[string]any{"currency": "usd", "paymentMethod": "pm_card_visa"}), "needs amount"},
	} {
		def := definition(tc.node)
		def.Edges = chain(tc.node.ID)
		problems := def.Validate()
		require.Len(t, problems, 1, tc.problem)
		require.Contains(t, problems[0].Message, tc.problem)
	}
	// A template is checked once rendered, not against the pattern.
	def := definition(connector("gh", "github", "create_issue", map[string]any{"repo": "{{ input.repo }}", "title": "x"}))
	def.Edges = chain("gh")
	require.Empty(t, def.Validate())
}

func TestConnectorExamples(t *testing.T) {
	flows := func() map[string]flow.Definition { return examples.Flows(baseURL, time.Now()) }

	t.Run("new customer", func(t *testing.T) {
		sb := newSandbox(t)
		st, err := sb.run(t, flows()["new-customer"], map[string]any{
			"company": "Acme Corporation", "domain": "acme.com", "firstName": "Bugs", "lastName": "Bunny",
			"email": "bugs@acme.com", "plan": "Enterprise", "amount": 480, "paymentMethod": "pm_card_visa",
		})
		require.NoError(t, err)
		require.Equal(t, false, output(t, st, "account")["created"], "Acme exists, so the upsert updates it")
		opps := sb.salesforce.Records("Opportunity")
		won := opps[len(opps)-1]
		require.Equal(t, "Acme Corporation — Enterprise", won["Name"])
		require.EqualValues(t, 480, won["Amount"])
		require.Regexp(t, `^\d{4}-\d{2}-\d{2}$`, won["CloseDate"])
		require.Equal(t, "succeeded", output(t, st, "charge")["status"])
		msgs := sb.slack.Messages()
		require.Len(t, msgs, 1)
		require.Equal(t, "sales", msgs[0].Name)
		require.Contains(t, msgs[0].Text, "2 open support cases")
	})

	t.Run("card declined", func(t *testing.T) {
		sb := newSandbox(t)
		opps, contacts := len(sb.salesforce.Records("Opportunity")), len(sb.salesforce.Records("Contact"))
		_, err := sb.run(t, flows()["new-customer"], map[string]any{
			"company": "Globex", "domain": "globex.com", "firstName": "Hank", "lastName": "Scorpio",
			"email": "hank@globex.com", "plan": "Enterprise", "amount": 480, "paymentMethod": "pm_card_chargeDeclined",
		})
		require.ErrorContains(t, err, "card_declined")
		require.Len(t, sb.salesforce.Records("Opportunity"), opps)
		require.Len(t, sb.salesforce.Records("Contact"), contacts)
		require.Empty(t, sb.stripe.Objects("customer"))
		require.Len(t, sb.salesforce.Records("Account"), 7, "the upsert has no undo: the account may have been there before")
	})

	t.Run("pipeline sweep", func(t *testing.T) {
		sb := newSandbox(t)
		st, err := sb.run(t, flows()["salesforce-pipeline-sweep"], map[string]any{})
		require.NoError(t, err)
		require.EqualValues(t, 5, output(t, st, "each")["succeeded"])
		priority := map[any]any{}
		for _, task := range sb.salesforce.Records("Task") {
			priority[task["Subject"]] = task["Priority"]
		}
		require.Len(t, priority, 5)
		require.Equal(t, "High", priority["Overdue: Umbrella — Lab automation"])
		require.Equal(t, "Normal", priority["Overdue: Acme — Rocket skates pilot"])
		require.Contains(t, sb.slack.Messages()[0].Text, "5 deals worth $586500")
	})

	t.Run("incident response", func(t *testing.T) {
		sb := newSandbox(t)
		def := flows()["incident-response"]
		require.Empty(t, def.Validate())
		// The sandbox's GitHub calls the run back; here the test does.
		sb.env.RegisterDelayedCallback(func() {
			sb.env.SignalWorkflow("rollback_finished", map[string]any{"conclusion": "success", "url": "https://github.com/acme/storefront/actions/runs/1"})
		}, time.Minute)
		st, err := sb.run(t, def, map[string]any{"service": "Storefront", "summary": "Checkout error rate above 5%", "severity": 1, "version": "v2.14.0"})
		require.NoError(t, err)

		inc := sb.servicenow.Records("incident")
		last := inc[len(inc)-1]
		require.Equal(t, "6", last["state"])
		require.Equal(t, "1", last["priority"])
		require.Contains(t, last["close_notes"], "actions/runs/1")
		issue := sb.github.Issues("acme/storefront")[4]
		require.Equal(t, "closed", issue["state"])
		msgs := sb.slack.Messages()
		require.Len(t, msgs, 1)
		require.True(t, msgs[0].Edited)
		require.Contains(t, msgs[0].Text, ":white_check_mark: *"+last["number"]+"* resolved")
		require.Equal(t, flow.NodeSkipped, st.Nodes["page"].Status)
	})
}
