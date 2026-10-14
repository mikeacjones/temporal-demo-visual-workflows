import dagre from '@dagrejs/dagre';
import { HANDLES, isEntry, type Definition, type Direction, type FlowNode, type OnSignalConfig, type OnUpdateConfig, type Position } from './types';
import { backEdges } from './loops';

/** One path of a flow: the main path from the trigger, or a lane from an On signal / On update entry. */
export interface FlowPath {
	entryId: string;
	kind: 'main' | 'init' | 'signal' | 'update';
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

function laneLabel(n: FlowNode): string {
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
	const entries = def.nodes.filter((n) => isEntry(n.type));
	// Main path first, then the init lane, then lanes in definition order.
	const rank = (t: string) => (t === 'trigger' ? 0 : t === 'onStart' ? 1 : 2);
	entries.sort((a, b) => rank(a.type) - rank(b.type));
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
			kind: entry.type === 'trigger' ? 'main' : entry.type === 'onStart' ? 'init' : entry.type === 'onSignal' ? 'signal' : 'update',
			label: laneLabel(entry),
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
	const byId = new Map(def.nodes.map((n) => [n.id, n]));
	const out: Record<string, Position> = {};
	let offset = 0;
	for (const path of flowPaths(def)) {
		const ids = new Set(path.nodeIds);
		const g = new dagre.graphlib.Graph();
		g.setGraph({ rankdir: direction, nodesep: direction === 'LR' ? 46 : 70, ranksep: direction === 'LR' ? 64 : 56, marginx: 0, marginy: 0 });
		g.setDefaultEdgeLabel(() => ({}));
		for (const id of path.nodeIds) g.setNode(id, { ...(sizes.get(id) ?? DEFAULT_SIZE) });
		// Order constraints keep each step's main-outcome child before its
		// secondary children: above them in LR, left of them in TB, so unhappy
		// paths always branch to the same side.
		const constraints: { left: string; right: string }[] = [];
		const mainChild = new Map<string, string>();
		// A loop's back edges would make dagre reverse part of the body; lay
		// the body out forward and let the back edge curve home.
		const back = backEdges(def);
		for (const e of def.edges) {
			if (!ids.has(e.source) || !ids.has(e.target) || back.has(e.id)) continue;
			const src = byId.get(e.source);
			const main = src ? (e.sourceHandle || 'out') === HANDLES[src.type][0] : true;
			g.setEdge(e.source, e.target, { weight: main ? 8 : 1, minlen: 1 });
			if (main && !mainChild.has(e.source)) mainChild.set(e.source, e.target);
		}
		for (const e of def.edges) {
			if (back.has(e.id)) continue;
			const m = mainChild.get(e.source);
			if (m && m !== e.target && ids.has(e.target)) constraints.push({ left: m, right: e.target });
		}
		dagre.layout(g, { constraints });
		let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
		for (const id of path.nodeIds) {
			const n = g.node(id);
			const s = sizes.get(id) ?? DEFAULT_SIZE;
			minX = Math.min(minX, n.x - s.width / 2);
			minY = Math.min(minY, n.y - s.height / 2);
			maxX = Math.max(maxX, n.x + s.width / 2);
			maxY = Math.max(maxY, n.y + s.height / 2);
		}
		for (const id of path.nodeIds) {
			const n = g.node(id);
			const s = sizes.get(id) ?? DEFAULT_SIZE;
			const x = n.x - s.width / 2 - minX;
			const y = n.y - s.height / 2 - minY;
			out[id] = direction === 'LR' ? { x: Math.round(x), y: Math.round(y + offset) } : { x: Math.round(x + offset), y: Math.round(y) };
		}
		offset += (direction === 'LR' ? maxY - minY : maxX - minX) + LANE_GAP + (path.kind === 'main' ? 0 : BAND_PAD);
	}
	return out;
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
		.filter((p) => p.kind !== 'main')
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
