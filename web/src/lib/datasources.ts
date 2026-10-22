import { connectorSpec, outputFields, connectorOperation } from './connectors';
import { NODE_SPECS } from './nodes';
import { loopBody } from './loops';
import { isConnector, type ConnectorConfig, type ConnectorField, type ConnectorType, type Definition, type FlowNode, type ForEachConfig, type RunState, type TransformConfig, type TriggerConfig, type VariableDef } from './types';

// What a step can read in its templates, as typed trees for the mapper: the
// start input, the outputs of the steps before it, the run's variables, its
// item in a For each body, and the run itself. Connector steps declare their
// outputs; other steps' come from their type, and anything unknown is inferred
// from sample data (the latest run, or the trigger's sample input).

/** One top-level source in the mapper: input, a step's output, vars, item, run. */
export interface SourceRoot {
	/** The expression path: input, steps.account, vars. */
	path: string;
	label: string;
	/** A step's type, for its icon and colour. */
	type?: FlowNode['type'];
	field: ConnectorField;
}

/** The sample data a template sees: the Workflow's env, from a run or from sample input. */
export type SampleEnv = Record<string, unknown>;

/** Infer a field tree from a JSON value. */
export function infer(name: string, v: unknown): ConnectorField {
	if (Array.isArray(v)) {
		const first = v.find((x) => x !== null && x !== undefined);
		if (first && typeof first === 'object' && !Array.isArray(first)) return { name, type: 'array', fields: infer('', first).fields };
		return { name, type: 'array', items: first === undefined ? undefined : infer('', first).type };
	}
	if (v && typeof v === 'object') {
		return { name, type: 'object', fields: Object.entries(v as Record<string, unknown>).map(([k, x]) => infer(k, x)) };
	}
	if (typeof v === 'number') return { name, type: Number.isInteger(v) ? 'integer' : 'number' };
	if (typeof v === 'boolean') return { name, type: 'boolean' };
	if (typeof v === 'string') return { name, type: /^\d{4}-\d{2}-\d{2}$/.test(v) ? 'date' : /^\d{4}-\d{2}-\d{2}T/.test(v) ? 'datetime' : 'string' };
	return { name, type: '' };
}

const f = (name: string, type: ConnectorField['type'], extra: Partial<ConnectorField> = {}): ConnectorField => ({ name, type, ...extra });

/** A step's output fields: declared by connectors, known by step type, else inferred from a sample. */
export function stepOutputs(node: FlowNode, sample: unknown): ConnectorField[] {
	const fromSample = (): ConnectorField[] => (sample && typeof sample === 'object' ? (infer('', sample).fields ?? []) : []);
	if (isConnector(node.type)) {
		const spec = connectorSpec(node.type as ConnectorType);
		const op = spec && connectorOperation(node.type as ConnectorType, (node.config as ConnectorConfig).operation);
		return spec && op ? withSample(outputFields(spec, op, node.config as ConnectorConfig), sample) : fromSample();
	}
	switch (node.type) {
		case 'http':
			return [f('status', 'integer'), withSampleField(f('body', '', { description: 'The response: parsed JSON, or text.' }), (sample as { body?: unknown })?.body)];
		case 'transform': {
			const t = node.config as TransformConfig;
			if (t.fields) return withSample(t.fields, sample);
			try {
				return infer('', JSON.parse(t.output ?? '')).fields ?? fromSample();
			} catch {
				return fromSample();
			}
		}
		case 'waitForSignal':
			return [withSampleField(f('payload', '', { description: 'The event.' }), (sample as { payload?: unknown })?.payload), f('timedOut', 'boolean')];
		case 'waitForCondition':
			return [f('met', 'boolean'), f('timedOut', 'boolean')];
		case 'approval':
			return [f('approved', 'boolean'), f('comment', 'string'), f('by', 'string'), f('decidedAt', 'datetime'), f('decision', 'string')];
		case 'delay':
			return [f('waitedSeconds', 'number')];
		case 'condition':
			return [f('result', 'boolean')];
		case 'setVariable':
			return [f('set', 'object', { open: true })];
		case 'loop':
			return [f('pass', 'integer'), f('passes', 'integer')];
		case 'firstOf':
			return [f('winner', 'string'), withSampleField(f('output', ''), (sample as { output?: unknown })?.output)];
		case 'forEach':
			return [f('count', 'integer'), f('succeeded', 'integer'), withSampleField(f('results', 'array'), (sample as { results?: unknown })?.results)];
		case 'runFlow':
			return fromSample().length ? fromSample() : [f('workflowId', 'string'), f('runId', 'string')];
		case 'agent':
			return [withSampleField(f('data', '', { description: 'The agent\'s JSON answer.' }), (sample as { data?: unknown })?.data), f('text', 'string')];
		case 'onSignal':
		case 'onUpdate':
			return fromSample();
		default:
			return fromSample();
	}
}

