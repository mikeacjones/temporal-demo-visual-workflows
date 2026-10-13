# HTTP API

The `app` process serves the builder UI and this JSON API from one origin. Every
`/api` route is scoped to the caller's identity: the catalog's signed-in user
(`X-Temporal-Auth-Subject`, when trusted) or a signed session cookie. One user
can never see or act on another user's flows or runs. `/hooks` routes are for
outside systems; a signed token in the path authorizes them instead.

Types are the Go structs in `internal/flow` (`Definition`, `RunState`,
`Progress`, `AgentState`, ...), serialized with their JSON tags.

## Session and workers

| Method & path | Response |
|---|---|
| `GET /healthz` | `200 ok` |
| `GET /api/session` | `{sessionKey, displayName, signedIn, taskQueue, namespace, temporalUiUrl, workerMode}`; `workerMode` is `"serverless"` or `"long-lived"` |
| `GET /api/worker` | `{mode, available, workers, source}`. Serverless: `available` means a worker is running *right now*; none running is normal (scaled to zero). |

## Flows

| Method & path | Body | Response |
|---|---|---|
| `GET /api/flows` | | `FlowSummary[]`: `{id, name, description, version, updatedAt, triggerKind, nodeCount, scheduleActive, keyed}` |
| `POST /api/flows` | `Definition` (id/version ignored) | saved `Definition` |
| `GET /api/flows/{id}` | | `{flow, webhookUrl, scheduleActive}` |
| `PUT /api/flows/{id}` | `Definition` | saved `Definition` (version + 1); `400 {error, problems}` when invalid |
| `DELETE /api/flows/{id}` | | `204` (also deletes its schedule) |
| `GET /api/flows/{id}/versions` | | earlier versions `[{version, updatedAt, nodeCount}]`, newest first (last 20 kept); the current version is `GET /api/flows/{id}` |
| `GET /api/flows/{id}/versions/{version}` | | `Definition` |

## Runs

| Method & path | Body | Response |
|---|---|---|
| `POST /api/flows/{id}/runs` | `{input?}` | `{workflowId}`. Keyed flows: starts the run for the key, or sends the input to the existing one. A flow with a Respond step on its main path (early return) is started with Update-With-Start and also returns `{response, responded: true}` once the run reaches it; if it never does, `{workflowId, responseError}`. |
| `GET /api/flows/{id}/runs` | | `RunSummary[]` for that flow |
| `GET /api/runs?status=&flowId=&limit=` | | `RunSummary[]` across all flows: `{workflowId, flowId, flowName, flowVersion, source, key, status, startTime, closeTime}`. `status` is a Temporal status (`Running`, `Completed`, `Failed`, `Canceled`, `Terminated`, `TimedOut`). |
| `GET /api/runs/{wid}` | | `RunView` (below). Never needs a worker. |
| `GET /api/runs/{wid}/details` | | full `RunState` including step outputs: the run's result when complete, otherwise the `state` Query (wakes a worker on serverless) |
| `POST /api/runs/{wid}/signal` | `{name, payload?}` | `204` |
| `POST /api/runs/{wid}/updates/{name}` | `{payload?}` | `{result}` from the lane's Respond step; `409 {error}` if the validator rejects it or the lane fails |
| `GET /api/runs/{wid}/vars` | | visible variables (a Query) |
| `POST /api/runs/{wid}/approve` | `{nodeId, approved, comment?}` | `204`; `409` if not waiting |
| `POST /api/runs/{wid}/fix` | `{nodeId, action: "retry"\|"fail", url?, body?, headers?}` | `{result}`; resumes a step paused after its retries ran out (`waiting.kind == "fix"`). `409` if it isn't paused. |
| `POST /api/runs/{wid}/cancel` | | `204` |
| `GET /api/runs/{wid}/agents/{nodeId}` | | `AgentState` |
| `POST /api/runs/{wid}/agents/{nodeId}/answer` | `{answer}` | `204`; `409` if no question is pending |

### RunView

```jsonc
{
  "workflowId": "vflow-…",
  "temporalUrl": "http://localhost:8233/namespaces/default/workflows/vflow-…",
  "execution": { "status": "Running", "startTime": "…", "closeTime": null, "historyLength": 42,
                 "executionTime": "…" },          // delayed starts only: when the run begins working
  "flow": { /* Definition snapshot this run executes (from its start event) */ },
  "callbackUrl": "…/hooks/<token>/runs/<wid>",   // + /events/<name>, /updates/<name>, /vars
  "key": "C-42",                                   // keyed runs only
  "source": "manual",                              // manual, webhook, schedule, event, parent
  "progress": { /* Progress, from the run's memo; null before the first step.
                  events.<name> is {count, lastAt, last (when under 1 KB), pending} */ },
  "pendingActivities": [
    { "nodeId": "n3", "activityType": "HTTPRequest", "attempt": 3, "maxAttempts": 5,
      "state": "Scheduled", "lastFailure": "503 …", "nextAttemptTime": "…",
      "lastHeartbeatTime": "…", "heartbeat": { "phase": "waiting for GET …", "seconds": 8 } }
  ]
}
```

