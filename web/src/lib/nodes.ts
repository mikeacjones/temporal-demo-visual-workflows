import type { Component } from 'svelte';
import Zap from '@lucide/svelte/icons/zap';
import Radio from '@lucide/svelte/icons/radio';
import RadioTower from '@lucide/svelte/icons/radio-tower';
import ArrowLeftRight from '@lucide/svelte/icons/arrow-left-right';
import Globe from '@lucide/svelte/icons/globe';
import Hourglass from '@lucide/svelte/icons/hourglass';
import UserCheck from '@lucide/svelte/icons/user-check';
import Timer from '@lucide/svelte/icons/timer';
import GitBranch from '@lucide/svelte/icons/git-branch';
import Braces from '@lucide/svelte/icons/braces';
import Variable from '@lucide/svelte/icons/variable';
import Swords from '@lucide/svelte/icons/swords';
import Reply from '@lucide/svelte/icons/reply';
import Bot from '@lucide/svelte/icons/bot';
import Workflow from '@lucide/svelte/icons/workflow';
import Repeat from '@lucide/svelte/icons/repeat';
import IterationCw from '@lucide/svelte/icons/iteration-cw';
import CircleStop from '@lucide/svelte/icons/circle-stop';
import Power from '@lucide/svelte/icons/power';
import RotateCcw from '@lucide/svelte/icons/rotate-ccw';
import Cloud from '@lucide/svelte/icons/cloud';
import CreditCard from '@lucide/svelte/icons/credit-card';
import Siren from '@lucide/svelte/icons/siren';
import Hash from '@lucide/svelte/icons/hash';
import GitPullRequest from '@lucide/svelte/icons/git-pull-request';
import type {
	AgentConfig,
	ApprovalConfig,
	ConditionConfig,
	DelayConfig,
	FlowNode,
	FirstOfConfig,
	ForEachConfig,
	HTTPConfig,
	LoopConfig,
	NodeConfig,
	NodeType,
	OnStartConfig,
	SagaConfig,
	OnSignalConfig,
	OnUpdateConfig,
	RespondConfig,
	RunFlowConfig,
	ConnectorConfig,
	ConnectorType,
	SetVariableConfig,
	TransformConfig,
	TriggerConfig,
	WaitConditionConfig,
	WaitSignalConfig
} from './types';
import { humanDuration } from './duration';
import { scheduleSummary } from './schedule';
import { missingMapped } from './mapping';
import { connectorBadge, connectorDefaults, connectorEyebrow, connectorOperation, connectorSummary } from './connectors';

export type PaletteGroup = 'Triggers' | 'Event lanes' | 'Actions' | 'Connectors' | 'Waiting' | 'Logic' | 'Sub-flows' | 'AI';

export interface NodeSpec {
	type: NodeType;
	label: string;
	group: PaletteGroup;
	/** The Temporal primitive this node runs as, shown in the palette and on the card. */
	primitive: string;
	icon: Component;
	accent: string;
	defaults: (variant?: string) => NodeConfig;
	summary: (config: NodeConfig) => string;
	/** The card's eyebrow, when it says more than the label: SALESFORCE · UPSERT RECORD. */
	eyebrow?: (config: NodeConfig) => string;
	/** A badge on the card: a connector step's mapping. */
	badge?: (config: NodeConfig) => { text: string; missing: boolean } | undefined;
	/** The label a new step gets, when it depends on the variant (a connector's operation). */
	variantLabel?: (variant: string) => string | undefined;
}

const dur = (s?: number) => (s ? humanDuration(s) : 'no timeout');

/** A business connector's step: its operations come from the session's connector specs. */
function connector(type: ConnectorType, label: string, icon: Component): NodeSpec {
	return {
		type,
		label,
		group: 'Connectors',
		primitive: 'Activity per operation',
		icon,
		accent: `var(--${type})`,
		defaults: (operation) => connectorDefaults(type, operation),
		summary: (c) => connectorSummary(type, c as ConnectorConfig),
		eyebrow: (c) => connectorEyebrow(type, c as ConnectorConfig),
		badge: (c) => connectorBadge(type, c as ConnectorConfig),
		variantLabel: (operation) => connectorOperation(type, operation)?.label
	};
}

