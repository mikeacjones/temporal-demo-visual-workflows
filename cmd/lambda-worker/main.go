// Command lambda-worker runs the flow worker on AWS Lambda. Temporal Cloud
// invokes it only when a flow has work to do, so compute scales to zero while
// runs wait on events, timers, or people. The platform sets the Worker
// Deployment name and build ID for each rollout.
package main

import (
	lambdaworker "go.temporal.io/sdk/contrib/aws/lambdaworker"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/temporalx"
	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/workflows"
)

func main() {
	logger := temporalx.Logger()
	lambdaworker.RunWorker(temporalx.DeploymentVersion(), func(opts *lambdaworker.Options) error {
		opts.TaskQueue = temporalx.TaskQueue()
		workflows.Register(opts, workflows.NewActivities(logger))
		return nil
	})
}
