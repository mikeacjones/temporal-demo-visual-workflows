import dagre from '@dagrejs/dagre';
import { HANDLES, isEntry, type Definition, type Direction, type FlowNode, type OnSignalConfig, type OnUpdateConfig, type Position, type SagaConfig, type ForEachConfig, type LoopConfig } from './types';
import { backEdges, hasBody, loopBody } from './loops';

// A built-in rollback's ghost: tidy-up leaves it room in its saga's track.
const GHOST_SIZE = { width: 200, height: 48 };

/** One path of a flow: the main path from the trigger, or a lane from an On signal / On update entry. */
export interface FlowPath {
	entryId: string;
	kind: 'main' | 'init' | 'signal' | 'update' | 'saga' | 'compensation';
	label: string;
	nodeIds: string[];
}

export interface Size {
	width: number;
	height: number;
}

export const DEFAULT_SIZE: Size = { width: 248, height: 96 };
const LANE_GAP = 90;
const BAND_PAD = 16;

function laneLabel(n: FlowNode, undoes?: FlowNode): string {
	if (undoes) return `Undoes ${undoes.label} · only if the saga unwinds`;
	if (n.type === 'saga') return `${n.label} · after it unwinds`;
	if (n.type === 'onStart') return 'On start · runs first';
	if (n.type === 'onSignal') return `On signal: ${(n.config as OnSignalConfig).signalName}`;
	if (n.type === 'onUpdate') return `On update: ${(n.config as OnUpdateConfig).updateName}`;
	return 'Main path';
}

/** Splits a flow into its main path and lanes. Unreachable steps join the main path. */
export function flowPaths(def: Pick<Definition, 'nodes' | 'edges'>): FlowPath[] {
	const next = new Map<string, string[]>();
	for (const e of def.edges) next.set(e.source, [...(next.get(e.source) ?? []), e.target]);
	const owned = new Set<string>();
	const paths: FlowPath[] = [];
	// A compensation step starts a chain only its saga runs: a lane of its own.
	const undoes = new Map<string, FlowNode>();
	for (const saga of def.nodes) {
		if (saga.type !== 'saga') continue;
		for (const m of (saga.config as SagaConfig).members ?? []) {
			const member = def.nodes.find((n) => n.id === m.step);
			if (m.compensation && member) undoes.set(m.compensation, member);
		}
	}
	const entries = def.nodes.filter((n) => isEntry(n.type) || undoes.has(n.id));
	// Main path first, then the init lane, then lanes in definition order,
	// then sagas and their compensations.
	const rank = (n: FlowNode) => (n.type === 'trigger' ? 0 : n.type === 'onStart' ? 1 : n.type === 'saga' ? 3 : undoes.has(n.id) ? 4 : 2);
	entries.sort((a, b) => rank(a) - rank(b));
	for (const entry of entries) {
		const ids: string[] = [];
		const stack = [entry.id];
		while (stack.length) {
			const id = stack.pop()!;
			if (owned.has(id)) continue;
			owned.add(id);
			ids.push(id);
			stack.push(...(next.get(id) ?? []));
		}
		paths.push({
			entryId: entry.id,
			kind: undoes.has(entry.id)
				? 'compensation'
				: entry.type === 'trigger'
					? 'main'
					: entry.type === 'onStart'
						? 'init'
						: entry.type === 'onSignal'
							? 'signal'
							: entry.type === 'saga'
								? 'saga'
								: 'update',
			label: laneLabel(entry, undoes.get(entry.id)),
			nodeIds: ids
		});
	}
	const orphans = def.nodes.filter((n) => !owned.has(n.id)).map((n) => n.id);
	const main = paths.find((p) => p.kind === 'main');
	if (orphans.length) {
		if (main) main.nodeIds.push(...orphans);
		else paths.unshift({ entryId: orphans[0], kind: 'main', label: 'Main path', nodeIds: orphans });
	}
	return paths;
}

/**
 * Lays every path out with dagre in the given direction. The main path comes
 * first; each lane is its own band beside it (LR: below, TB: to the right).
 * Main-outcome edges get more weight so the happy path runs straight.
 */
