package salesforcemock

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type client struct {
	t     *testing.T
	base  string
	token string
}

func newOrg(t *testing.T) (*Org, *client) {
	t.Helper()
	ReadLatency, WriteLatency = 0, 0
	org := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	mux.Handle("/salesforce/", org.Handler("/salesforce"))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := &client{t: t, base: srv.URL + "/salesforce"}
	resp, err := http.PostForm(c.base+"/services/oauth2/token", url.Values{
		"grant_type": {"client_credentials"}, "client_id": {ClientID}, "client_secret": {ClientSecret},
	})
	require.NoError(t, err)
	var tok struct {
		AccessToken string `json:"access_token"`
		InstanceURL string `json:"instance_url"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&tok))
	require.Equal(t, c.base, tok.InstanceURL)
	c.token = tok.AccessToken
	return org, c
}

func (c *client) do(method, path string, body string) (int, any) {
	c.t.Helper()
	req, _ := http.NewRequest(method, c.base+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(c.t, err)
	defer resp.Body.Close()
	var out any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func (c *client) query(soql string) (int, map[string]any) {
	status, out := c.do("GET", "/services/data/v62.0/query?q="+url.QueryEscape(soql), "")
	m, _ := out.(map[string]any)
	return status, m
}

func errorCode(out any) string {
	errs, _ := out.([]any)
	if len(errs) == 0 {
		return ""
	}
	return errs[0].(map[string]any)["errorCode"].(string)
}

func TestTokens(t *testing.T) {
	_, c := newOrg(t)
	resp, err := http.PostForm(c.base+"/services/oauth2/token", url.Values{"grant_type": {"client_credentials"}, "client_id": {"x"}, "client_secret": {"y"}})
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	c.token = "expired"
	status, out := c.do("GET", "/services/data/v62.0/query?q=SELECT+Id+FROM+Account", "")
	require.Equal(t, http.StatusUnauthorized, status)
	require.Equal(t, "INVALID_SESSION_ID", errorCode(out))
}

func TestQueries(t *testing.T) {
	_, c := newOrg(t)

	status, res := c.query("SELECT Id, Name, Amount, Account.Name FROM Opportunity WHERE IsClosed = false AND CloseDate < TODAY ORDER BY Amount DESC")
	require.Equal(t, http.StatusOK, status)
	require.EqualValues(t, 5, res["totalSize"])
	first := res["records"].([]any)[0].(map[string]any)
	require.Equal(t, "Umbrella — Lab automation", first["Name"])
	require.Equal(t, "Umbrella Health", first["Account"].(map[string]any)["Name"])

	_, res = c.query("select count() from opportunity where Account.Name = 'acme corporation'")
	require.EqualValues(t, 2, res["totalSize"])

	_, res = c.query("SELECT Name FROM Account WHERE Name LIKE '%Ind%' OR Industry IN ('Healthcare', 'Energy') ORDER BY Name LIMIT 2")
	names := []any{}
	for _, r := range res["records"].([]any) {
		names = append(names, r.(map[string]any)["Name"])
	}
	require.Equal(t, []any{"Stark Industries", "Umbrella Health"}, names)

	_, res = c.query("SELECT Id FROM Opportunity WHERE CloseDate = NEXT_N_DAYS:30 AND (StageName = 'Prospecting' OR StageName = 'Qualification')")
	require.EqualValues(t, 1, res["totalSize"])

	status, out := c.do("GET", "/services/data/v62.0/query?q="+url.QueryEscape("SELECT Amout FROM Opportunity"), "")
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "MALFORMED_QUERY", errorCode(out))
	status, out = c.do("GET", "/services/data/v62.0/query?q="+url.QueryEscape("SELECT Id FROM Widget"), "")
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "INVALID_TYPE", errorCode(out))
}

func TestQueryPages(t *testing.T) {
	_, c := newOrg(t)
	for i := range 250 {
		status, _ := c.do("POST", "/services/data/v62.0/sobjects/Task", `{"Subject": "call `+string(rune('a'+i%26))+`"}`)
		require.Equal(t, http.StatusCreated, status)
	}
	_, res := c.query("SELECT Id FROM Task")
	require.EqualValues(t, 250, res["totalSize"])
	require.Equal(t, false, res["done"])
	require.Len(t, res["records"], 200)
	status, out := c.do("GET", res["nextRecordsUrl"].(string), "")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, true, out.(map[string]any)["done"])
	require.Len(t, out.(map[string]any)["records"], 50)
}

func TestRecords(t *testing.T) {
	org, c := newOrg(t)

	status, out := c.do("POST", "/services/data/v62.0/sobjects/Opportunity", `{"Name": "Big deal"}`)
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "REQUIRED_FIELD_MISSING", errorCode(out))
	status, out = c.do("POST", "/services/data/v62.0/sobjects/Opportunity", `{"Name": "Refund", "StageName": "Prospecting", "CloseDate": "2026-12-01", "Amount": -5}`)
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "FIELD_CUSTOM_VALIDATION_EXCEPTION", errorCode(out))

	// Upsert creates on the first call and updates on the second.
	status, out = c.do("PATCH", "/services/data/v62.0/sobjects/Account/Domain__c/globex.com", `{"Name": "Globex"}`)
	require.Equal(t, http.StatusCreated, status)
	id := out.(map[string]any)["id"].(string)
	require.Len(t, id, 18)
	require.True(t, strings.HasPrefix(id, "001"))
	status, out = c.do("PATCH", "/services/data/v62.0/sobjects/Account/Domain__c/globex.com", `{"Industry": "Chemicals"}`)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, id, out.(map[string]any)["id"])

	status, out = c.do("GET", "/services/data/v62.0/sobjects/Account/"+id+"?fields=Name,Industry", "")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "Globex", out.(map[string]any)["Name"])
	require.Equal(t, "Chemicals", out.(map[string]any)["Industry"])
	require.NotContains(t, out, "Domain__c")

	status, _ = c.do("PATCH", "/services/data/v62.0/sobjects/Account/"+id, `{"Industry": "Energy"}`)
	require.Equal(t, http.StatusNoContent, status)
	status, _ = c.do("DELETE", "/services/data/v62.0/sobjects/Account/"+id, "")
	require.Equal(t, http.StatusNoContent, status)
	status, out = c.do("DELETE", "/services/data/v62.0/sobjects/Account/"+id, "")
	require.Equal(t, http.StatusNotFound, status)
	require.Equal(t, "NOT_FOUND", errorCode(out))
	require.Len(t, org.Records("Account"), 6)
}

func TestParallelWritesUnderOneAccountCollide(t *testing.T) {
	org, c := newOrg(t)
	WriteLatency = 200 * time.Millisecond
	acme := org.Records("Account")[0]["Id"].(string)

	var wg sync.WaitGroup
	codes := make([]string, 2)
	for i, body := range []string{
		`{"AccountId": "` + acme + `", "LastName": "Bunny"}`,
		`{"AccountId": "` + acme + `", "LastName": "Duck"}`,
	} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(time.Duration(i) * 50 * time.Millisecond)
			_, out := c.do("POST", "/services/data/v62.0/sobjects/Contact", body)
			codes[i] = errorCode(out)
		}()
	}
	wg.Wait()
	require.Equal(t, []string{"", "UNABLE_TO_LOCK_ROW"}, codes)

	// Once the first write is done, the row is free again.
	status, _ := c.do("POST", "/services/data/v62.0/sobjects/Contact", `{"AccountId": "`+acme+`", "LastName": "Duck"}`)
	require.Equal(t, http.StatusCreated, status)
}
