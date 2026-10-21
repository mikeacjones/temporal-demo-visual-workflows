package servicenowmock

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEncodedQueries(t *testing.T) {
	recs := []record{
		{"number": "INC1", "priority": "1", "state": "2", "short_description": "Checkout down"},
		{"number": "INC2", "priority": "3", "state": "1", "short_description": "Slow search"},
		{"number": "INC3", "priority": "4", "state": "6", "short_description": "Typo on checkout page", "close_code": "x"},
	}
	match := func(q string) []string {
		var out []string
		eq := parseQuery(q)
		for _, r := range recs {
			if eq.matches(r) {
				out = append(out, r["number"])
			}
		}
		return out
	}
	require.Equal(t, []string{"INC1", "INC3"}, match("short_descriptionLIKEcheckout"))
	require.Equal(t, []string{"INC1", "INC2"}, match("priority<=3"))
	require.Equal(t, []string{"INC2", "INC3"}, match("state=1^ORstate=6"))
	require.Equal(t, []string{"INC3"}, match("stateIN6,7^close_codeISNOTEMPTY"))
	require.Equal(t, []string{"INC2"}, match("priority!=1^state!=6^ORDERBYDESCnumber"))
}
