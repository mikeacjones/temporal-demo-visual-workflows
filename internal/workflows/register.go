package workflows

import (
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"go.temporal.io/sdk/workflow"
)

// Registry is what both worker entry points (long-lived and AWS Lambda)
// register on.
type Registry interface {
	RegisterWorkflowWithOptions(w any, options workflow.RegisterOptions)
	RegisterActivity(a any)
}

// Register adds the flow Workflows and their Activities. Both Workflows are
// Pinned: a run finishes on the worker build it started on, so a new build
// never replays an old run's history and no patching is needed. Keyed runs
// move to the newest build when they continue as new.
func Register(r Registry, acts *Activities) {
	pinned := workflow.RegisterOptions{VersioningBehavior: workflow.VersioningBehaviorPinned}
	r.RegisterWorkflowWithOptions(FlowWorkflow, pinned)
	r.RegisterWorkflowWithOptions(AgentWorkflow, pinned)
	r.RegisterActivity(acts)
}

// RegisterActivities adds only the Activities: for a shared limit's task
// queue, which runs requests, never Workflows.
func RegisterActivities(r Registry, acts *Activities) {
	r.RegisterActivity(acts)
}

// NewActivities wires the Activities' clients from the environment.
func NewActivities(logger *slog.Logger) *Activities {
	acts := &Activities{
		HTTP:  &http.Client{Timeout: 25 * time.Second},
		Model: os.Getenv("ANTHROPIC_MODEL"),
	}
	if acts.Model == "" {
		acts.Model = "claude-opus-5-5"
	}
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		// Temporal owns retries (see the agent's retry policy), so the SDK's
		// own retry loop is turned off to keep each attempt visible.
		claude := anthropic.NewClient(option.WithMaxRetries(0))
		acts.Claude = &claude
	} else {
		logger.Warn("ANTHROPIC_API_KEY is not set; agent steps will fail until it is")
	}
	return acts
}