export const NODE_SPECS: Record<NodeType, NodeSpec> = {
	trigger: {
		type: 'trigger',
		label: 'Trigger',
		group: 'Triggers',
		primitive: 'Start · Webhook · Schedule',
		icon: Zap,
		accent: 'var(--green)',
		defaults: (): TriggerConfig => ({ kind: 'manual', sampleInput: '{}' }),
		summary: (c) => {
			const t = c as TriggerConfig;
			if (t.kind === 'schedule') return scheduleSummary(t);
			return t.kind === 'webhook' ? 'Webhook POST' : 'Manual start';
		}
	},
	onStart: {
		type: 'onStart',
		label: 'On start',
		group: 'Event lanes',
		primitive: 'Init, before any handler',
		icon: Power,
		accent: 'var(--purple)',
		defaults: (): OnStartConfig => ({}),
		summary: () => 'once, before everything else'
	},
	onSignal: {
		type: 'onSignal',
		label: 'On signal',
		group: 'Event lanes',
		primitive: 'Signal handler',
		icon: RadioTower,
		accent: 'var(--purple)',
		defaults: (): OnSignalConfig => ({ signalName: 'change_address' }),
		summary: (c) => `each "${(c as OnSignalConfig).signalName}" event`
	},
	onUpdate: {
		type: 'onUpdate',
		label: 'On update',
		group: 'Event lanes',
		primitive: 'Update handler + validator',
		icon: ArrowLeftRight,
		accent: 'var(--purple)',
		defaults: (): OnUpdateConfig => ({ updateName: 'get_status', validator: '' }),
		summary: (c) => {
			const u = c as OnUpdateConfig;
			return `"${u.updateName}"${u.validator ? ' · validated' : ''}`;
		}
	},
	http: {
		type: 'http',
		label: 'HTTP request',
		group: 'Actions',
		primitive: 'Activity + retry policy',
		icon: Globe,
		accent: 'var(--cyan)',
		defaults: (): HTTPConfig => ({ method: 'GET', url: 'https://', headers: {}, body: '', activity: { retry: {} } }),
		summary: (c) => {
			const h = c as HTTPConfig;
			const extras = [h.undo && 'undo', h.pauseForFix && 'pauses for a fix', h.activity?.slowRetry && 'slow retries'].filter(Boolean);
			return `${h.method} ${h.url.replace(/^https?:\/\//, '')}${extras.map((x) => ' · ' + x).join('')}`;
		}
	},
	salesforce: connector('salesforce', 'Salesforce', Cloud),
	stripe: connector('stripe', 'Stripe', CreditCard),
	servicenow: connector('servicenow', 'ServiceNow', Siren),
	slack: connector('slack', 'Slack', Hash),
	github: connector('github', 'GitHub', GitPullRequest),
	transform: {
		type: 'transform',
		label: 'Transform',
		group: 'Actions',
		primitive: 'Deterministic workflow code',
		icon: Braces,
		accent: 'var(--indigo-200)',
		// New Transform steps use the mapper; the JSON template is the escape hatch.
		defaults: (): TransformConfig => ({ fields: [], mapping: {}, lists: {} }),
		summary: (c) => {
			const t = c as TransformConfig;
			if (t.fields) return t.fields.length ? `→ { ${t.fields.map((f) => (f.type === 'array' ? `${f.name}[]` : f.name)).join(', ')} }` : 'no output fields yet';
			try {
				return `→ { ${Object.keys(JSON.parse(t.output ?? '')).join(', ')} }`;
			} catch {
				return 'JSON template';
			}
		},
		badge: (c) => {
			const t = c as TransformConfig;
			if (!t.fields) return undefined;
			const missing = missingMapped(t).length;
			return missing ? { text: `${missing} required to map`, missing: true } : { text: `${Object.values(t.mapping ?? {}).filter((v) => v.trim()).length + Object.keys(t.lists ?? {}).length} mapped`, missing: false };
		}
	},
	setVariable: {
		type: 'setVariable',
		label: 'Set variable',
		group: 'Actions',
		primitive: 'Workflow state',
		icon: Variable,
		accent: 'var(--indigo-200)',
		defaults: (): SetVariableConfig => ({ assignments: [{ name: '', value: '' }] }),
		summary: (c) => {
			const a = (c as SetVariableConfig).assignments.filter((x) => x.name);
			return a.length ? a.map((x) => `vars.${x.name}`).join(', ') : 'no assignments';
		}
	},
	respond: {
		type: 'respond',
		label: 'Respond',
		group: 'Actions',
		primitive: 'Update result · early return',
		icon: Reply,
		accent: 'var(--purple)',
		defaults: (): RespondConfig => ({ value: '{\n  "ok": true\n}' }),
		summary: () => 'answers the caller'
	},
	waitForCondition: {
		type: 'waitForCondition',
		label: 'Wait for condition',
		group: 'Waiting',
		primitive: 'Signal + wait condition',
		icon: Hourglass,
		accent: 'var(--yellow)',
		defaults: (): WaitConditionConfig => ({
			signalName: 'order_status',
			condition: 'events.order_status.status == "shipped"',
			timeoutSeconds: 300
		}),
		summary: (c) => (c as WaitConditionConfig).condition
	},
	waitForSignal: {
		type: 'waitForSignal',
		label: 'Wait for signal',
		group: 'Waiting',
		primitive: 'Signal',
		icon: Radio,
		accent: 'var(--purple)',
		defaults: (): WaitSignalConfig => ({ signalName: 'event_received', timeoutSeconds: 0 }),
		summary: (c) => {
			const w = c as WaitSignalConfig;
			if (w.collect) return `collect "${w.signalName}" until ${humanDuration(w.collect.quietSeconds)} quiet`;
			return `"${w.signalName}" · ${dur(w.timeoutSeconds)}`;
		}
	},
	approval: {
		type: 'approval',
		label: 'Approval',
		group: 'Waiting',
		primitive: 'Update with validator',
		icon: UserCheck,
		accent: 'var(--purple)',
		defaults: (): ApprovalConfig => ({ title: 'Approve this step?', details: '', timeoutSeconds: 0 }),
		summary: (c) => {
			const a = c as ApprovalConfig;
			return `${a.title} · ${dur(a.timeoutSeconds)}`;
		}
	},
	delay: {
		type: 'delay',
		label: 'Delay',
		group: 'Waiting',
		primitive: 'Durable timer',
		icon: Timer,
		accent: 'var(--indigo-300)',
		defaults: (): DelayConfig => ({ mode: 'duration', amount: 10, unit: 'seconds' }),
		summary: (c) => {
			const d = c as DelayConfig;
			if (d.mode === 'expression') return `until ${d.expression}`;
			if (d.mode === 'duration') return `Sleep ${d.amount} ${d.unit}`;
			return `Sleep ${dur(d.seconds)}`;
		}
	},
	condition: {
		type: 'condition',
		label: 'Condition',
		group: 'Logic',
		primitive: 'Deterministic workflow code',
		icon: GitBranch,
		accent: 'var(--lime)',
		defaults: (): ConditionConfig => ({ expression: 'input.amount > 100' }),
		summary: (c) => (c as ConditionConfig).expression
	},
	loop: {
		type: 'loop',
		label: 'Loop',
		group: 'Logic',
		primitive: 'Repeat until · durable pause',
		icon: IterationCw,
		accent: 'var(--lime)',
		defaults: (): LoopConfig => ({ until: 'steps.check.body.status == "ready"', maxPasses: 20, pause: { mode: 'duration', amount: 5, unit: 'seconds' } }),
		summary: (c) => {
			const l = c as LoopConfig;
			const pause = l.pause ? (l.pause.mode === 'expression' ? ' · pause by expression' : ` · every ${l.pause.amount} ${l.pause.unit}`) : '';
			// Long loops continue as new; the summary needn't say so.
			return `until ${l.until}${pause}`;
		}
	},
	end: {
		type: 'end',
		label: 'End run',
		group: 'Logic',
		primitive: 'Completes the Workflow',
		icon: CircleStop,
		accent: 'var(--red)',
		defaults: () => ({}) as Record<string, never>,
		summary: () => 'the run completes here'
	},
	saga: {
		type: 'saga',
		label: 'Saga',
		group: 'Logic',
		primitive: 'Compensation stack · unwinds on failure',
		icon: RotateCcw,
		accent: 'var(--pink)',
		defaults: (): SagaConfig => ({ members: [] }),
		summary: (c) => {
			const m = (c as SagaConfig).members ?? [];
			if (!m.length) return 'link steps from their saga handle';
			const undo = m.filter((x) => x.compensation || x.builtIn).length;
			return `${m.length} member${m.length === 1 ? '' : 's'} · ${undo} with an undo`;
		}
	},
	firstOf: {
		type: 'firstOf',
		label: 'First of',
		group: 'Logic',
		primitive: 'Race: first branch wins, others cancelled',
		icon: Swords,
		accent: 'var(--lime)',
		defaults: (): FirstOfConfig => ({ wins: 'arrives' }),
		summary: (c) => ((c as FirstOfConfig).wins === 'succeeds' ? 'first branch to succeed wins' : 'first branch to arrive wins')
	},
	runFlow: {
		type: 'runFlow',
		label: 'Run flow',
		group: 'Sub-flows',
		primitive: 'Child Workflow',
		icon: Workflow,
		accent: 'var(--cyan)',
		defaults: (): RunFlowConfig => ({ flowId: '', input: '{\n  "from": "{{ run.id }}"\n}', parentClosePolicy: 'terminate' }),
		summary: (c) => {
			const r = c as RunFlowConfig;
			if (!r.flowId) return 'choose a flow';
			return r.wait === 'start' ? `starts ${r.flowId} · doesn't wait` : `runs ${r.flowId}`;
		}
	},
	forEach: {
		type: 'forEach',
		label: 'For each',
		group: 'Sub-flows',
		primitive: 'Child Workflows, or steps run per item',
		icon: Repeat,
		accent: 'var(--cyan)',
		defaults: (): ForEachConfig => ({
			items: 'input.items',
			flowId: '',
			input: '{\n  "item": "{{ item }}",\n  "index": "{{ index }}"\n}',
			concurrency: 5
		}),
		summary: (c) => {
			const f = c as ForEachConfig;
			const until = f.finishWhen === 'first' ? ' · first to succeed' : f.finishWhen === 'count' ? ` · first ${f.count ?? 1} to succeed` : '';
			const rate = f.rate ? ` · ≤${f.rate.count}/${f.rate.per === 'minute' ? 'min' : 's'}` : '';
			const what = f.run === 'steps' ? 'body steps' : f.flowId || 'choose a flow';
			return `${f.items} → ${what} · ${f.concurrency} at a time${rate}${until}`;
		}
	},
	agent: {
		type: 'agent',
		label: 'AI agent',
		group: 'AI',
		primitive: 'Child Workflow · ReAct loop',
		icon: Bot,
		accent: 'var(--pink)',
		defaults: (): AgentConfig => ({
			instructions: 'Use the tools to answer: {{ input.question }}',
			tools: [],
			allowAskHuman: true,
			maxTurns: 8,
			modelActivity: { retry: {} },
			toolActivity: { retry: {} }
		}),
		summary: (c) => {
			const a = c as AgentConfig;
			const tools = a.tools.length + (a.allowAskHuman ? 1 : 0);
			return `${tools} tool${tools === 1 ? '' : 's'} · ≤${a.maxTurns} turns`;
		}
	}
};

