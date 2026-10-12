package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/workflows"
)

func (s *Server) scheduleActive(ctx context.Context, u user, flowID string) bool {
	_, err := s.tc.ScheduleClient().GetHandle(ctx, s.scheduleID(u.Key, flowID)).Describe(ctx)
	return err == nil
}

var weekdays = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// scheduleSpec turns the trigger's schedule settings into a Temporal
// Schedule spec: an interval, a cron expression, or calendar days and time,
// in a time zone.
func scheduleSpec(c flow.TriggerConfig) (client.ScheduleSpec, string) {
	cfg := flow.ScheduleConfig{}
	if c.Schedule != nil {
		cfg = *c.Schedule
	}
	spec := client.ScheduleSpec{TimeZoneName: cfg.Timezone, Jitter: time.Duration(cfg.JitterSeconds) * time.Second}
	tz := cfg.Timezone
	if tz == "" {
		tz = "UTC"
	}
	switch {
	case cfg.Cron != "":
		spec.CronExpressions = []string{cfg.Cron}
		return spec, fmt.Sprintf("cron %q (%s)", cfg.Cron, tz)
	case cfg.Calendar != nil:
		cal := client.ScheduleCalendarSpec{
			Hour:   []client.ScheduleRange{{Start: cfg.Calendar.Hour}},
			Minute: []client.ScheduleRange{{Start: cfg.Calendar.Minute}},
		}
		days := "every day"
		if len(cfg.Calendar.Days) > 0 {
			for _, d := range cfg.Calendar.Days {
				if i := slices.Index(weekdays, d); i >= 0 {
					cal.DayOfWeek = append(cal.DayOfWeek, client.ScheduleRange{Start: i})
				}
			}
			days = strings.Join(cfg.Calendar.Days, ", ")
		} else {
			cal.DayOfWeek = []client.ScheduleRange{{Start: 0, End: 6}}
		}
		spec.Calendars = []client.ScheduleCalendarSpec{cal}
		return spec, fmt.Sprintf("%s at %02d:%02d (%s)", days, cfg.Calendar.Hour, cfg.Calendar.Minute, tz)
	default:
		every := c.Interval()
		spec.Intervals = []client.ScheduleIntervalSpec{{Every: every}}
		return spec, "every " + every.String()
	}
}

func overlapPolicy(name string) enumspb.ScheduleOverlapPolicy {
	switch name {
	case "buffer_one":
		return enumspb.SCHEDULE_OVERLAP_POLICY_BUFFER_ONE
	case "buffer_all":
		return enumspb.SCHEDULE_OVERLAP_POLICY_BUFFER_ALL
	case "cancel_other":
		return enumspb.SCHEDULE_OVERLAP_POLICY_CANCEL_OTHER
	case "terminate_other":
		return enumspb.SCHEDULE_OVERLAP_POLICY_TERMINATE_OTHER
	case "allow_all":
		return enumspb.SCHEDULE_OVERLAP_POLICY_ALLOW_ALL
	default:
		return enumspb.SCHEDULE_OVERLAP_POLICY_SKIP
	}
}

