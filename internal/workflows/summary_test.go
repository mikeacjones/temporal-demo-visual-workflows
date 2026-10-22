package workflows

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRequestSummaryLeavesOutQueriesAndBodies(t *testing.T) {
	require.Equal(t, "POST api.example.com/orders", requestSummary("post", "https://api.example.com/orders?token=secret#x"))
	require.Equal(t, "GET localhost:8090/inventory/BLND-500", requestSummary("", "http://localhost:8090/inventory/BLND-500"))
	require.Equal(t, "GET /relative/path", requestSummary("GET", "/relative/path?key=1"))
}
