package workflows

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"

	"go.temporal.io/sdk/activity"
)

// StripeConnection is the worker's Stripe account: an API key, kept off
// flows and out of history.
type StripeConnection struct {
	URL    string // https://api.stripe.com, or the sandbox's
	APIKey string
	HTTP   *http.Client
}

// StripeRequest is one Stripe call. Each operation reads the fields it needs.
type StripeRequest struct {
	Email         string         `json:"email,omitempty"`
	Name          string         `json:"name,omitempty"`
	Description   string         `json:"description,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	Customer      string         `json:"customer,omitempty"`
	Amount        float64        `json:"amount,omitempty"`
	Currency      string         `json:"currency,omitempty"`
	PaymentMethod string         `json:"paymentMethod,omitempty"`
	PaymentIntent string         `json:"paymentIntent,omitempty"`
	Reason        string         `json:"reason,omitempty"`
}

// StripeFindCustomers lists customers with an email: {found, customer,
// customers}.
func (a *Activities) StripeFindCustomers(ctx context.Context, req StripeRequest) (map[string]any, error) {
	var list struct {
		Data []any `json:"data"`
	}
	if err := a.callStripe(ctx, http.MethodGet, "/v1/customers?"+url.Values{"email": {req.Email}, "limit": {"10"}}.Encode(), nil, &list); err != nil {
		return nil, err
	}
	out := map[string]any{"found": len(list.Data) > 0, "customers": list.Data, "customer": nil}
	if len(list.Data) > 0 {
		out["customer"] = list.Data[0]
	}
	return out, nil
}

// StripeCreateCustomer creates a customer.
func (a *Activities) StripeCreateCustomer(ctx context.Context, req StripeRequest) (map[string]any, error) {
	form := url.Values{}
	set(form, "email", req.Email)
	set(form, "name", req.Name)
	set(form, "description", req.Description)
	metadata(form, req.Metadata)
	var out map[string]any
	err := a.callStripe(ctx, http.MethodPost, "/v1/customers", form, &out)
	return out, err
}

// StripeDeleteCustomer deletes a customer. One that is already gone counts as
// deleted, so a retried delete (or an undo) succeeds.
func (a *Activities) StripeDeleteCustomer(ctx context.Context, req StripeRequest) (map[string]any, error) {
	var out map[string]any
	err := a.callStripe(ctx, http.MethodDelete, "/v1/customers/"+url.PathEscape(req.Customer), nil, &out)
	if errorType(err) == "resource_missing" {
		return map[string]any{"id": req.Customer, "deleted": true, "alreadyDeleted": true}, nil
	}
	return out, err
}

// StripeCreatePayment creates and confirms a PaymentIntent: a charge.
func (a *Activities) StripeCreatePayment(ctx context.Context, req StripeRequest) (map[string]any, error) {
	form := url.Values{
		"amount":                 {strconv.FormatInt(int64(math.Round(req.Amount)), 10)},
		"currency":               {req.Currency},
		"payment_method":         {req.PaymentMethod},
		"payment_method_types[]": {"card"},
		"confirm":                {"true"},
	}
	set(form, "customer", req.Customer)
	set(form, "description", req.Description)
	metadata(form, req.Metadata)
	var out map[string]any
	if err := a.callStripe(ctx, http.MethodPost, "/v1/payment_intents", form, &out); err != nil {
		return nil, err
	}
	activity.GetLogger(ctx).Info("stripe payment", "id", out["id"], "status", out["status"], "amount", out["amount"])
	return out, nil
}

// StripeGetPayment reads a PaymentIntent.
func (a *Activities) StripeGetPayment(ctx context.Context, req StripeRequest) (map[string]any, error) {
	var out map[string]any
	err := a.callStripe(ctx, http.MethodGet, "/v1/payment_intents/"+url.PathEscape(req.PaymentIntent), nil, &out)
	return out, err
}

// StripeRefund refunds a PaymentIntent, in full unless an amount is given.
func (a *Activities) StripeRefund(ctx context.Context, req StripeRequest) (map[string]any, error) {
	form := url.Values{"payment_intent": {req.PaymentIntent}}
	if req.Amount > 0 {
		form.Set("amount", strconv.FormatInt(int64(math.Round(req.Amount)), 10))
	}
	set(form, "reason", req.Reason)
	var out map[string]any
	err := a.callStripe(ctx, http.MethodPost, "/v1/refunds", form, &out)
	return out, err
}

// callStripe makes one Stripe API call. Writes carry an Idempotency-Key built
// from the run, the Activity (the step, and its item in a For each body) and
// when it was scheduled: every attempt of one Activity sends the same key, so
// Stripe answers a retry with the first attempt's result instead of charging
// again.
func (a *Activities) callStripe(ctx context.Context, method, path string, form url.Values, out any) error {
	sc := a.Stripe
	if sc == nil || sc.APIKey == "" {
		return notConfigured("STRIPE_API_KEY")
	}
	defer keepAlive(ctx, "waiting for Stripe: "+method+" "+truncate(path, 60))()
	header := bearer(sc.APIKey)
	var body []byte
	if method == http.MethodPost {
		info := activity.GetInfo(ctx)
		header.Set("Idempotency-Key", fmt.Sprintf("%s/%s/%d", info.WorkflowExecution.RunID, info.ActivityID, info.ScheduledTime.UnixNano()))
		body = []byte(form.Encode())
	}
	resp, err := apiCall(ctx, sc.HTTP, method, sc.URL+path, header, "application/x-www-form-urlencoded", body)
	if err != nil {
		return err
	}
	if resp.Status >= 200 && resp.Status < 300 {
		return resp.decodeInto("Stripe", out)
	}
	return stripeError(method, path, resp)
}

// stripeError: Stripe says whether to retry in its Stripe-Should-Retry
// header; without it, rate limits, conflicts and server errors are retried
// and other client errors (a declined card, a missing customer) are not.
func stripeError(method, path string, resp apiResponse) error {
	var body struct {
		Error struct {
			Type        string `json:"type"`
			Code        string `json:"code"`
			DeclineCode string `json:"decline_code"`
			Message     string `json:"message"`
		} `json:"error"`
	}
	_ = resp.decodeInto("Stripe", &body)
	e := body.Error
	code := cmp.Or(e.Code, e.Type, "stripe_error")
	msg := fmt.Sprintf("Stripe %s on %s %s: %s", code, method, truncate(path, 80), cmp.Or(e.Message, http.StatusText(resp.Status)))
	if e.DeclineCode != "" {
		msg += " (decline code " + e.DeclineCode + ")"
	}
	retryable := resp.Status == 429 || resp.Status == 409 || resp.Status >= 500
	switch resp.Header.Get("Stripe-Should-Retry") {
	case "true":
		retryable = true
	case "false":
		retryable = false
	}
	return connectorError(msg, code, retryable, 0, e)
}

func set(form url.Values, key, value string) {
	if value != "" {
		form.Set(key, value)
	}
}

// metadata sends key-value pairs in Stripe's metadata[key]=value form.
func metadata(form url.Values, m map[string]any) {
	for _, k := range slices.Sorted(maps.Keys(m)) {
		form.Set("metadata["+k+"]", fmt.Sprint(m[k]))
	}
}
