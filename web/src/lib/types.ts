// Mirrors internal/flow (definition.go, run.go) and docs/api.md. Keep the JSON
// field names in step with the Go struct tags.

export type NodeType =
	| 'trigger'
	| 'onSignal'
	| 'onUpdate'
	| 'http'
	| 'waitForCondition'
	| 'waitForSignal'
	| 'approval'
	| 'delay'
	| 'condition'
	| 'transform'
	| 'setVariable'
	| 'firstOf'
	| 'respond'
	| 'agent'
	| 'runFlow'
	| 'forEach';

/** Outcomes per node type. The first is the main path; the rest are secondary. */
export const HANDLES: Record<NodeType, string[]> = {
	trigger: ['out'],
	onSignal: ['out'],
	onUpdate: ['out'],
	http: ['out', 'error'],
	waitForCondition: ['out', 'timeout'],
	waitForSignal: ['received', 'timeout'],
	approval: ['approved', 'rejected', 'timeout'],
	delay: ['out'],
	condition: ['true', 'false'],
	transform: ['out'],
	setVariable: ['out'],
	firstOf: ['out'],
	respond: ['out'],
	agent: ['out', 'error'],
	runFlow: ['out', 'error'],
	forEach: ['out', 'error']
};

export const ENTRY_TYPES: NodeType[] = ['trigger', 'onSignal', 'onUpdate'];
export const isEntry = (t: NodeType) => ENTRY_TYPES.includes(t);

export type Direction = 'LR' | 'TB';

export interface Position {
	x: number;
	y: number;
}

export interface FlowNode {
	id: string;
	type: NodeType;
	key: string;
	label: string;
	position: Position;
	config: NodeConfig;
}

export interface FlowEdge {
	id: string;
	source: string;
	target: string;
	sourceHandle?: string;
}

export interface VariableDef {
	name: string;
	/** Starting value as JSON text. */
	initial?: string;
	visible?: boolean;
}

export interface RetryConfig {
	maxAttempts?: number;
	initialIntervalSeconds?: number;
	backoffCoefficient?: number;
	maximumIntervalSeconds?: number;
	nonRetryableErrorTypes?: string[];
	nonRetryableStatusCodes?: number[];
}

/** Zero/absent fields inherit: step → flow defaults → system defaults. */
export interface ActivityOptions {
	startToCloseSeconds?: number;
	scheduleToCloseSeconds?: number;
	retry: RetryConfig;
}

export const SYSTEM_ACTIVITY_DEFAULTS: Required<Pick<ActivityOptions, 'startToCloseSeconds'>> & ActivityOptions = {
	startToCloseSeconds: 30,
	retry: { maxAttempts: 5, initialIntervalSeconds: 1, backoffCoefficient: 2, maximumIntervalSeconds: 60 }
};
export const MAX_START_TO_CLOSE_SECONDS = 600;

export interface FlowSettings {
	direction?: Direction;
	variables?: VariableDef[];
	activity: ActivityOptions;
	keyedBy?: string;
	idleTimeoutSeconds?: number;
}

export interface Definition {
	id: string;
	name: string;
	description?: string;
	version: number;
	updatedAt: string;
	settings: FlowSettings;
	nodes: FlowNode[];
	edges: FlowEdge[];
}

export type TimeUnit = 'seconds' | 'minutes' | 'hours' | 'days';

export interface CalendarSpec {
	days?: string[];
	hour: number;
	minute: number;
}

export interface ScheduleConfig {
	every?: number;
	everyUnit?: TimeUnit;
	cron?: string;
	calendar?: CalendarSpec;
	timezone?: string;
	overlap?: 'skip' | 'buffer_one' | 'buffer_all' | 'cancel_other' | 'terminate_other' | 'allow_all';
	catchupWindowSeconds?: number;
	jitterSeconds?: number;
}

export interface TriggerConfig {
	kind: 'manual' | 'webhook' | 'schedule';
	sampleInput?: string;
	schedule?: ScheduleConfig;
	/** Original interval field, still accepted. */
	everySeconds?: number;
}