export function tidy(def: Pick<Definition, 'nodes' | 'edges'>, direction: Direction, sizes: Map<string, Size>): Record<string, Position> {
	const out: Record<string, Position> = {};
	const paths = flowPaths(def);
	const size = (id: string) => sizes.get(id) ?? DEFAULT_SIZE;
	// The main path first, then each saga as a ladder beside its members,
	// then the other lanes beside all of that.
	const main = paths.find((p) => p.kind === 'main');
	if (main) Object.assign(out, placePath(def, main.nodeIds, direction, sizes, { x: 0, y: 0 }));
	const arranged = new Set<string>(Object.keys(out));
	for (const saga of def.nodes.filter((n) => n.type === 'saga')) {
		for (const id of arrangeSaga(def, saga, paths, out, direction, size)) arranged.add(id);
	}
	const across = (p: Position, id: string) => (direction === 'LR' ? p.y + size(id).height : p.x + size(id).width);
	let offset = Object.entries(out).reduce((m, [id, p]) => Math.max(m, across(p, id)), 0) + LANE_GAP;
	for (const path of paths) {
		if (path.kind === 'main' || path.nodeIds.every((id) => arranged.has(id))) continue;
		const placed = placePath(def, path.nodeIds, direction, sizes, direction === 'LR' ? { x: 0, y: offset + BAND_PAD } : { x: offset + BAND_PAD, y: 0 });
		Object.assign(out, placed);
		offset = Object.entries(placed).reduce((m, [id, p]) => Math.max(m, across(p, id)), offset) + LANE_GAP + BAND_PAD;
	}
	return out;
}

// Room around a body inside its box: the box's label sits in the top.
const BODY_PAD = { side: 24, top: 36, bottom: 20 };
// From a loop's centre line, where its other outcomes run, to its box; and
// from the loop to the box below (TB) or beside it (LR).
const RAIL_GAP = 48;
const BODY_GAP = 44;

/**
 * Lays some steps out with dagre, their top-left corner at origin. Main-outcome
 * edges get more weight so the happy path runs straight.
 *
 * A Loop or For each and its body are placed as one block: the body, laid
 * out on its own first, in its scope box past the loop (TB: below and to the
 * right; LR: right and below), clear of the loop's centre line. The loop's
 * other outcomes leave along that line, so the steps after the loop come
 * after the box. The block is centred on the loop, so they line up with it.
 */
function placePath(def: Pick<Definition, 'nodes' | 'edges'>, nodeIds: string[], rankdir: Direction, sizes: Map<string, Size>, origin: Position): Record<string, Position> {
	const byId = new Map(def.nodes.map((n) => [n.id, n]));
	const ids = new Set(nodeIds);
	const size = (id: string) => sizes.get(id) ?? DEFAULT_SIZE;

	// Bodies, outermost; a nested body is placed within its own.
	const bodies = new Map<string, Set<string>>();
	for (const n of def.nodes) {
		if (!ids.has(n.id) || !hasBody(n)) continue;
		const body = new Set([...loopBody(def, n.id)].filter((id) => ids.has(id)));
		if (body.size) bodies.set(n.id, body);
	}
	for (const [owner] of bodies) if ([...bodies].some(([o, body]) => o !== owner && body.has(owner))) bodies.delete(owner);
	const blockOf = new Map<string, string>();
	const blocks = new Map<string, { rel: Record<string, Position>; size: Size }>();
	const tb = rankdir === 'TB';
	for (const [owner, body] of bodies) {
		const inner = placePath(def, [...body], rankdir, sizes, { x: 0, y: 0 });
		let bw = 0, bh = 0;
		for (const id of body) {
			bw = Math.max(bw, inner[id].x + size(id).width);
			bh = Math.max(bh, inner[id].y + size(id).height);
		}
		bw += BODY_PAD.side * 2;
		bh += BODY_PAD.top + BODY_PAD.bottom;
		const o = size(owner);
		// Owner and box corners, and the block's size, centred on the owner.
		const half = tb ? Math.max(o.width / 2, RAIL_GAP + bw) : Math.max(o.height / 2, RAIL_GAP + bh);
		const blockSize = tb ? { width: half * 2, height: o.height + BODY_GAP + bh } : { width: o.width + BODY_GAP + bw, height: half * 2 };
		const ownerAt = tb ? { x: half - o.width / 2, y: 0 } : { x: 0, y: half - o.height / 2 };
		const boxAt = tb ? { x: half + RAIL_GAP, y: o.height + BODY_GAP } : { x: o.width + BODY_GAP, y: half + RAIL_GAP };
		const rel: Record<string, Position> = { [owner]: ownerAt };
		for (const id of body) rel[id] = { x: boxAt.x + BODY_PAD.side + inner[id].x, y: boxAt.y + BODY_PAD.top + inner[id].y };
		blocks.set(owner, { rel, size: blockSize });
		for (const id of body) blockOf.set(id, owner);
	}
	const at = (id: string) => blockOf.get(id) ?? id;
	const nodeSize = (id: string) => blocks.get(id)?.size ?? size(id);

	const g = new dagre.graphlib.Graph();
	g.setGraph({ rankdir, nodesep: rankdir === 'LR' ? 46 : 70, ranksep: rankdir === 'LR' ? 64 : 56, marginx: 0, marginy: 0 });
	g.setDefaultEdgeLabel(() => ({}));
	const placed = nodeIds.filter((id) => !blockOf.has(id));
	for (const id of placed) g.setNode(id, { ...nodeSize(id) });
	// Order constraints keep each step's main-outcome child before its
	// secondary children: above them in LR, left of them in TB, so unhappy
	// paths always branch to the same side.
	const constraints: { left: string; right: string }[] = [];
	const mainChild = new Map<string, string>();
	// A loop's back edges would make dagre reverse part of the body; lay
	// the body out forward and let the back edge curve home.
	const back = backEdges(def);
	const edges = def.edges.filter((e) => ids.has(e.source) && ids.has(e.target) && !back.has(e.id) && at(e.source) !== at(e.target));
	for (const e of edges) {
		const src = byId.get(e.source);
		const main = src ? (e.sourceHandle || 'out') === HANDLES[src.type][0] : true;
		g.setEdge(at(e.source), at(e.target), { weight: main ? 8 : 1, minlen: 1 });
		if (main && !mainChild.has(at(e.source))) mainChild.set(at(e.source), at(e.target));
	}
	for (const e of edges) {
		const m = mainChild.get(at(e.source));
		if (!m || m === at(e.target)) continue;
		// A loop's other outcomes branch away from its box, to the near side.
		constraints.push(blocks.has(e.source) ? { left: at(e.target), right: m } : { left: m, right: at(e.target) });
	}
	dagre.layout(g, { constraints });
	let minX = Infinity, minY = Infinity;
	for (const id of placed) {
		const n = g.node(id);
		const s = nodeSize(id);
		minX = Math.min(minX, n.x - s.width / 2);
		minY = Math.min(minY, n.y - s.height / 2);
	}
	const out: Record<string, Position> = {};
	for (const id of placed) {
		const n = g.node(id);
		const s = nodeSize(id);
		const x = Math.round(n.x - s.width / 2 - minX + origin.x), y = Math.round(n.y - s.height / 2 - minY + origin.y);
		const block = blocks.get(id);
		if (block) for (const [bid, p] of Object.entries(block.rel)) out[bid] = { x: Math.round(x + p.x), y: Math.round(y + p.y) };
		else out[id] = { x, y };
	}
	return out;
}

