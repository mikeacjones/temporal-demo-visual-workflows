// Command worker executes flow runs as a long-lived process. It is the
// crashable worker for local runs: kill it mid-run and every run resumes from
// its last completed step when a worker comes back. In the catalog the same
// code runs as a Kubernetes worker that KEDA scales to zero when idle.
package main

import (
	"context"
	"os"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/temporalx"
	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/workflows"
)

func main() {
	logger := temporalx.Logger()
	c, err := temporalx.Dial(logger)
	if err != nil {
		logger.Error("temporal client", "error", err)
		os.Exit(1)
	}
	defer c.Close()

	tq := temporalx.TaskQueue()
	version := temporalx.DeploymentVersion()
	versioned := worker.DeploymentOptions{UseVersioning: true, Version: version}
	acts := workflows.NewActivities(logger)
	w := worker.New(c, tq, worker.Options{DeploymentOptions: versioned})
	workflows.Register(w, acts)

	// Each rate limit shared by every run is a task queue of its own, in the
	// same deployment version. TaskQueueActivitiesPerSecond is enforced by
	// the server for the whole queue, however many workers poll it.
	limits, err := temporalx.SharedLimits()
	if err != nil {
		logger.Error("rate limits", "error", err)
		os.Exit(1)
	}
	for _, l := range limits {
		lw := worker.New(c, flow.LimitTaskQueue(tq, l.Name), worker.Options{
			DeploymentOptions:            versioned,
			DisableWorkflowWorker:        true,
			TaskQueueActivitiesPerSecond: l.PerSecond(),
		})
		workflows.RegisterActivities(lw, acts)
		if err := lw.Start(); err != nil {
			logger.Error("rate limit worker", "limit", l.Name, "error", err)
			os.Exit(1)
		}
		defer lw.Stop()
		logger.Info("serving rate limit", "limit", l.Name, "count", l.Count, "per", l.Per)
	}

	if os.Getenv("WORKER_SET_CURRENT_VERSION") == "true" {
		// Local convenience: make this build the one new runs start on. In the
		// catalog, the platform does this when it rolls out a version.
		go setCurrentVersion(c, version)
	}

	logger.Info("flow worker starting", "task_queue", tq, "deployment", version.DeploymentName, "build_id", version.BuildID)
	if err := w.Run(worker.InterruptCh()); err != nil {
		logger.Error("worker stopped", "error", err)
		os.Exit(1)
	}
}

// setCurrentVersion retries until the server has seen this version's pollers.
func setCurrentVersion(c client.Client, v worker.WorkerDeploymentVersion) {
	logger := temporalx.Logger()
	handle := c.WorkerDeploymentClient().GetHandle(v.DeploymentName)
	for attempt := 0; attempt < 60; attempt++ {
		time.Sleep(time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := handle.SetCurrentVersion(ctx, client.WorkerDeploymentSetCurrentVersionOptions{BuildID: v.BuildID})
		cancel()
		if err == nil {
			logger.Info("current worker version set", "deployment", v.DeploymentName, "build_id", v.BuildID)
			return
		}
	}
	logger.Warn("could not set the current worker version", "deployment", v.DeploymentName, "build_id", v.BuildID)
}
