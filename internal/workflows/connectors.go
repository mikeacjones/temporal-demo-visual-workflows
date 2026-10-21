package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.temporal.io/sdk/temporal"
)

// apiResponse is a connector API's answer.
type apiResponse struct {
	Status int
	Header http.Header
	Body   []byte
}

// apiCall makes one request for a connector Activity. A malformed request
// fails for good; a network error is worth retrying, unless the step was
// cancelled. body, when set, is sent as is with contentType.
func apiCall(ctx context.Context, client *http.Client, method, url string, header http.Header, contentType string, body []byte) (apiResponse, error) {
	var reader io.Reader
	if body != nil {
		reader = strings.NewReader(string(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return apiResponse{}, temporal.NewNonRetryableApplicationError(err.Error(), "InvalidRequest", nil)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return apiResponse{}, ctx.Err()
		}
		return apiResponse{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return apiResponse{}, err
	}
	return apiResponse{Status: resp.StatusCode, Header: resp.Header, Body: raw}, nil
}

// jsonBody encodes v for a request body.
func jsonBody(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, temporal.NewNonRetryableApplicationError(err.Error(), "InvalidRequest", nil)
	}
	return raw, nil
}

// decodeInto reads a successful answer's JSON.
func (r apiResponse) decodeInto(service string, out any) error {
	if len(r.Body) == 0 {
		return nil
	}
	if err := json.Unmarshal(r.Body, out); err != nil {
		return fmt.Errorf("%s answered with unreadable JSON: %w", service, err)
	}
	return nil
}

// connectorError is a connector failure: its type is the service's own error
// code, so retry policies and the run view speak the service's language. A
// retryable one may say when to try again (Retry-After, a rate limit's reset).
func connectorError(msg, errType string, retryable bool, delay time.Duration, details any) error {
	if !retryable {
		return temporal.NewNonRetryableApplicationError(msg, errType, nil, details)
	}
	if delay > 0 {
		return temporal.NewApplicationErrorWithOptions(fmt.Sprintf("%s (retrying in %s, as asked)", msg, delay), errType,
			temporal.ApplicationErrorOptions{NextRetryDelay: delay, Details: []any{details}})
	}
	return temporal.NewApplicationError(msg, errType, details)
}

func notConfigured(variable string) error {
	return temporal.NewNonRetryableApplicationError(variable+" is not set on the worker", "ConnectorNotConfigured", nil)
}

func bearer(token string) http.Header {
	return http.Header{"Authorization": {"Bearer " + token}, "Accept": {"application/json"}}
}

// errorType is an Activity error's type, or "" for any other error.
func errorType(err error) string {
	var appErr *temporal.ApplicationError
	if errors.As(err, &appErr) {
		return appErr.Type()
	}
	return ""
}