/** Fill in fields a schema leaves open (a query's records, an HTTP body) from a sample. */
function withSample(fields: ConnectorField[], sample: unknown): ConnectorField[] {
	if (!sample || typeof sample !== 'object') return fields;
	return fields.map((x) => withSampleField(x, (sample as Record<string, unknown>)[x.name]));
}

function withSampleField(x: ConnectorField, v: unknown): ConnectorField {
	if (v === undefined || v === null) return x;
	const known = x.fields?.length || (x.type && x.type !== 'object' && x.type !== 'array');
	if (known) return x;
	const inferred = infer(x.name, v);
	return { ...inferred, description: x.description, open: x.open };
}

/** The steps before a step, nearest first: everything with a path to it. */
export function upstream(def: Definition, nodeId: string): FlowNode[] {
	const seen = new Set<string>([nodeId]);
	const order: FlowNode[] = [];
	let frontier = [nodeId];
	while (frontier.length) {
		const next: string[] = [];
		for (const id of frontier) {
			for (const e of def.edges) {
				if (e.target !== id || seen.has(e.source)) continue;
				seen.add(e.source);
				next.push(e.source);
				const n = def.nodes.find((x) => x.id === e.source);
				if (n) order.push(n);
			}
		}
		frontier = next;
	}
	return order;
}

/** The For each whose body holds a step, if any. */
export function enclosingEach(def: Definition, nodeId: string): FlowNode | undefined {
	return def.nodes.find((n) => n.type === 'forEach' && (n.config as ForEachConfig).run === 'steps' && loopBody(def, n.id).has(nodeId));
}

/** Everything a step's templates can read, as mapper sources. */
export function sourceRoots(def: Definition, node: FlowNode, sample: SampleEnv): SourceRoot[] {
	const roots: SourceRoot[] = [];
	const steps = (sample.steps ?? {}) as Record<string, unknown>;
	roots.push({ path: 'input', label: 'Start input', type: 'trigger', field: { ...infer('input', sample.input ?? {}), name: 'input' } });

	const each = enclosingEach(def, node.id);
	if (each) {
		roots.push({ path: 'item', label: `This item of ${each.label}`, type: 'forEach', field: { ...infer('item', sample.item), name: 'item' } });
		roots.push({ path: 'index', label: 'Its index', type: 'forEach', field: f('index', 'integer') });
	}
	for (const up of upstream(def, node.id)) {
		// A For each's output exists only once every item is done, so steps
		// in its body can't read it.
		if (up.type === 'trigger' || up.id === each?.id) continue;
		roots.push({ path: `steps.${up.key}`, label: up.label, type: up.type, field: { name: up.key, type: 'object', fields: stepOutputs(up, steps[up.key]) } });
	}
	const vars = def.settings.variables ?? [];
	if (vars.length) {
		roots.push({ path: 'vars', label: 'Variables', field: { name: 'vars', type: 'object', fields: vars.map((v) => variableField(v, (sample.vars as Record<string, unknown>)?.[v.name])) } });
	}
	roots.push({
		path: 'run',
		label: 'This run',
		field: { name: 'run', type: 'object', fields: [f('id', 'string', { description: 'The Workflow ID: stable across retries, a good idempotency key.' }), f('callbackUrl', 'string'), f('startedAt', 'datetime')] }
	});
	return roots;
}

