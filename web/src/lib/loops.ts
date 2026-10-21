import type { Definition, FlowNode, ForEachConfig } from './types';

type Graph = Pick<Definition, 'nodes' | 'edges'>;

/** Whether a node owns a body: a Loop, or a For each that runs steps. Mirrors flow.HasBody. */
export function hasBody(n: Pick<FlowNode, 'type' | 'config'>): boolean {
	return n.type === 'loop' || (n.type === 'forEach' && (n.config as ForEachConfig).run === 'steps');
}

/** The steps a Loop repeats, or a For each runs per item: everything reachable from its body outcome without passing back through it. Mirrors flow.Body. */
export function loopBody(def: Graph, loopId: string): Set<string> {
	const next = new Map<string, string[]>();
	const stack: string[] = [];
	for (const e of def.edges) {
		if (e.source === loopId) {
			if (e.sourceHandle === 'body') stack.push(e.target);
			continue;
		}
		next.set(e.source, [...(next.get(e.source) ?? []), e.target]);
	}
	const body = new Set<string>();
	while (stack.length) {
		const id = stack.pop()!;
		if (id === loopId || body.has(id)) continue;
		body.add(id);
		stack.push(...(next.get(id) ?? []));
	}
	return body;
}

/** Edges from a body back into its Loop or For each. Drawn, never followed: the next pass (or item) starts instead. Mirrors flow.BackEdges. */
export function backEdges(def: Graph): Set<string> {
	const back = new Set<string>();
	for (const n of def.nodes) {
		if (!hasBody(n)) continue;
		const body = loopBody(def, n.id);
		for (const e of def.edges) if (e.target === n.id && body.has(e.source)) back.add(e.id);
	}
	return back;
}
