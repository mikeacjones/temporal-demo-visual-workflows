import { connectorSpec } from './connectors';
import { makeNode, newNodeId } from './nodes';
import { isConnector, type ConnectorConfig, type ConnectorType, type Definition, type FlowNode, type SagaConfig, type SagaMember } from './types';

// Sagas on the canvas (internal/flow/saga.go). A Saga step lists its members;
// each member says whether its failure unwinds the saga, and what undoes it:
// a compensation step (the first of a chain that only the saga runs), or its
// connector's built-in rollback. The links the canvas draws (member to saga,
// member to compensation) are these settings, not control-flow edges.

export interface SagaRole {
	saga: FlowNode;
	member: SagaMember;
}

const membersOf = (saga: FlowNode) => ((saga.config as SagaConfig).members ??= []);

/** Each member step's role, by step ID. */
export function sagaRoles(def: Pick<Definition, 'nodes'>): Map<string, SagaRole> {
	const out = new Map<string, SagaRole>();
	for (const saga of def.nodes) {
		if (saga.type !== 'saga') continue;
		for (const member of (saga.config as SagaConfig).members ?? []) if (!out.has(member.step)) out.set(member.step, { saga, member });
	}
	return out;
}

/** Each compensation chain's first step, mapped to the member it undoes. */
export function compensationSteps(def: Pick<Definition, 'nodes'>): Map<string, string> {
	const out = new Map<string, string>();
	for (const [step, role] of sagaRoles(def)) if (role.member.compensation) out.set(role.member.compensation, step);
	return out;
}

/** The connector operation's built-in rollback, if the step has one. */
export function builtInRollback(step: FlowNode) {
	if (!isConnector(step.type)) return undefined;
	const spec = connectorSpec(step.type as ConnectorType);
	const op = spec?.operations.find((o) => o.name === (step.config as ConnectorConfig).operation);
	const comp = op?.compensation;
	if (!spec || !comp) return undefined;
	return { spec, op: op!, comp, undo: spec.operations.find((o) => o.name === comp.operation) };
}

/** Add a step to a saga (leaving any other first). It unwinds the saga, and uses its built-in rollback if it has one. */
export function joinSaga(def: Definition, saga: FlowNode, step: FlowNode): void {
	leaveSaga(def, step.id);
	membersOf(saga).push({ step: step.id, unwinds: true, ...(builtInRollback(step) ? { builtIn: true } : {}) });
}

export function leaveSaga(def: Definition, stepId: string): void {
	for (const n of def.nodes) {
		if (n.type !== 'saga') continue;
		const c = n.config as SagaConfig;
		if (c.members?.some((m) => m.step === stepId)) c.members = c.members.filter((m) => m.step !== stepId);
	}
}

/** Link a member to the step that undoes it. */
export function setCompensation(member: SagaMember, stepId: string | undefined): void {
	member.compensation = stepId;
	if (stepId) delete member.builtIn;
}

/** When steps go: memberships and compensations that named them go too. */
export function forgetSteps(def: Definition, gone: Set<string>): void {
	for (const n of def.nodes) {
		if (n.type !== 'saga') continue;
		const c = n.config as SagaConfig;
		c.members = (c.members ?? []).filter((m) => !gone.has(m.step));
		for (const m of c.members) if (m.compensation && gone.has(m.compensation)) delete m.compensation;
	}
}

/**
 * Customize a built-in rollback: a real compensation step of the rollback's
 * operation, below its member, with the rollback's mapping written against the
 * member's output ({{ out.id }} becomes {{ steps.charge.id }}).
 */
export function customizeRollback(def: Definition, member: SagaMember): FlowNode | undefined {
	const step = def.nodes.find((n) => n.id === member.step);
	const rb = step && builtInRollback(step);
	if (!step || !rb?.undo) return undefined;
	const node = makeNode(step.type, { x: step.position.x, y: step.position.y + 220 }, def.nodes, rb.undo.name);
	node.id = newNodeId();
	const own = step.config as ConnectorConfig;
	const c = node.config as ConnectorConfig;
	c.settings = Object.fromEntries((rb.comp.settings ?? []).filter((k) => own.settings?.[k] !== undefined).map((k) => [k, own.settings![k]]));
	c.mapping = Object.fromEntries(
		Object.entries(rb.comp.mapping).map(([k, v]) => [
			k,
			v.replace(/\{\{\s*out\./g, `{{ steps.${step.key}.`).replace(/\{\{\s*arg\.(\w+)\s*\}\}/g, (m, arg: string) => own.mapping?.[arg] ?? m)
		])
	);
	node.label = `${rb.undo.label} (undo ${step.label})`;
	def.nodes.push(node);
	setCompensation(member, node.id);
	return node;
}

/** The steps on the main path: reachable from the trigger. Saga members come from these. */
export function mainPath(def: Pick<Definition, 'nodes' | 'edges'>): Set<string> {
	const trigger = def.nodes.find((n) => n.type === 'trigger');
	const seen = new Set<string>(trigger ? [trigger.id] : []);
	const stack = trigger ? [trigger.id] : [];
	while (stack.length) {
		const id = stack.pop()!;
		for (const e of def.edges) if (e.source === id && !seen.has(e.target)) (seen.add(e.target), stack.push(e.target));
	}
	return seen;
}

/** Steps that can be a compensation: nothing leads into them and they're no one's yet (or are this member's). */
export function compensationCandidates(def: Definition, member: SagaMember): FlowNode[] {
	const taken = compensationSteps(def);
	const led = new Set(def.edges.map((e) => e.target));
	const roles = sagaRoles(def);
	return def.nodes.filter(
		(n) =>
			!['trigger', 'onStart', 'onSignal', 'onUpdate', 'saga', 'end', 'respond'].includes(n.type) &&
			n.id !== member.step &&
			!led.has(n.id) &&
			!roles.has(n.id) &&
			(!taken.has(n.id) || member.compensation === n.id)
	);
}