function variableField(v: VariableDef, sample: unknown): ConnectorField {
	if (sample !== undefined) return infer(v.name, sample);
	try {
		return infer(v.name, JSON.parse(v.initial ?? 'null'));
	} catch {
		return f(v.name, '');
	}
}

/**
 * Sample data for previews: the latest run's input and step outputs when
 * there is one, else the trigger's sample input. Steps without a sample get
 * placeholder values from their schemas, so templates still render.
 */
export function sampleEnv(def: Definition, node: FlowNode, run: RunState | null): SampleEnv {
	const trigger = def.nodes.find((n) => n.type === 'trigger');
	let input: unknown = {};
	try {
		input = JSON.parse((trigger?.config as TriggerConfig | undefined)?.sampleInput || '{}');
	} catch {
		/* keep {} */
	}
	const steps: Record<string, unknown> = {};
	if (run) {
		for (const [id, st] of Object.entries(run.nodes ?? {})) {
			const n = run.flow.nodes.find((x) => x.id === id);
			if (!n || st.output === undefined) continue;
			if (n.type === 'trigger') input = st.output;
			else steps[n.key] = st.output;
		}
	}
	for (const up of upstream(def, node.id)) {
		if (up.type !== 'trigger' && steps[up.key] === undefined) steps[up.key] = placeholder({ name: up.key, type: 'object', fields: stepOutputs(up, undefined) });
	}
	const vars: Record<string, unknown> = { ...(run?.vars ?? {}) };
	for (const v of def.settings.variables ?? []) {
		if (vars[v.name] === undefined) {
			try {
				vars[v.name] = JSON.parse(v.initial ?? 'null');
			} catch {
				vars[v.name] = null;
			}
		}
	}
	const env: SampleEnv = {
		input,
		steps,
		vars,
		events: {},
		key: run?.key ?? '',
		run: { id: run ? 'vflow-sample' : 'vflow-sample', callbackUrl: run?.callbackUrl ?? 'http://localhost:8080/hooks/sample', startedAt: run?.startedAt ?? new Date().toISOString() }
	};
	const each = enclosingEach(def, node.id);
	if (each) {
		const items = (run?.items?.[each.id] ?? [])[0]?.item;
		env.item = items ?? placeholderItem(def, each, env);
		env.index = 0;
	}
	return env;
}

/** A sample item for a For each body: the first element of its items list in the sample, if it can be found. */
function placeholderItem(def: Definition, each: FlowNode, env: SampleEnv): unknown {
	const path = (each.config as ForEachConfig).items.trim();
	let v: unknown = env;
	for (const part of path.split('.')) v = v && typeof v === 'object' ? (v as Record<string, unknown>)[part] : undefined;
	return Array.isArray(v) ? v[0] : {};
}

/** A placeholder value for a field, so a template that reads it still renders. */
export function placeholder(x: ConnectorField): unknown {
	switch (x.type) {
		case 'object':
			return Object.fromEntries((x.fields ?? []).map((sub) => [sub.name, placeholder(sub)]));
		case 'array':
			return x.fields?.length ? [placeholder({ ...x, type: 'object' })] : [];
		case 'integer':
		case 'number':
			return 0;
		case 'boolean':
			return false;
		case 'date':
			return '2026-01-31';
		case 'datetime':
			return '2026-01-31T09:00:00Z';
		case 'string':
			return `<${x.name}>`;
		default:
			return null;
	}
}

export const sourceAccent = (type?: FlowNode['type']) => (type ? NODE_SPECS[type].accent : 'var(--slate-300)');
