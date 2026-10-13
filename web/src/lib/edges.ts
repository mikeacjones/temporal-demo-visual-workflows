import { MarkerType, type Edge } from '@xyflow/svelte';
import { HANDLES, type Definition, type RunState } from './types';
import { backEdges } from './loops';

/**
 * Svelte Flow edges for a flow. Edges from a node's main outcome are solid;
 * secondary outcomes (error, timeout, rejected, false, …) are dashed pink.
 * With a run state, edges show where the run went.
 */
export function toEdges(def: Pick<Definition, 'nodes' | 'edges'>, state?: RunState | null, interactive = true): Edge[] {
	const types = new Map(def.nodes.map((n) => [n.id, n.type]));
	const back = backEdges(def);
	return def.edges.map((e) => {
		const handle = e.sourceHandle || 'out';
		const t = types.get(e.source);
		const loopBack = back.has(e.id);
		const secondary = !loopBack && !!t && HANDLES[t][0] !== handle;
		const classes = [loopBack ? 'loop-back' : secondary ? 'secondary' : 'primary'];
		if (state) {
			const src = state.nodes[e.source];
			const dst = state.nodes[e.target];
			if (src?.status === 'completed' && src.handle === handle) {
				classes.push(dst && (dst.status === 'running' || dst.status === 'waiting') ? 'flowing' : 'taken');
			} else if (src && (src.status === 'completed' || src.status === 'skipped' || src.status === 'failed')) {
				classes.push('not-taken');
			}
		}
		return {
			id: e.id,
			source: e.source,
			target: e.target,
			sourceHandle: handle,
			type: 'smoothstep',
			class: classes.join(' '),
			selectable: interactive,
			deletable: interactive,
			...(loopBack ? { label: 'next pass', labelStyle: 'fill: #c8f169; font-size: 10px', labelBgStyle: 'fill: #15171f' } : {}),
			markerEnd: { type: MarkerType.ArrowClosed, width: 16, height: 16, color: loopBack ? '#a3e635' : secondary ? '#b46ab8' : '#5a6380' }
		};
	});
}
