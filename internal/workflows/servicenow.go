package workflows

import (
	"cmp"
	"context"
	"encoding/base64"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.temporal.io/sdk/activity"
)

// ServiceNowConnection is the worker's ServiceNow instance and login.
type ServiceNowConnection struct {
	URL      string // https://<instance>.service-now.com, or the sandbox's
	User     string
	Password string
	HTTP     *http.Client
}

// ServiceNowRequest is one Table API call.
type ServiceNowRequest struct {
	Table  string         `json:"table"`
	Query  string         `json:"query,omitempty"`
	Fields []string       `json:"fields,omitempty"`
	Limit  float64        `json:"limit,omitempty"`
	SysID  string         `json:"sysId,omitempty"`
	Values map[string]any `json:"values,omitempty"`
}

// maxServiceNowRecords bounds a query, to keep its result a comfortable size
// for history.
const maxServiceNowRecords = 1000

// ServiceNowQuery reads a table's records matching an encoded query:
// {records, count}.
func (a *Activities) ServiceNowQuery(ctx context.Context, req ServiceNowRequest) (map[string]any, error) {
	limit := int(req.Limit)
	if limit <= 0 {
		limit = 20
	}
	q := url.Values{"sysparm_limit": {strconv.Itoa(min(limit, maxServiceNowRecords))}}
	set(q, "sysparm_query", req.Query)
	set(q, "sysparm_fields", strings.Join(req.Fields, ","))
	var res struct {
		Result []any `json:"result"`
	}
	if err := a.callServiceNow(ctx, http.MethodGet, tablePath(req.Table, "")+"?"+q.Encode(), nil, &res); err != nil {
		return nil, err
	}
	if res.Result == nil {
		res.Result = []any{}
	}
	return map[string]any{"records": res.Result, "count": len(res.Result)}, nil
}

// ServiceNowGetRecord reads one record by sys_id: {record}.
func (a *Activities) ServiceNowGetRecord(ctx context.Context, req ServiceNowRequest) (map[string]any, error) {
	var res struct {
		Result map[string]any `json:"result"`
	}
	if err := a.callServiceNow(ctx, http.MethodGet, tablePath(req.Table, req.SysID), nil, &res); err != nil {
		return nil, err
	}
	return map[string]any{"record": res.Result}, nil
}

// ServiceNowCreateRecord inserts a record: {record}, with its sys_id and
// number.
func (a *Activities) ServiceNowCreateRecord(ctx context.Context, req ServiceNowRequest) (map[string]any, error) {
	var res struct {
		Result map[string]any `json:"result"`
	}
	if err := a.callServiceNow(ctx, http.MethodPost, tablePath(req.Table, ""), req.Values, &res); err != nil {
		return nil, err
	}
	activity.GetLogger(ctx).Info("servicenow record created", "table", req.Table, "number", res.Result["number"])
	return map[string]any{"record": res.Result}, nil
}

// ServiceNowUpdateRecord changes a record's fields: {record, previous}. It
// reads the fields it is about to change first, so an undo can put them back.
func (a *Activities) ServiceNowUpdateRecord(ctx context.Context, req ServiceNowRequest) (map[string]any, error) {
	path := tablePath(req.Table, req.SysID)
	var before struct {
		Result map[string]any `json:"result"`
	}
	fields := strings.Join(slices.Sorted(maps.Keys(req.Values)), ",")
	if err := a.callServiceNow(ctx, http.MethodGet, path+"?"+url.Values{"sysparm_fields": {fields}}.Encode(), nil, &before); err != nil {
		return nil, err
	}
	var after struct {
		Result map[string]any `json:"result"`
	}
	if err := a.callServiceNow(ctx, http.MethodPatch, path, req.Values, &after); err != nil {
		return nil, err
	}
	if before.Result == nil {
		before.Result = map[string]any{}
	}
	return map[string]any{"record": after.Result, "previous": before.Result}, nil
}

// ServiceNowDeleteRecord deletes a record. One that is already gone counts as
// deleted, so a retried delete (or an undo) succeeds.
func (a *Activities) ServiceNowDeleteRecord(ctx context.Context, req ServiceNowRequest) (map[string]any, error) {
	err := a.callServiceNow(ctx, http.MethodDelete, tablePath(req.Table, req.SysID), nil, nil)
	if errorType(err) == "ServiceNowNotFound" {
		return map[string]any{"sysId": req.SysID, "deleted": true, "alreadyDeleted": true}, nil
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"sysId": req.SysID, "deleted": true}, nil
}

func tablePath(table, sysID string) string {
	p := "/api/now/table/" + url.PathEscape(table)
	if sysID != "" {
		p += "/" + url.PathEscape(sysID)
	}
	return p
}

func (a *Activities) callServiceNow(ctx context.Context, method, path string, body, out any) error {
	sn := a.ServiceNow
	if sn == nil || sn.URL == "" {
		return notConfigured("SERVICENOW_URL")
	}
	defer keepAlive(ctx, "waiting for ServiceNow: "+method+" "+truncate(path, 60))()
	var payload []byte
	if body != nil {
		var err error
		if payload, err = jsonBody(body); err != nil {
			return err
		}
	}
	header := http.Header{
		"Accept":        {"application/json"},
		"Authorization": {"Basic " + base64.StdEncoding.EncodeToString([]byte(sn.User+":"+sn.Password))},
	}
	resp, err := apiCall(ctx, sn.HTTP, method, sn.URL+path, header, "application/json", payload)
	if err != nil {
		return err
	}
	if resp.Status >= 200 && resp.Status < 300 {
		return resp.decodeInto("ServiceNow", out)
	}
	return serviceNowError(method, path, resp)
}

// serviceNowError: ServiceNow has no error codes, only statuses with a
// message and detail. Rate limiting (429, with Retry-After) and server errors
// are retried; a bad request, a failed login, a data policy or ACL (403), or
// a missing record are not.
func serviceNowError(method, path string, resp apiResponse) error {
	var body struct {
		Error struct {
			Message string `json:"message"`
			Detail  string `json:"detail"`
		} `json:"error"`
	}
	_ = resp.decodeInto("ServiceNow", &body)
	types := map[int]string{400: "ServiceNowBadRequest", 401: "ServiceNowAuthFailed", 403: "ServiceNowForbidden", 404: "ServiceNowNotFound", 429: "ServiceNowRateLimited"}
	errType := cmp.Or(types[resp.Status], "ServiceNowError")
	if resp.Status >= 500 {
		errType = "ServiceNowUnavailable"
	}
	detail := strings.TrimSpace(strings.Join(strings.Fields(cmp.Or(body.Error.Detail, string(resp.Body))), " "))
	msg := fmt.Sprintf("ServiceNow %d on %s %s: %s", resp.Status, method, truncate(path, 80), cmp.Or(body.Error.Message, http.StatusText(resp.Status)))
	if detail != "" {
		msg += ": " + truncate(detail, 200)
	}
	var delay time.Duration
	if d, ok := retryAfter(resp.Header.Get("Retry-After"), time.Now()); ok {
		delay = d
	}
	return connectorError(msg, errType, resp.Status == 429 || resp.Status >= 500, delay, body.Error)
}