const TRACK_GAP = 90;

/**
 * Lays a saga out as a ladder, around its members (already placed on the main
 * path). Room opens up before the first member for the Saga step, which heads
 * a track beside the members; each member's compensation chain sits in that
 * track level with its member; the Saga's outcomes go past the track. In TB the
 * flow runs down and the track is to the right; in LR it runs right and the
 * track is below: the same layout with the axes swapped. Returns the steps it
 * placed.
 */
function arrangeSaga(
	def: Pick<Definition, 'nodes' | 'edges'>,
	saga: FlowNode,
	paths: FlowPath[],
	out: Record<string, Position>,
	direction: Direction,
	size: (id: string) => Size
): string[] {
	const members = ((saga.config as SagaConfig).members ?? []).filter((m) => out[m.step]);
	if (!members.length) return [];
	// along: the flow's axis; across: the track's side.
	const A = direction === 'TB' ? 'y' : 'x';
	const C = direction === 'TB' ? 'x' : 'y';
	const lenA = (id: string) => (direction === 'TB' ? size(id).height : size(id).width);
	const lenC = (id: string) => (direction === 'TB' ? size(id).width : size(id).height);
	const ids = members.map((m) => m.step);
	const minA = Math.min(...ids.map((id) => out[id][A]));

	// Room for the Saga step before the first member.
	const header = lenA(saga.id) + 40;
	for (const p of Object.values(out)) if (p[A] >= minA - 1) p[A] += header;
	const startA = Math.min(...ids.map((id) => out[id][A]));
	const endA = Math.max(...ids.map((id) => out[id][A] + lenA(id)));
	// The track: past anything placed level with the members.
	const trackC =
		Math.max(...Object.entries(out).filter(([id, p]) => p[A] < endA && p[A] + lenA(id) > startA).map(([id, p]) => p[C] + lenC(id))) + TRACK_GAP;

	const placed: string[] = [saga.id];
	out[saga.id] = { [A]: minA, [C]: trackC } as unknown as Position;
	let farC = trackC + lenC(saga.id);

	// Compensation chains, level with their members, never overlapping.
	const chainRankdir: Direction = direction === 'TB' ? 'LR' : 'TB';
	let nextA = -Infinity;
	for (const m of [...members].sort((a, b) => out[a.step][A] - out[b.step][A])) {
		const chain = paths.find((p) => p.kind === 'compensation' && p.entryId === m.compensation);
		if (!chain) {
			// A built-in rollback is drawn as a ghost here: leave it room.
			if (m.builtIn) {
				const at = Math.max(out[m.step][A], nextA);
				nextA = at + (direction === 'TB' ? GHOST_SIZE.height : GHOST_SIZE.width) + 24;
				farC = Math.max(farC, trackC + (direction === 'TB' ? GHOST_SIZE.width : GHOST_SIZE.height));
			}
			continue;
		}
		const rel = placePath(def, chain.nodeIds, chainRankdir, new Map(chain.nodeIds.map((id) => [id, size(id)])), { x: 0, y: 0 });
		const at = Math.max(out[m.step][A], nextA);
		let reach = at;
		for (const id of chain.nodeIds) {
			out[id] = { [A]: at + rel[id][A], [C]: trackC + rel[id][C] } as unknown as Position;
			reach = Math.max(reach, out[id][A] + lenA(id));
			farC = Math.max(farC, out[id][C] + lenC(id));
			placed.push(id);
		}
		nextA = reach + 24;
	}

	// The Saga's outcomes, past the track.
	const outcomes = paths.find((p) => p.kind === 'saga' && p.entryId === saga.id);
	const rest = (outcomes?.nodeIds ?? []).filter((id) => id !== saga.id);
	if (rest.length) {
		const rel = placePath(def, rest, direction, new Map(rest.map((id) => [id, size(id)])), { x: 0, y: 0 });
		for (const id of rest) {
			out[id] = { [A]: minA + rel[id][A], [C]: farC + TRACK_GAP + rel[id][C] } as unknown as Position;
			placed.push(id);
		}
	}
	return placed;
}

