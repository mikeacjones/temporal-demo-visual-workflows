// Package temporalx holds the Temporal wiring shared by every process: client
// options from the environment, the task queue, and readable logs.
package temporalx

import (
	"errors"
	"log/slog"
	"os"
	"strings"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/envconfig"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
)

// TaskQueueEnv is the one variable every worker and client reads. The catalog
// injects <base>-<workspace> here for crashable workspaces, so starters and
// workers in a clone stay paired.
const TaskQueueEnv = "TASK_QUEUE"

// TaskQueue is where flow runs and their Activities execute.
func TaskQueue() string {
	if tq := os.Getenv(TaskQueueEnv); tq != "" {
		return tq
	}
	return "visual-workflows"
}

// BaseTaskQueue is the demo's own task queue. In a crashable workspace
// (PLATFORM_WORKSPACE is set) TASK_QUEUE is <base>-<workspace>; elsewhere
// it is the base itself.
func BaseTaskQueue() string {
	tq := TaskQueue()
	if ws := os.Getenv("PLATFORM_WORKSPACE"); ws != "" {
		return strings.TrimSuffix(tq, "-"+ws)
	}
	return tq
}

// LibraryTaskQueue serves the flow libraries and the template gallery. It
// derives from the base queue, so a crashable workspace uses the same
// libraries as the demo it was copied from. The app process polls it rather
// than the flow worker: the builder keeps loading and saving flows while the
// flow worker is down.
func LibraryTaskQueue() string { return BaseTaskQueue() + "-library" }

// SharedLimitsEnv lists the rate limits shared by every run, as
// "crm=5/s,payments=30/m". Each is a task queue of its own (see
// flow.LimitTaskQueue) that the flow worker also polls, and Temporal
// dispatches its Activities no faster than the rate. The app reads the same
// variable to offer the limits in the builder.
const SharedLimitsEnv = "RATE_LIMITS"

// SharedLimits parses SharedLimitsEnv.
func SharedLimits() ([]flow.SharedLimit, error) {
	return flow.ParseSharedLimits(os.Getenv(SharedLimitsEnv))
}

// DeploymentVersion identifies this worker build for Worker Versioning. The
// catalog sets both variables (the Worker Controller names the deployment
// TEMPORAL_DEPLOYMENT_NAME); locally the build is "local".
func DeploymentVersion() worker.WorkerDeploymentVersion {
	v := worker.WorkerDeploymentVersion{
		DeploymentName: os.Getenv("TEMPORAL_WORKER_DEPLOYMENT_NAME"),
		BuildID:        os.Getenv("TEMPORAL_WORKER_BUILD_ID"),
	}
	if v.DeploymentName == "" {
		v.DeploymentName = os.Getenv("TEMPORAL_DEPLOYMENT_NAME")
	}
	if v.DeploymentName == "" {
		v.DeploymentName = "visual-workflows"
	}
	if v.BuildID == "" {
		v.BuildID = "local"
	}
	return v
}

// Logger writes logfmt, which reads well in `kubectl logs` and k9s.
func Logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// Dial connects using only environment variables (TEMPORAL_ADDRESS,
// TEMPORAL_NAMESPACE, TEMPORAL_API_KEY, TEMPORAL_TLS_*). The CLI config file is
// ignored on purpose: a developer's default profile may point at a real Cloud
// namespace.
func Dial(logger *slog.Logger) (client.Client, error) {
	opts, err := envconfig.LoadClientOptions(envconfig.LoadClientOptionsRequest{DisableFile: true})
	if err != nil {
		return nil, err
	}
	if opts.HostPort == "" {
		opts.HostPort = "localhost:7233"
	}
	if opts.Namespace == "" {
		opts.Namespace = "default"
	}
	opts.Logger = storyLogger{log.NewStructuredLogger(logger)}
	// Frequent worker heartbeats let the builder show within seconds that a
	// worker was killed. (Experimental in the SDK; the default is 60s.)
	opts.WorkerHeartbeatInterval = 5 * time.Second
	// Lazy, so the app serves its UI and health check while Temporal is still
	// coming up instead of crash-looping.
	return client.NewLazyClient(opts)
}

// storyLogger rewrites the SDK's "Activity error." line. A failed attempt that
// Temporal will retry is expected behavior in this demo, and a red ERROR line
// on a shared screen reads as breakage.
type storyLogger struct{ log.Logger }

func (l storyLogger) Error(msg string, keyvals ...any) {
	if msg != "Activity error." {
		l.Logger.Error(msg, keyvals...)
		return
	}
	for i := 0; i+1 < len(keyvals); i += 2 {
		err, ok := keyvals[i+1].(error)
		if !ok {
			continue
		}
		var appErr *temporal.ApplicationError
		if errors.As(err, &appErr) && appErr.NonRetryable() {
			l.Logger.Warn("activity failed permanently; the flow routes or fails the node", keyvals...)
			return
		}
	}
	l.Logger.Warn("activity attempt failed; Temporal will retry it", keyvals...)
}

func (l storyLogger) With(keyvals ...any) log.Logger {
	if w, ok := l.Logger.(log.WithLogger); ok {
		return storyLogger{w.With(keyvals...)}
	}
	return l
}
