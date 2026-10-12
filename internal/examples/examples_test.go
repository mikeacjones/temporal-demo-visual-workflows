package examples

import (
	"testing"
	"time"
)

func TestExamplesAreValid(t *testing.T) {
	for id, def := range Flows("http://sandbox:8090", time.Now()) {
		if problems := def.Validate(); len(problems) > 0 {
			t.Errorf("%s: %+v", id, problems)
		}
	}
}