/** A scope on the canvas: a saga's box around its members, its Saga step and its compensations, or a Loop or For each's box around its body. */
export interface Scope {
	id: string;
	kind: 'saga' | 'loop' | 'forEach';
	label: string;
	/** The box's colour: the owning step's accent. */
	color: string;
	x: number;
	y: number;
	width: number;
	height: number;
	/** Steps that sit inside the box without being in the saga. */
	strangers: string[];
}

export function sagaScopes(def: Pick<Definition, 'nodes' | 'edges'>, positions: Map<string, Position>, sizes: Map<string, Size>): Scope[] {
	const next = new Map<string, string[]>();
	for (const e of def.edges) next.set(e.source, [...(next.get(e.source) ?? []), e.target]);
	const scopes: Scope[] = [];
	for (const saga of def.nodes.filter((n) => n.type === 'saga')) {
		const members = (saga.config as SagaConfig).members ?? [];
		if (!members.length) continue;
		const inside = new Set<string>([saga.id, ...members.map((m) => m.step)]);
		for (const m of members) {
			const stack = m.compensation ? [m.compensation] : [];
			while (stack.length) {
				const id = stack.pop()!;
				if (inside.has(id)) continue;
				inside.add(id);
				stack.push(...(next.get(id) ?? []));
			}
		}
		let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
		for (const id of inside) {
			const p = positions.get(id);
			if (!p) continue;
			const s = sizes.get(id) ?? DEFAULT_SIZE;
			minX = Math.min(minX, p.x);
			minY = Math.min(minY, p.y);
			maxX = Math.max(maxX, p.x + s.width);
			maxY = Math.max(maxY, p.y + s.height);
		}
		if (!isFinite(minX)) continue;
		const box = { x: minX - 28, y: minY - 44, width: maxX - minX + 56, height: maxY - minY + 72 };
		const strangers = strangersIn(def, box, inside, positions, sizes);
		const undo = members.filter((m) => m.compensation || m.builtIn).length;
		scopes.push({ id: saga.id, kind: 'saga', color: 'var(--pink)', label: `${saga.label} · ${members.length} member${members.length === 1 ? '' : 's'} · ${undo} with an undo`, ...box, strangers });
	}
	return scopes;
}