export const PALETTE_GROUPS: { name: PaletteGroup; types: NodeType[] }[] = [
	{ name: 'Triggers', types: ['trigger'] },
	{ name: 'Event lanes', types: ['onStart', 'onSignal', 'onUpdate'] },
	{ name: 'Actions', types: ['http', 'transform', 'setVariable', 'respond'] },
	{ name: 'Connectors', types: ['salesforce', 'stripe', 'servicenow', 'slack', 'github'] },
	{ name: 'Waiting', types: ['waitForCondition', 'waitForSignal', 'approval', 'delay'] },
	{ name: 'Logic', types: ['condition', 'loop', 'firstOf', 'saga', 'end'] },
	{ name: 'Sub-flows', types: ['runFlow', 'forEach'] },
	{ name: 'AI', types: ['agent'] }
];

/** lower_snake_case key from a label, unique among the given nodes. */
export function uniqueKey(label: string, nodes: FlowNode[], selfId?: string): string {
	let base = label
		.toLowerCase()
		.replace(/[^a-z0-9]+/g, '_')
		.replace(/^_+|_+$/g, '');
	if (!/^[a-z]/.test(base)) base = `step_${base}`.replace(/_+$/, '');
	if (!base) base = 'step';
	const taken = new Set(nodes.filter((n) => n.id !== selfId).map((n) => n.key));
	let key = base;
	for (let i = 2; taken.has(key); i++) key = `${base}_${i}`;
	return key;
}

export function newNodeId(): string {
	return 'n' + Math.random().toString(36).slice(2, 9);
}

/** A new step. variant picks among a type's kinds: a connector's operation. */
export function makeNode(type: NodeType, position: { x: number; y: number }, nodes: FlowNode[], variant?: string): FlowNode {
	const spec = NODE_SPECS[type];
	const label = (variant && spec.variantLabel?.(variant)) || spec.label;
	return { id: newNodeId(), type, key: uniqueKey(label, nodes), label, position, config: spec.defaults(variant) };
}

export function blankFlow(name = 'Untitled flow') {
	const trigger = makeNode('trigger', { x: 80, y: 160 }, []);
	trigger.label = 'Start';
	trigger.key = 'start';
	return { name, description: '', settings: { direction: 'LR' as const, variables: [], activity: { retry: {} } }, nodes: [trigger], edges: [] };
}
