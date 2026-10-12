package workflows

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
)

// agentModelDefaults apply to the agent's model calls, which take longer than
// an ordinary HTTP step. They don't inherit the flow's HTTP-oriented defaults.
var agentModelDefaults = flow.ActivityOptions{
	StartToCloseSeconds: 300,
	Retry: flow.RetryConfig{
		MaxAttempts: 5, InitialIntervalSeconds: 2, BackoffCoefficient: 2, MaximumIntervalSeconds: 60,
	},
}

// activityOptions turns the builder's settings into Temporal Activity
// options. Retries come from the retry policy, not from a loop in the
// Workflow: Temporal schedules each attempt and records it.
func activityOptions(activityID string, o flow.ActivityOptions) workflow.ActivityOptions {
	return workflow.ActivityOptions{
		ActivityID:             activityID,
		StartToCloseTimeout:    time.Duration(o.StartToCloseSeconds) * time.Second,
		ScheduleToCloseTimeout: time.Duration(o.ScheduleToCloseSeconds) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        seconds(o.Retry.InitialIntervalSeconds),
			BackoffCoefficient:     o.Retry.BackoffCoefficient,
			MaximumInterval:        seconds(o.Retry.MaximumIntervalSeconds),
			MaximumAttempts:        int32(o.Retry.MaxAttempts),
			NonRetryableErrorTypes: o.Retry.NonRetryableErrorTypes,
		},
	}
}

func seconds(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }
