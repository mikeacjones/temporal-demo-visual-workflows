// Command lambda-worker runs the flow worker on AWS Lambda. Temporal Cloud
// invokes it only when a flow has work to do, so compute scales to zero while
// runs wait on events, timers, or people. The platform sets the Worker
// Deployment name and build ID for each rollout.
//
// A Lambda worker serves one task queue, so each rate limit shared by every
// run (RATE_LIMITS) needs a function of its own: the same image with
// RATE_LIMIT set to the limit's name serves only that limit's queue.
package main

import (
	"fmt"
	"os"

	lambdaworker "go.temporal.io/sdk/contrib/aws/lambdaworker"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/temporalx"
	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/workflows"
)

func main() {
	logger := temporalx.Logger()
	lambdaworker.RunWorker(temporalx.DeploymentVersion(), func(opts *lambdaworker.Options) error {
		acts := workflows.NewActivities(logger)
		name := os.Getenv("RATE_LIMIT")
		if name == "" {
			opts.TaskQueue = temporalx.TaskQueue()
			workflows.Register(opts, acts)
			return nil
		}
		limits, err := temporalx.SharedLimits()
		if err != nil {
			return err
		}
		for _, l := range limits {
			if l.Name == name {
				opts.TaskQueue = flow.LimitTaskQueue(temporalx.TaskQueue(), name)
				opts.WorkerOptions.DisableWorkflowWorker = true
				opts.WorkerOptions.TaskQueueActivitiesPerSecond = l.PerSecond()
				workflows.RegisterActivities(opts, acts)
				return nil
			}
		}
		return fmt.Errorf("RATE_LIMIT %q is not in RATE_LIMITS", name)
	})
}
