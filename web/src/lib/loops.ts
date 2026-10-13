import type { Definition } from './types';

type Graph = Pick<Definition, 'nodes' | 'edges'>;

/** The steps a loop repeats: everything reachable from its body outcome without passing back through the loop. Mirrors flow.LoopBody. */
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

/** Edges from a loop's body back into the loop. Drawn, never followed: the loop starts its next pass instead. Mirrors flow.BackEdges. */
export function backEdges(def: Graph): Set<string> {
	const back = new Set<string>();
	for (const n of def.nodes) {
		if (n.type !== 'loop') continue;
		const body = loopBody(def, n.id);
		for (const e of def.edges) if (e.target === n.id && body.has(e.source)) back.add(e.id);
	}
	return back;
}
