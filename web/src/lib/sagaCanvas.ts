import { MarkerType, type Edge, type Node } from '@xyflow/svelte';
import { builtInRollback, compensationSteps, sagaRoles } from './saga';
import type { Definition, Direction, RunState } from './types';

// What the canvas draws for sagas: a link from each member to its Saga step
// (solid for a member whose failure unwinds the saga, hollow for a handled
// one), a link from each member to what undoes it, and a ghost node for a
// built-in rollback. These come from the Saga steps' settings, not from
// control-flow edges.

/** A card's part in a saga, for its saga strip. */
export interface CardSaga {
	/** Member: does its failure unwind the saga? */
	member?: 'unwinds' | 'handled';
	sagaId?: string;
	sagaLabel?: string;
	/** What undoes it: a step's label, or a built-in rollback's. */
	undo?: string;
	builtIn?: boolean;
	/** A compensation step: the member it undoes. */
	compensates?: string;
	/** In a run: this member's failure unwound the saga. */
	trigger?: boolean;
}

export const GHOST = 'ghost:';
export const isGhost = (id: string) => id.startsWith(GHOST);

export function cardSagas(def: Pick<Definition, 'nodes'>, state?: RunState | null): Map<string, CardSaga> {
	const out = new Map<string, CardSaga>();
	const label = (id: string) => def.nodes.find((n) => n.id === id)?.label ?? id;
	for (const [step, { saga, member }] of sagaRoles(def)) {
		const node = def.nodes.find((n) => n.id === step);
		const rollback = member.builtIn && node ? builtInRollback(node) : undefined;
		out.set(step, {
			member: member.unwinds ? 'unwinds' : 'handled',
			sagaId: saga.id,
			sagaLabel: saga.label,
			undo: member.compensation ? label(member.compensation) : rollback ? (rollback.undo?.label ?? rollback.comp.operation) : undefined,
			builtIn: !!rollback,
			trigger: state?.sagas?.[saga.id]?.trigger === step
		});
	}
	for (const [comp, member] of compensationSteps(def)) out.set(comp, { ...out.get(comp), compensates: label(member) });
	return out;
}

/**
 * The saga links, as Svelte Flow edges. A member's link to its Saga shows only
 * while the saga or one of its members is selected (in a run, only the link of
 * the member that unwound it): the scope box shows membership the rest of the
 * time. A link to what undoes a member is numbered with its place in the
 * unwind: 1 runs first.
 */
export function sagaEdges(def: Pick<Definition, 'nodes' | 'edges'>, state?: RunState | null, interactive = true, selected?: string | null): Edge[] {
	const edges: Edge[] = [];
	const roles = sagaRoles(def);
	const order = flowOrder(def);
	for (const [step, { saga, member }] of roles) {
		const st = state?.sagas?.[saga.id];
		const trigger = st?.trigger === step;
		const focused = !!selected && (selected === saga.id || roles.get(selected)?.saga.id === saga.id);
		edges.push({
			id: `saga:${saga.id}:${step}`,
			source: step,
			sourceHandle: 'saga',
			target: saga.id,
			targetHandle: 'members',
			type: 'default',
			class: ['saga-link', member.unwinds ? 'unwinds' : 'handled', trigger ? 'trigger' : ''].join(' '),
			hidden: state ? !trigger : !focused,
			selectable: interactive,
			deletable: interactive
		});
		const undoTarget = member.compensation ?? (member.builtIn ? GHOST + step : undefined);
		if (!undoTarget) continue;
		// Its place in the unwind: newest first.
		const undoers = [...roles].filter(([, r]) => r.saga.id === saga.id && (r.member.compensation || r.member.builtIn)).map(([id]) => id);
		const registered = st?.registered ?? [];
		const place = registered.includes(step)
			? registered.length - registered.indexOf(step)
			: undoers.sort((x, y) => (order.get(x) ?? 0) - (order.get(y) ?? 0)).length - undoers.indexOf(step);
		const ran = member.compensation ? state?.nodes[member.compensation]?.status === 'completed' : state?.nodes[step]?.undo === 'done';
		edges.push({
			id: `comp:${step}`,
			source: step,
			sourceHandle: 'compensate',
			target: undoTarget,
			type: 'smoothstep',
			class: ['compensation-link', ran ? 'ran' : ''].join(' '),
			label: `↺ ${place}`,
			labelStyle: 'color: #ff6bff; background: #15171f; font-size: 10px; padding: 0 3px',
			selectable: interactive,
			deletable: interactive,
			markerEnd: { type: MarkerType.ArrowClosed, width: 14, height: 14, color: '#ff6bff' }
		});
	}
	return edges;
}

/** Each step's place in the flow's order (a topological sort): members registered in this order. */
function flowOrder(def: Pick<Definition, 'nodes' | 'edges'>): Map<string, number> {
	const indegree = new Map(def.nodes.map((n) => [n.id, 0]));
	for (const e of def.edges) indegree.set(e.target, (indegree.get(e.target) ?? 0) + 1);
	const queue = def.nodes.filter((n) => !indegree.get(n.id)).map((n) => n.id);
	const order = new Map<string, number>();
	while (queue.length) {
		const id = queue.shift()!;
		order.set(id, order.size);
		for (const e of def.edges) {
			if (e.source !== id) continue;
			const d = (indegree.get(e.target) ?? 1) - 1;
			indegree.set(e.target, d);
			if (d === 0) queue.push(e.target);
		}
	}
	return order;
}

/** A ghost's size, which tidy-up leaves room for in a saga's track. */
export const GHOST_SIZE = { width: 200, height: 48 };

/**
 * Ghost nodes for built-in rollbacks, in their saga's track (where the Saga
 * step is), level with their members like compensation steps, and never on
 * top of a compensation chain or another ghost.
 */
export function ghostNodes(def: Pick<Definition, 'nodes' | 'edges'>, state?: RunState | null, direction: Direction = 'LR'): Node[] {
	const A = direction === 'TB' ? 'y' : 'x';
	const C = direction === 'TB' ? 'x' : 'y';
	const lenA = direction === 'TB' ? GHOST_SIZE.height : GHOST_SIZE.width;
	const pos = (id: string) => def.nodes.find((n) => n.id === id)?.position;
	const ghosts: Node[] = [];
	const roles = [...sagaRoles(def)];
	for (const saga of def.nodes.filter((n) => n.type === 'saga')) {
		const members = roles.filter(([, r]) => r.saga.id === saga.id).map(([id, r]) => ({ id, member: r.member, at: pos(id) }));
		let nextA = -Infinity;
		for (const { id, member, at } of members.sort((x, y) => (x.at?.[A] ?? 0) - (y.at?.[A] ?? 0))) {
			if (!at) continue;
			if (member.compensation) {
				// A compensation step already holds this stretch of the track.
				const cp = pos(member.compensation);
				if (cp) nextA = Math.max(nextA, cp[A] + (direction === 'TB' ? 96 : 248) + 24);
				continue;
			}
			const node = def.nodes.find((n) => n.id === id);
			const rollback = member.builtIn && node ? builtInRollback(node) : undefined;
			if (!node || !rollback) continue;
			const place = Math.max(at[A], nextA);
			nextA = place + lenA + 24;
			ghosts.push({
				id: GHOST + id,
				type: 'ghost',
				position: { [A]: place, [C]: saga.position[C] } as unknown as { x: number; y: number },
				data: { label: rollback.undo?.label ?? rollback.comp.operation, note: rollback.comp.note, member: id, undo: state?.nodes[id]?.undo, direction },
				draggable: false,
				deletable: false,
				connectable: false
			});
		}
	}
	return ghosts;
}
