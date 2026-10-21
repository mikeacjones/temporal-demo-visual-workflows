package workflows

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

// SalesforceAPIVersion is the REST API version the connector speaks.
const SalesforceAPIVersion = "v62.0"

// SalesforceConnection is how the worker reaches a Salesforce org. It lives on
// the worker, not in flows: a Salesforce step names an operation and an
// object, so no credential appears in a flow, in its Activity inputs, or in
// Workflow history.
type SalesforceConnection struct {
	// URL is the org's My Domain URL. The OAuth 2.0 client credentials flow
	// posts to URL/services/oauth2/token.
	URL          string
	ClientID     string
	ClientSecret string
	// AccessToken, when set, is used as is, against URL, instead of the
	// client credentials flow.
	AccessToken string
	HTTP        *http.Client

	mu          sync.Mutex
	token       string
	instanceURL string
}

// SalesforceRequest is one Salesforce call. Each operation reads the fields
// it needs.
type SalesforceRequest struct {
	Object string `json:"object,omitempty"`
	// Query is SOQL, or the nextRecordsUrl of an earlier query's result,
	// to read its next page.
	Query string `json:"query,omitempty"`
	// AllPages follows nextRecordsUrl until every record is read, up to
	// maxQueryRecords.
	AllPages bool   `json:"allPages,omitempty"`
	RecordID string `json:"recordId,omitempty"`
	// Fields limits which fields a get returns; empty returns them all.
	Fields []string `json:"fields,omitempty"`
	// Values are the field values a create, update or upsert writes.
	Values          map[string]any `json:"values,omitempty"`
	ExternalIDField string         `json:"externalIdField,omitempty"`
	ExternalID      string         `json:"externalId,omitempty"`
}

// A query's result is stored in Workflow history and passed to later steps,
// so reading every page stops at whichever of these comes first. The result
// then carries nextRecordsUrl, and a Loop can read on from there.
const (
	maxQueryRecords = 2000
	maxQueryBytes   = 512 << 10
)

// SalesforceQuery runs a SOQL query: {totalSize, done, records}, plus
// nextRecordsUrl while records remain.
func (a *Activities) SalesforceQuery(ctx context.Context, req SalesforceRequest) (map[string]any, error) {
	sf, err := a.salesforceConnection()
	if err != nil {
		return nil, err
	}
	defer keepAlive(ctx, "waiting for Salesforce: query")()
	path := "/services/data/" + SalesforceAPIVersion + "/query?q=" + url.QueryEscape(req.Query)
	if strings.HasPrefix(req.Query, "/services/data/") {
		path = req.Query
	}
	records := []any{}
	var page struct {
		TotalSize      int    `json:"totalSize"`
		Done           bool   `json:"done"`
		NextRecordsURL string `json:"nextRecordsUrl"`
		Records        []any  `json:"records"`
	}
	read := 0
	for {
		raw, err := sf.call(ctx, http.MethodGet, path, nil, &page)
		if err != nil {
			return nil, err
		}
		read += len(raw)
		records = append(records, page.Records...)
		if page.Done || !req.AllPages || len(records) >= maxQueryRecords || read >= maxQueryBytes {
			break
		}
		path, page.Records = page.NextRecordsURL, nil
	}
	out := map[string]any{"totalSize": page.TotalSize, "done": page.Done, "records": stripAttributes(records)}
	if !page.Done {
		out["nextRecordsUrl"] = page.NextRecordsURL
	}
	activity.GetLogger(ctx).Info("salesforce query", "records", len(records), "totalSize", page.TotalSize)
	return out, nil
}

// SalesforceGetRecord reads one record by ID.
func (a *Activities) SalesforceGetRecord(ctx context.Context, req SalesforceRequest) (map[string]any, error) {
	sf, err := a.salesforceConnection()
	if err != nil {
		return nil, err
	}
	defer keepAlive(ctx, "waiting for Salesforce: get "+req.Object)()
	path := recordPath(req.Object, req.RecordID)
	if len(req.Fields) > 0 {
		path += "?fields=" + url.QueryEscape(strings.Join(req.Fields, ","))
	}
	var rec map[string]any
	if _, err := sf.call(ctx, http.MethodGet, path, nil, &rec); err != nil {
		return nil, err
	}
	return stripAttributes(rec).(map[string]any), nil
}