## Schedules

| Method & path | Body | Response |
|---|---|---|
| `GET /api/schedules` | | `[{flowId, flowName, paused, note, summary, nextRunTimes, recentRuns: [{workflowId, scheduledAt}], remainingActions}]` |
| `POST /api/flows/{id}/schedule` | `{enabled}` or `{action: "pause"\|"unpause"\|"trigger", note?}` | `{scheduleActive, paused}` |

The schedule comes from the trigger's `schedule` config (interval with unit,
cron with timezone, or calendar days and time). Saving the flow updates an
active schedule to the new version.

## Templates

| Method & path | Body | Response |
|---|---|---|
| `GET /api/templates` | | `Template[]`: `{id, name, description, author, builtIn, listed, mine, publishedAt, nodeCount, flow}`. Built-in examples plus published ones. |
| `POST /api/templates` | `{flowId, listed}` | `{template, shareUrl}`. `listed: true` publishes to the gallery; `false` makes a link-only share. |
| `GET /api/templates/{id}?t={token}` | | `Template`; unlisted templates need the share token |
| `POST /api/templates/{id}/use` | `{t?}` | new `Definition` (your own copy) |
| `DELETE /api/templates/{id}` | | `204`; only the author |

## Design patterns

| Method & path | Body | Response |
|---|---|---|
| `GET /api/patterns` | | `{categories, patterns: Pattern[]}`: every pattern in Temporal's catalog, with `{id, name, category, summary, support, useFor, build: [{node?, text}], watch, tryIt, docsUrl, flow?, children?, related}`. `support` is `visual`, `built-in`, or `not-in-builder`. |
| `GET /api/patterns/{id}` | | `Pattern` |
| `POST /api/patterns/{id}/use` | | new `Definition`: the example, copied into your library, with its child flows saved first and pointed at |

## Hooks (outside systems)

| Method & path | Body | Response |
|---|---|---|
| `POST /hooks/{token}` | any JSON | `{workflowId}`; starts a run (keyed flows: start-or-signal the key's run with Signal-With-Start). Early-return flows answer with `{workflowId, response, responded}`, or `502` with the run's ID if it never responds. |
| `POST /hooks/{token}/runs/{wid}/events/{name}` | any JSON | `204`; Signal `{name}` on that run |
| `POST /hooks/{token}/runs/{wid}/updates/{name}` | any JSON | `{result}`; Update `{name}`, waits for the Respond step |
| `GET /hooks/{token}/runs/{wid}/vars` | | visible variables |
| `POST /hooks/{token}/keys/{key}/events/{name}` | any JSON | `{workflowId}`; keyed flows: Signal-With-Start the key's run |

## Events, state and lanes

- A flow has one **main path** (from its trigger) and any number of **lanes**
  (from On signal / On update entry steps). Lanes share the run's variables,
  events and step outputs, and run on the same deterministic event loop, so
  they never interleave mid-step.
- **On signal** lanes run once per event, in arrival order. **On update** lanes
  run when an Update is accepted (validator true) and return the Respond step's
  value. A failed step in a signal lane marks that step failed; the run goes
  on. A failed update lane returns the error to the caller.
- **Wait for condition** re-checks when an event arrives or a variable changes.
  **Expression timers** recalculate when their variables change.
- The run finishes when its main path does, after any lane already running
  finishes, or when an **End run** step runs (anything still running on the
  main path stops).
- An **entity** (`settings.entity: true`) keeps serving events after its main
  path until an End run step, or for ever.
- A **keyed** flow (`settings.keyedBy`) keeps one run per key, and events for
  the key reach it; once it has completed, the key's next event starts a fresh
  run. Keying and being an entity are independent. Flows saved before the setting existed read a keyed flow
  without `idleTimeoutSeconds` as an entity.
- **Continue-As-New** applies to every run, only when Temporal suggests it.
  The main path stops at a safe point. Waits are interrupted and resumed in
  the next run with their deadlines; Activities and children finish first;
  For each stops between children. Running lanes and handlers finish, and
  arrived Signals are recorded. Events still queued for a lane, unconsumed
  events, variables, step outputs, loop passes and the undo stack all carry
  over in `RunInput.resume`.
- A **Loop** repeats the steps on its `body` outcome until its `until`
  expression holds (checked after each pass; the body runs at least once),
  taking `done`, or `limit` after `maxPasses`. The edges from the body back
  into the loop close it on the canvas; the interpreter starts the next pass
  instead of following them. Steps read the pass as `steps.<loop>.pass`.
- **Wait for signal** with `collect` gathers a batch: `{events, count,
  duplicates}`, closed by a quiet period, a maximum size, or a maximum wait.
- A **Respond** step on the main path answers whoever started the run (early
  return); the run carries on.