export interface OnSignalConfig {
	signalName: string;
}

export interface OnUpdateConfig {
	updateName: string;
	validator?: string;
}

export interface HTTPRequestSpec {
	method: string;
	url: string;
	headers?: Record<string, string>;
	body?: string;
}

export interface HTTPConfig extends HTTPRequestSpec {
	activity: ActivityOptions;
	undo?: HTTPRequestSpec;
	/** Original per-step fields, still accepted and folded into activity. */
	timeoutSeconds?: number;
	retry?: RetryConfig;
}

export interface WaitConditionConfig {
	signalName?: string;
	condition: string;
	timeoutSeconds?: number;
}

export interface WaitSignalConfig {
	signalName: string;
	timeoutSeconds?: number;
}

export interface ApprovalConfig {
	title: string;
	details?: string;
	timeoutSeconds?: number;
}

export interface DelayConfig {
	mode?: 'duration' | 'expression';
	amount?: number;
	unit?: TimeUnit;
	expression?: string;
	/** Original field, still accepted. */
	seconds?: number;
}

export interface ConditionConfig {
	expression: string;
}

export interface TransformConfig {
	output: string;
}

export interface Assignment {
	name: string;
	value: string;
}

export interface SetVariableConfig {
	assignments: Assignment[];
}

export type FirstOfConfig = Record<string, never>;

export interface RespondConfig {
	value: string;
}

export interface AgentToolParam {
	name: string;
	description: string;
}

export interface AgentTool {
	name: string;
	description: string;
	method: string;
	url: string;
	params: AgentToolParam[];
}

export interface AgentConfig {
	instructions: string;
	tools: AgentTool[];
	allowAskHuman: boolean;
	maxTurns: number;
	modelActivity: ActivityOptions;
	toolActivity: ActivityOptions;
}

export type ParentClosePolicy = 'terminate' | 'request_cancel' | 'abandon';

export interface RunFlowConfig {
	flowId: string;
	input?: string;
	parentClosePolicy?: ParentClosePolicy;
}

export interface ForEachConfig {
	items: string;
	flowId: string;
	input?: string;
	concurrency: number;
}

export type NodeConfig =
	| TriggerConfig
	| OnSignalConfig
	| OnUpdateConfig
	| HTTPConfig
	| WaitConditionConfig
	| WaitSignalConfig
	| ApprovalConfig
	| DelayConfig
	| ConditionConfig
	| TransformConfig
	| SetVariableConfig
	| FirstOfConfig
	| RespondConfig
	| AgentConfig
	| RunFlowConfig
	| ForEachConfig;

export interface Problem {
	nodeId?: string;
	message: string;
}

// ---- Run state ----

export type RunStatus = 'running' | 'completed' | 'failed';
export type NodeStatus = 'pending' | 'running' | 'waiting' | 'completed' | 'failed' | 'skipped';
/** Temporal execution status names, as the API returns them. */
export type ExecutionStatus = 'Running' | 'Completed' | 'Failed' | 'Canceled' | 'Terminated' | 'TimedOut' | string;

export interface Waiting {
	kind: 'signal' | 'condition' | 'approval' | 'timer' | 'children' | string;
	signalName?: string;
	title?: string;
	details?: string;
	until?: string;
}

export interface NodeState {
	status: NodeStatus;
	startedAt?: string;
	endedAt?: string;
	output?: unknown;
	error?: string;
	handle?: string;
	waiting?: Waiting;
	childWorkflowId?: string;
	invocations?: number;
	undo?: 'running' | 'done' | 'failed' | string;
	progress?: string;
}

export interface EventState {
	count: number;
	last?: unknown;
	lastAt?: string;
	pending: number;
}

export interface RunState {
	flowId: string;
	flowName: string;
	flowVersion: number;
	source: string;
	status: RunStatus;
	error?: string;
	startedAt: string;
	endedAt?: string;
	nodes: Record<string, NodeState>;
	callbackUrl: string;
	events: Record<string, EventState>;
	vars?: Record<string, unknown>;
	compensation?: '' | 'running' | 'done' | 'failed' | string;
	key?: string;
	generation?: number;
	flow: Definition;
}