// SalesforceCreateRecord creates a record: {id, success}. Like any create,
// a retry after a lost response can create it twice; upsert by an external
// ID is the idempotent way to write.
func (a *Activities) SalesforceCreateRecord(ctx context.Context, req SalesforceRequest) (map[string]any, error) {
	sf, err := a.salesforceConnection()
	if err != nil {
		return nil, err
	}
	defer keepAlive(ctx, "waiting for Salesforce: create "+req.Object)()
	var res struct {
		ID string `json:"id"`
	}
	if _, err := sf.call(ctx, http.MethodPost, objectPath(req.Object), req.Values, &res); err != nil {
		return nil, err
	}
	activity.GetLogger(ctx).Info("salesforce record created", "object", req.Object, "id", res.ID)
	return map[string]any{"id": res.ID, "success": true}, nil
}

// SalesforceUpdateRecord changes a record's fields: {id, success, previous}.
// It reads the fields it is about to change first, so an undo can put them
// back.
func (a *Activities) SalesforceUpdateRecord(ctx context.Context, req SalesforceRequest) (map[string]any, error) {
	sf, err := a.salesforceConnection()
	if err != nil {
		return nil, err
	}
	defer keepAlive(ctx, "waiting for Salesforce: update "+req.Object)()
	path := recordPath(req.Object, req.RecordID)
	previous := map[string]any{}
	if len(req.Values) > 0 {
		fields := slices.Sorted(maps.Keys(req.Values))
		if _, err := sf.call(ctx, http.MethodGet, path+"?fields="+url.QueryEscape(strings.Join(fields, ",")), nil, &previous); err != nil {
			return nil, err
		}
		delete(previous, "attributes")
	}
	if _, err := sf.call(ctx, http.MethodPatch, path, req.Values, nil); err != nil {
		return nil, err
	}
	activity.GetLogger(ctx).Info("salesforce record updated", "object", req.Object, "id", req.RecordID)
	return map[string]any{"id": req.RecordID, "success": true, "previous": previous}, nil
}

// SalesforceUpsertRecord updates the record whose external ID field has the
// value, or creates it: {id, created}. Retrying it is always safe.
func (a *Activities) SalesforceUpsertRecord(ctx context.Context, req SalesforceRequest) (map[string]any, error) {
	sf, err := a.salesforceConnection()
	if err != nil {
		return nil, err
	}
	defer keepAlive(ctx, "waiting for Salesforce: upsert "+req.Object)()
	path := objectPath(req.Object) + "/" + url.PathEscape(req.ExternalIDField) + "/" + url.PathEscape(req.ExternalID)
	var res struct {
		ID      string `json:"id"`
		Created bool   `json:"created"`
	}
	if _, err := sf.call(ctx, http.MethodPatch, path, req.Values, &res); err != nil {
		return nil, err
	}
	activity.GetLogger(ctx).Info("salesforce record upserted", "object", req.Object, "id", res.ID, "created", res.Created)
	return map[string]any{"id": res.ID, "created": res.Created}, nil
}

// SalesforceDeleteRecord deletes a record: {id, deleted}. A record that is
// already gone counts as deleted, so a retried delete (or undo) succeeds.
func (a *Activities) SalesforceDeleteRecord(ctx context.Context, req SalesforceRequest) (map[string]any, error) {
	sf, err := a.salesforceConnection()
	if err != nil {
		return nil, err
	}
	defer keepAlive(ctx, "waiting for Salesforce: delete "+req.Object)()
	_, err = sf.call(ctx, http.MethodDelete, recordPath(req.Object, req.RecordID), nil, nil)
	var appErr *temporal.ApplicationError
	if errors.As(err, &appErr) && (appErr.Type() == "NOT_FOUND" || appErr.Type() == "ENTITY_IS_DELETED") {
		activity.GetLogger(ctx).Info("salesforce record was already deleted", "object", req.Object, "id", req.RecordID)
		return map[string]any{"id": req.RecordID, "deleted": true, "alreadyDeleted": true}, nil
	}
	if err != nil {
		return nil, err
	}
	activity.GetLogger(ctx).Info("salesforce record deleted", "object", req.Object, "id", req.RecordID)
	return map[string]any{"id": req.RecordID, "deleted": true}, nil
}

func (a *Activities) salesforceConnection() (*SalesforceConnection, error) {
	if a.Salesforce == nil || a.Salesforce.URL == "" {
		return nil, temporal.NewNonRetryableApplicationError("SALESFORCE_URL is not set on the worker", "SalesforceNotConfigured", nil)
	}
	return a.Salesforce, nil
}

func objectPath(object string) string {
	return "/services/data/" + SalesforceAPIVersion + "/sobjects/" + url.PathEscape(object)
}

func recordPath(object, id string) string {
	return objectPath(object) + "/" + url.PathEscape(id)
}

