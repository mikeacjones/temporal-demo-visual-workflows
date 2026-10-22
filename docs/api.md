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
| `GET /api/session` | `{sessionKey, displayName, signedIn, taskQueue, namespace, temporalUiUrl, workerMode, rateLimits}`; `workerMode` is `"serverless"` or `"long-lived"`; `rateLimits` is `[{name, count, per}]`, the limits shared by every run (`RATE_LIMITS`) |
| `GET /api/worker` | `{mode, available, workers, source}`. Serverless: `available` means a worker is running *right now*; none running is normal (scaled to zero). |

## Flows

| Method & path | Body | Response |
|---|---|---|
| `GET /api/flows` | | `FlowSummary[]`: `{id, name, description, version, updatedAt, triggerKind, nodeCount, scheduleActive, keyed}` |
| `POST /api/flows` | `Definition` (version ignored; `id` kept if it looks like `f-` and 6–16 hex digits, else one is made up) | `{flow, webhookUrl, scheduleActive}`, as `GET /api/flows/{id}`; `409` if the id is in use |
| `GET /api/flows/{id}` | | `{flow, webhookUrl, scheduleActive}` |
| `PUT /api/flows/{id}` | `Definition` | saved `Definition` (version + 1); `400 {error, problems}` when invalid |
| `DELETE /api/flows/{id}` | | `204` (also deletes its schedule) |
| `GET /api/flows/{id}/versions` | | earlier versions `[{version, updatedAt, nodeCount}]`, newest first (last 20 kept); the current version is `GET /api/flows/{id}` |
| `GET /api/flows/{id}/versions/{version}` | | `Definition` |

## Runs

| Method & path | Body | Response |
|---|---|---|
| `POST /api/flows/{id}/runs` | `{input?, with?, message?}` | `{workflowId}`. Keyed flows: starts the run for the key; if one exists, the flow's Workflow ID policies decide (by default it reaches the existing run). A flow with a Respond step on its main path (early return) is started with Update-With-Start and also returns `{response, responded: true}` once the run reaches it; if it never does, `{workflowId, responseError}`. Keyed flows only: `with: "signal"` or `"update"` sends the input as Signal or Update `message` with start, to the run for the key worked out from the input (see the hooks below); an Update returns its lane's value as `{response, responded: true}`. |
| `GET /api/flows/{id}/runs` | | `RunSummary[]` for that flow |
| `GET /api/runs?status=&flowId=&limit=` | | `RunSummary[]` across all flows: `{workflowId, flowId, flowName, flowVersion, source, key, status, startTime, closeTime}`. `status` is a Temporal status (`Running`, `Completed`, `Failed`, `Canceled`, `Terminated`, `TimedOut`). |
| `GET /api/runs/{wid}` | | `RunView` (below). Never needs a worker. |
| `GET /api/runs/{wid}/details` | | full `RunState` including step outputs: the run's result when complete, otherwise the `state` Query (wakes a worker on serverless) |
| `POST /api/runs/{wid}/signal` | `{name, payload?}` | `204` |
| `POST /api/runs/{wid}/updates/{name}` | `{payload?}` | `{result}` from the lane's Respond step; `409 {error}` if the validator rejects it or the lane fails |
| `GET /api/runs/{wid}/vars` | | visible variables (a Query) |
| `POST /api/runs/{wid}/approve` | `{nodeId, item?, approved, comment?}` | `204`; `409` if not waiting. `item` is required for a step in a For each body: each item waits for its own decision. |
| `POST /api/runs/{wid}/fix` | `{nodeId, item?, action: "retry"\|"fail", url?, body?, headers?}` | `{result}`; resumes a step paused after its retries ran out (`waiting.kind == "fix"`). `409` if it isn't paused. |
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
      "lastHeartbeatTime": "…", "heartbeat": { "phase": "waiting for GET …", "seconds": 8 } },
    { "nodeId": "fetch", "item": 3, … }        // a step running for item 3 of a For each body
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
| `POST /api/templates/{id}/use` | `{t?}` | your own copy, as `{flow, webhookUrl, scheduleActive}` |
| `DELETE /api/templates/{id}` | | `204`; only the author |