/**
 * One node's progress in the run memo, with short keys to keep the memo small:
 * s status, h handle, e error, w waiting, t0 started, t1 ended,
 * i invocations, u undo, p progress note, c child workflow ID.
 */
export interface NodeProgress {
	s: NodeStatus;
	h?: string;
	e?: string;
	w?: Waiting;
	t0?: string;
	t1?: string;
	i?: number;
	u?: string;
	p?: string;
	c?: string;
}

/** The run memo: everything the run view draws without waking a worker. */
export interface Progress {
	status?: RunStatus;
	error?: string;
	startedAt?: string;
	endedAt?: string;
	source?: string;
	nodes: Record<string, NodeProgress>;
	vars?: Record<string, unknown>;
	/** The memo keeps a count per Signal name; full EventState comes from /details. */
	events?: Record<string, EventState | number>;
	compensation?: string;
	key?: string;
	generation?: number;
}

export interface TraceEntry {
	kind: 'reasoning' | 'tool_call' | 'tool_result' | 'question' | 'answer' | 'final' | string;
	text?: string;
	tool?: string;
	input?: unknown;
	isError?: boolean;
	at: string;
}

export interface AgentState {
	status: string;
	turn: number;
	maxTurns: number;
	trace: TraceEntry[];
	pendingQuestion?: string;
	answer?: string;
}

// ---- API envelopes (docs/api.md) ----

export type WorkerMode = 'serverless' | 'long-lived';

export interface SessionInfo {
	sessionKey: string;
	displayName?: string;
	signedIn?: boolean;
	taskQueue: string;
	namespace: string;
	temporalUiUrl: string;
	workerMode?: WorkerMode;
}

export interface WorkerStatus {
	mode?: WorkerMode;
	/** Serverless: a worker is running right now. None running is normal. */
	available: boolean;
	workers: number;
	source: 'heartbeat' | 'pollers' | string;
	error?: string;
}

export interface FlowSummary {
	id: string;
	name: string;
	description?: string;
	version: number;
	updatedAt: string;
	triggerKind: string;
	nodeCount: number;
	scheduleActive: boolean;
	keyed?: boolean;
}

export interface FlowDetail {
	flow: Definition;
	webhookUrl: string;
	scheduleActive: boolean;
}

export interface FlowVersion {
	version: number;
	updatedAt: string;
	nodeCount: number;
}

export interface RunSummary {
	workflowId: string;
	flowId?: string;
	flowName?: string;
	flowVersion?: number;
	source?: string;
	key?: string;
	status: ExecutionStatus;
	startTime: string;
	closeTime?: string | null;
}

export interface PendingActivity {
	nodeId: string;
	activityType: string;
	attempt: number;
	maxAttempts: number;
	state: string;
	lastFailure?: string;
	nextAttemptTime?: string;
}

export interface RunView {
	workflowId: string;
	temporalUrl: string;
	execution: {
		status: ExecutionStatus;
		startTime: string;
		closeTime?: string | null;
		historyLength: number;
	};
	flow: Definition;
	/** <callbackUrl>/events/<name>, /updates/<name>, /vars */
	callbackUrl: string;
	key?: string;
	/** manual, webhook, schedule, event, or parent */
	source?: string;
	progress: Progress | null;
	pendingActivities: PendingActivity[];
}

export interface ScheduleInfo {
	flowId: string;
	flowName: string;
	paused: boolean;
	note?: string;
	summary: string;
	nextRunTimes: string[];
	recentRuns: { workflowId: string; scheduledAt: string }[];
	remainingActions?: number;
}

export interface Template {
	id: string;
	name: string;
	description?: string;
	author?: string;
	builtIn: boolean;
	listed: boolean;
	mine: boolean;
	publishedAt?: string;
	nodeCount: number;
	flow: Definition;
}