// upsertSchedule runs the flow on a Temporal Schedule. The schedule holds a
// snapshot of the definition, refreshed whenever the flow is saved.
func (s *Server) upsertSchedule(ctx context.Context, r *http.Request, u user, def flow.Definition) error {
	trigger := triggerOf(def)
	if trigger.Kind != "schedule" {
		return errors.New("set the trigger to a schedule first")
	}
	in, err := s.runInput(ctx, r, u, def, nil, "schedule")
	if err != nil {
		return err
	}
	action := &client.ScheduleWorkflowAction{
		// Temporal appends the scheduled time, keeping each run's ID unique.
		ID:        s.flowRunPrefix(u.Key, def.ID) + "scheduled",
		Workflow:  workflows.FlowWorkflow,
		Args:      []any{in},
		TaskQueue: s.cfg.TaskQueue,
		Memo:      runMemo(in),
	}
	spec, _ := scheduleSpec(trigger)
	var catchup time.Duration
	overlap := enumspb.SCHEDULE_OVERLAP_POLICY_SKIP
	if c := trigger.Schedule; c != nil {
		catchup = time.Duration(c.CatchupWindowSeconds) * time.Second
		overlap = overlapPolicy(c.Overlap)
	}

	handle := s.tc.ScheduleClient().GetHandle(ctx, s.scheduleID(u.Key, def.ID))
	if _, err := handle.Describe(ctx); err == nil {
		return handle.Update(ctx, client.ScheduleUpdateOptions{DoUpdate: func(in client.ScheduleUpdateInput) (*client.ScheduleUpdate, error) {
			sched := in.Description.Schedule
			sched.Action = action
			sched.Spec = &spec
			if sched.Policy != nil {
				sched.Policy.Overlap = overlap
				sched.Policy.CatchupWindow = catchup
			}
			return &client.ScheduleUpdate{Schedule: &sched}, nil
		}})
	}
	_, err = s.tc.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID:            s.scheduleID(u.Key, def.ID),
		Spec:          spec,
		Action:        action,
		Overlap:       overlap,
		CatchupWindow: catchup,
		// A forgotten demo schedule shouldn't run forever.
		RemainingActions: 100,
	})
	return err
}

func (s *Server) setSchedule(w http.ResponseWriter, r *http.Request, u user) {
	var body struct {
		Enabled *bool  `json:"enabled"`
		Action  string `json:"action"`
		Note    string `json:"note"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	def, ok, err := s.findFlow(ctx, u, r.PathValue("id"))
	if err != nil || !ok {
		writeError(w, http.StatusNotFound, errors.New("flow not found"))
		return
	}
	handle := s.tc.ScheduleClient().GetHandle(ctx, s.scheduleID(u.Key, def.ID))
	switch {
	case body.Enabled != nil && *body.Enabled:
		err = s.upsertSchedule(ctx, r, u, def)
	case body.Enabled != nil:
		err = handle.Delete(ctx)
		var notFound *serviceerror.NotFound
		if errors.As(err, &notFound) {
			err = nil
		}
	case body.Action == "pause":
		err = handle.Pause(ctx, client.SchedulePauseOptions{Note: body.Note})
	case body.Action == "unpause":
		err = handle.Unpause(ctx, client.ScheduleUnpauseOptions{Note: body.Note})
	case body.Action == "trigger":
		// Run now, even if a scheduled run is still going.
		err = handle.Trigger(ctx, client.ScheduleTriggerOptions{Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_ALLOW_ALL})
	default:
		err = errors.New("send enabled, or an action: pause, unpause, trigger")
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	active, paused := false, false
	if desc, err := handle.Describe(ctx); err == nil {
		active, paused = true, desc.Schedule.State.Paused
	}
	writeJSON(w, http.StatusOK, map[string]any{"scheduleActive": active, "paused": paused})
}

func (s *Server) listSchedules(w http.ResponseWriter, r *http.Request, u user) {
	ctx := r.Context()
	list, err := s.flows(ctx, u)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	out := []map[string]any{}
	for _, def := range list {
		desc, err := s.tc.ScheduleClient().GetHandle(ctx, s.scheduleID(u.Key, def.ID)).Describe(ctx)
		if err != nil {
			continue
		}
		_, summary := scheduleSpec(triggerOf(def))
		recent := []map[string]any{}
		for _, a := range desc.Info.RecentActions {
			row := map[string]any{"scheduledAt": a.ScheduleTime}
			if a.StartWorkflowResult != nil {
				row["workflowId"] = a.StartWorkflowResult.WorkflowID
			}
			recent = append(recent, row)
		}
		slices.Reverse(recent)
		next := desc.Info.NextActionTimes
		if len(next) > 3 {
			next = next[:3]
		}
		out = append(out, map[string]any{
			"flowId": def.ID, "flowName": def.Name, "flowVersion": def.Version,
			"paused": desc.Schedule.State.Paused, "note": desc.Schedule.State.Note,
			"summary": summary, "nextRunTimes": next, "recentRuns": recent,
			"remainingActions": desc.Schedule.State.RemainingActions,
		})
	}
	writeJSON(w, http.StatusOK, out)
}