## Design patterns

| Method & path | Body | Response |
|---|---|---|
| `GET /api/patterns` | | `{categories, patterns: Pattern[]}`: every pattern in Temporal's catalog, with `{id, name, category, summary, support, useFor, build: [{node?, text}], watch, tryIt, docsUrl, flow?, children?, related}`. `support` is `visual`, `built-in`, or `not-in-builder`. |
| `GET /api/patterns/{id}` | | `Pattern` |
| `POST /api/patterns/{id}/use` | | `{flow, webhookUrl, scheduleActive}`: the example, copied into your library, with its child flows saved first and pointed at |

## Hooks (outside systems)

| Method & path | Body | Response |
|---|---|---|
| `POST /hooks/{token}` | any JSON | `{workflowId}`; starts a run (keyed flows: the run for the key, subject to the flow's Workflow ID policies). Early-return flows answer with `{workflowId, response, responded}`, or `502` with the run's ID if it never responds. |
| `POST /hooks/{token}/runs/{wid}/events/{name}` | any JSON | `204`; Signal `{name}` on that run |
| `POST /hooks/{token}/runs/{wid}/updates/{name}` | any JSON | `{result}`; Update `{name}`, waits for the Respond step |
| `GET /hooks/{token}/runs/{wid}/vars` | | visible variables |
| `POST /hooks/{token}/keys/{key}/events/{name}` | any JSON | `{workflowId}`; keyed flows: Signal-With-Start the key's run. Atomic: the run (if started) and the Signal are created together. `409` if the flow doesn't listen for `{name}`. |
| `POST /hooks/{token}/keys/{key}/updates/{name}` | any JSON | `{workflowId, result}`; keyed flows: Update-With-Start the key's run and wait for the On update lane's Respond value. Not atomic: on `503` (no worker answered in 30 s) the run may have started without the Update. `409` if the flow has no On update lane for `{name}`, or the policies or the validator refuse it. |

With-start sends the body as the message and, if a run starts, as its input
too. What happens when the key's Workflow ID is taken is up to the flow's
`settings.idConflictPolicy` (a run is running: `use_existing`, the default;
`fail`; `terminate_existing`) and `settings.idReusePolicy` (the last run has
closed: `allow_duplicate`, the default; `allow_duplicate_failed_only`;
`reject_duplicate`). Signal-With-Start can't fail on a running run, so `fail`
delivers to it, like `use_existing`.

## Events, state and lanes

- A flow has one **main path** (from its trigger) and any number of **lanes**
  (from On signal / On update entry steps). Lanes share the run's variables,
  events and step outputs, and run on the same deterministic event loop, so
  they never interleave mid-step.
- An optional **On start** lane (at most one) is the run's init: it runs once,
  first. The main path and the other lanes wait for it, so a Signal or Update
  sent with start is handled only after it. If it fails, the run fails, queued
  Signals are dropped and waiting Updates return the error. A run that
  continued as new doesn't run it again. It can't hold a Respond step.
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
  For each stops between items (running ones finish first). Running lanes and handlers finish, and
  arrived Signals are recorded. Events still queued for a lane, unconsumed
  events, variables, step outputs, loop passes and the undo stack all carry
  over in `RunInput.resume`.
- A **Loop** repeats the steps on its `body` outcome until its `until`
  expression holds (checked after each pass; the body runs at least once),
  taking `done`, or `limit` after `maxPasses`. The edges from the body back
  into the loop close it on the canvas; the interpreter starts the next pass
  instead of following them. Steps read the pass as `steps.<loop>.pass`.
- **For each** runs once per item of `items`, at most `concurrency` at a time,
  finishing per `finishWhen`. With `run: "flow"` (the default) each item is a
  child Workflow of `flowId`. With `run: "steps"` each item runs the steps on
  its `body` outcome inside this run, closed by edges back into the For each
  as with a Loop. Body steps see `item`, `index`, and the item's own step
  outputs, and each item's outputs end up in `steps.<each>.results[i]`, the
  same shape as a child's. A body can hold every step but Respond, End run,
  Loop and For each. Approvals and fixes there are per item (the decision
  names its `item`); a Wait for signal there needs `match`, an expression
  over `event` and `item` that picks the item's events, and events no item
  matches stay queued. Each item's Undo joins the run's stack, so a failed
  run undoes every item's completed requests; to put one item right when it
  fails, draw that on its error outcome. Items waiting at a Continue-As-New
  hand-off delay it until they finish.
  `rate: {count, per: "second"|"minute"}` spaces out starts in either mode.
- An HTTP step's **`rateLimit`** names a limit shared by every run, from
  `RATE_LIMITS` (`crm=5/s,payments=30/m`). Its requests run on the task queue
  `<TASK_QUEUE>-limit-<name>`, which the worker polls with
  `TaskQueueActivitiesPerSecond`, so the server dispatches them no faster than
  the rate however many runs and workers there are. Saving or running a flow
  that names a limit the deployment doesn't have is refused. On Lambda, each
  limit needs its own function: the worker image with `RATE_LIMIT=<name>`.
  In a run's state, a body step's progress sums up every item, and
  `items.<each>` lists the most recent items, each with its own step states.
- **Wait for signal** with `collect` gathers a batch: `{events, count,
  duplicates}`, closed by a quiet period, a maximum size, or a maximum wait.
- A **Respond** step on the main path answers whoever started the run (early
  return); the run carries on.
- **Run flow** waits for the child's result by default (`wait: "result"`).
  With `wait: "start"` it is fire and forget: the step finishes once the child
  has started, with `{workflowId, runId}`. That needs `parentClosePolicy:
  "abandon"`, so the child outlives this run, including when it continues as
  new.
- **Business connectors** (`type`: `salesforce`, `stripe`, `servicenow`,
  `slack` or `github`) share one config: `{operation, settings, mapping,
  undo, activity, rateLimit}`. `settings` say what the operation acts on (an
  sObject, a table, a repository). `mapping` gives its inputs their values:
  a template per input field, keyed by dotted path (`values.Name`,
  `metadata.plan`, `inputs.callback_url`). The Workflow renders each one and
  gives it the field's type (a number, a list of strings, a JSON value),
  then nests the paths into the Activity's argument. Saving refuses unknown
  inputs, values outside a picklist, and required inputs left unmapped
  (including the fields a create needs, such as a Contact's `LastName`).
  `GET /api/session` lists every connector's operations with their typed
  `settings`, `inputs` and `outputs`, and its `catalog` of record fields
  (Salesforce objects, ServiceNow tables); the specs live in
  `internal/connectors`. Steps saved with the earlier `params` map are read
  as settings and a mapping. Failures take the service's own error code as
  their type (`UNABLE_TO_LOCK_ROW`, `card_declined`, `channel_not_found`,
  `GitHubRateLimited`, ...), and each connector decides what is retried,
  waiting as long as a rate limit asks. `undo` runs the operation's
  compensation if the run fails later. The services and their credentials
  are the worker's (`SALESFORCE_*`, `STRIPE_*`, `SERVICENOW_*`, `SLACK_*`,
  `GITHUB_*`), or the sandbox's stand-ins when unset.
- A **Transform** either renders a JSON template (`output`) or, when it has
  `fields`, maps them: `{fields, mapping, lists}`. `fields` is the output's
  schema (the same field shape as connector inputs: `name`, `type`,
  `required`, nested `fields`, `items` for a list of values). `mapping` is a
  template per field path (`customer.email`, `lines[].sku`). `lists` builds a
  list field element by element: `{"lines": {"from": "steps.q.records",
  "where": "it.Amount > 1000"}}`, where `it` is the current element and
  `itIndex` its index. The Workflow renders each field and gives it its type
  (`any` keeps what a template renders). Saving refuses fields that aren't
  in the schema, element fields of a list that isn't mapped from one, and
  required fields left unmapped. `POST /api/transform/preview` and
  `POST /api/connectors/preview` build a step's output or Activity input
  against sample data, with the Workflow's own code, for the mapper.