/** Steps whose centre sits inside a box they don't belong to. */
function strangersIn(def: Pick<Definition, 'nodes'>, box: Omit<Scope, 'id' | 'kind' | 'label' | 'color' | 'strangers'>, inside: Set<string>, positions: Map<string, Position>, sizes: Map<string, Size>): string[] {
	return def.nodes
		.filter((n) => !inside.has(n.id) && positions.get(n.id))
		.filter((n) => {
			const p = positions.get(n.id)!;
			const s = sizes.get(n.id) ?? DEFAULT_SIZE;
			const cx = p.x + s.width / 2, cy = p.y + s.height / 2;
			return cx > box.x && cx < box.x + box.width && cy > box.y && cy < box.y + box.height;
		})
		.map((n) => n.id);
}

/** A box around each Loop's and For each's body: the steps it repeats, or runs per item. */
export function bodyScopes(def: Pick<Definition, 'nodes' | 'edges'>, positions: Map<string, Position>, sizes: Map<string, Size>): Scope[] {
	const scopes: Scope[] = [];
	for (const owner of def.nodes.filter(hasBody)) {
		const body = loopBody(def, owner.id);
		let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
		for (const id of body) {
			const p = positions.get(id);
			if (!p) continue;
			const s = sizes.get(id) ?? DEFAULT_SIZE;
			minX = Math.min(minX, p.x);
			minY = Math.min(minY, p.y);
			maxX = Math.max(maxX, p.x + s.width);
			maxY = Math.max(maxY, p.y + s.height);
		}
		if (!isFinite(minX)) continue;
		const box = { x: minX - BODY_PAD.side, y: minY - BODY_PAD.top, width: maxX - minX + BODY_PAD.side * 2, height: maxY - minY + BODY_PAD.top + BODY_PAD.bottom };
		const forEach = owner.type === 'forEach';
		// The loop's card heads the box, so the label says what the box is.
		const each = owner.config as ForEachConfig;
		const label = forEach
			? `Runs per item of ${each.items}${(each.concurrency ?? 1) > 1 ? ` · ${each.concurrency} at a time` : ''}`
			: `Repeats until ${(owner.config as LoopConfig).until}`;
		scopes.push({ id: owner.id, kind: forEach ? 'forEach' : 'loop', color: forEach ? 'var(--cyan)' : 'var(--lime)', label, ...box, strangers: strangersIn(def, box, new Set([owner.id, ...body]), positions, sizes) });
	}
	return scopes;
}

export interface Band {
	id: string;
	label: string;
	kind: FlowPath['kind'];
	x: number;
	y: number;
	width: number;
	height: number;
}

/** Faint background rectangles behind each lane (not the main path). */
export function laneBands(def: Pick<Definition, 'nodes' | 'edges'>, positions: Map<string, Position>, sizes: Map<string, Size>): Band[] {
	return flowPaths(def)
		.filter((p) => p.kind !== 'main' && p.kind !== 'compensation' && p.kind !== 'saga')
		.map((p) => {
			let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
			for (const id of p.nodeIds) {
				const pos = positions.get(id);
				if (!pos) continue;
				const s = sizes.get(id) ?? DEFAULT_SIZE;
				minX = Math.min(minX, pos.x);
				minY = Math.min(minY, pos.y);
				maxX = Math.max(maxX, pos.x + s.width);
				maxY = Math.max(maxY, pos.y + s.height);
			}
			return {
				id: `band-${p.entryId}`,
				label: p.label,
				kind: p.kind,
				x: minX - BAND_PAD,
				y: minY - BAND_PAD - 16,
				width: maxX - minX + BAND_PAD * 2,
				height: maxY - minY + BAND_PAD * 2 + 30
			};
		})
		.filter((b) => isFinite(b.x));
}

/**
 * Suggests the other direction when the user is clearly building that way:
 * at least 3 connected steps and most edges running down while the flow is
 * LR (or across while it is TB).
 */
export function suggestDirection(def: Pick<Definition, 'nodes' | 'edges'>, direction: Direction): Direction | null {
	if (def.edges.length < 2) return null;
	const pos = new Map(def.nodes.map((n) => [n.id, n.position]));
	let down = 0, across = 0;
	for (const e of def.edges) {
		const a = pos.get(e.source), b = pos.get(e.target);
		if (!a || !b) continue;
		const dx = b.x - a.x, dy = b.y - a.y;
		if (Math.abs(dy) > Math.abs(dx) && dy > 0) down++;
		else if (Math.abs(dx) >= Math.abs(dy) && dx > 0) across++;
	}
	const total = down + across;
	if (total < 2) return null;
	if (direction === 'LR' && down / total > 0.6) return 'TB';
	if (direction === 'TB' && across / total > 0.6) return 'LR';
	return null;
}