// call makes one API request and decodes a successful JSON answer into out.
// An expired session gets a new token and one more try, within the attempt.
// It returns the raw body.
func (c *SalesforceConnection) call(ctx context.Context, method, path string, body, out any) ([]byte, error) {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return nil, temporal.NewNonRetryableApplicationError(err.Error(), "InvalidRequest", nil)
		}
	}
	for try := 0; ; try++ {
		token, instance, err := c.session(ctx)
		if err != nil {
			return nil, err
		}
		var reader io.Reader
		if payload != nil {
			reader = strings.NewReader(string(payload))
		}
		req, err := http.NewRequestWithContext(ctx, method, instance+path, reader)
		if err != nil {
			return nil, temporal.NewNonRetryableApplicationError(err.Error(), "InvalidRequest", nil)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Sforce-Query-Options", "batchSize=200")
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.HTTP.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, err // network errors are worth retrying
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusUnauthorized && try == 0 && c.AccessToken == "" {
			c.forget(token)
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return raw, salesforceError(method, path, resp.StatusCode, raw)
		}
		if out != nil && len(raw) > 0 {
			if err := json.Unmarshal(raw, out); err != nil {
				return raw, fmt.Errorf("salesforce answered %s %s with unreadable JSON: %w", method, path, err)
			}
		}
		return raw, nil
	}
}

// session returns an access token and the instance URL to call, getting a
// token with the client credentials flow when there is none.
func (c *SalesforceConnection) session(ctx context.Context) (token, instance string, err error) {
	if c.AccessToken != "" {
		return c.AccessToken, strings.TrimRight(c.URL, "/"), nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" {
		return c.token, c.instanceURL, nil
	}
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {c.ClientID}, "client_secret": {c.ClientSecret}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.URL, "/")+"/services/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", temporal.NewNonRetryableApplicationError(err.Error(), "SalesforceNotConfigured", nil)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	var res struct {
		AccessToken string `json:"access_token"`
		InstanceURL string `json:"instance_url"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&res)
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		return "", "", temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Salesforce refused the worker's client credentials (%s: %s); check SALESFORCE_CLIENT_ID and SALESFORCE_CLIENT_SECRET", res.Error, res.Description),
			"SalesforceAuthFailed", nil)
	}
	if resp.StatusCode != http.StatusOK || res.AccessToken == "" {
		return "", "", fmt.Errorf("salesforce token endpoint returned %s", resp.Status)
	}
	c.token, c.instanceURL = res.AccessToken, strings.TrimRight(cmp.Or(res.InstanceURL, c.URL), "/")
	return c.token, c.instanceURL, nil
}

func (c *SalesforceConnection) forget(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token == token {
		c.token = ""
	}
}

// Salesforce error codes that mean "try again later", whatever the status.
var retryableSalesforceErrors = []string{"UNABLE_TO_LOCK_ROW", "REQUEST_LIMIT_EXCEEDED", "SERVER_UNAVAILABLE", "API_CURRENTLY_DISABLED"}

// salesforceError turns an error answer into an Activity error whose type is
// Salesforce's error code, so retry policies and the run view speak
// Salesforce. Lock contention, limits and outages are retried; anything else
// in the 4xx range (a missing field, a validation rule, a bad query) would
// fail the same way again, so it isn't.
func salesforceError(method, path string, status int, raw []byte) error {
	var errs []struct {
		ErrorCode string   `json:"errorCode"`
		Message   string   `json:"message"`
		Fields    []string `json:"fields,omitempty"`
	}
	_ = json.Unmarshal(raw, &errs)
	code, message := "SalesforceError", strings.TrimSpace(truncate(string(raw), 200))
	if len(errs) > 0 {
		code, message = errs[0].ErrorCode, errs[0].Message
	}
	if status == http.StatusMultipleChoices {
		code, message = "MULTIPLE_CHOICES", "more than one record has this external ID"
	}
	msg := fmt.Sprintf("Salesforce %s on %s %s: %s", code, method, truncate(path, 120), message)
	if status >= 500 || status == 408 || status == 429 || slices.Contains(retryableSalesforceErrors, code) {
		return temporal.NewApplicationError(msg, code, errs)
	}
	return temporal.NewNonRetryableApplicationError(msg, code, nil, errs)
}

// stripAttributes drops the "attributes" (type and URL) Salesforce adds to
// every record, at any depth, so later steps see only fields.
func stripAttributes(v any) any {
	switch x := v.(type) {
	case map[string]any:
		delete(x, "attributes")
		for k, child := range x {
			x[k] = stripAttributes(child)
		}
	case []any:
		for i, child := range x {
			x[i] = stripAttributes(child)
		}
	}
	return v
}
