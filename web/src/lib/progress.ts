// Turns a RunView (execution status + flow snapshot + compact progress memo)
// into the RunState shape the run view draws. The memo is all the API returns
// without waking a worker; full outputs come from /details on demand and are
// merged on top. Keep every assumption about the memo's shape in this file.

import type { EventState, NodeProgress, NodeState, Progress, RunState, RunStatus, RunView } from './types';

function expand(p: NodeProgress): NodeState {
	return {
		status: p.s,
		handle: p.h,
		error: p.e,
		waiting: p.w,
		startedAt: p.t0,
		endedAt: p.t1,
		invocations: p.i,
		undo: p.u,
		progress: p.p,
		childWorkflowId: p.c
	};
}

function runStatus(execution: string, memo?: RunStatus): RunStatus {
	if (execution === 'Running') return 'running';
	if (execution === 'Completed') return 'completed';
	return memo && memo !== 'running' ? memo : 'failed';
}

export function toRunState(view: RunView, details?: RunState | null): RunState {
	const p = view.progress;
	const nodes: Record<string, NodeState> = {};
	for (const [id, np] of Object.entries(p?.nodes ?? {})) nodes[id] = expand(np);
	// Details carry outputs; the memo is newer for status while the run moves,
	// so keep memo fields and only borrow what the memo leaves out.
	for (const [id, ns] of Object.entries(details?.nodes ?? {})) {
		nodes[id] = nodes[id] ? { ...ns, ...stripUndefined(nodes[id]), output: ns.output } : ns;
	}
	return {
		flowId: view.flow?.id ?? details?.flowId ?? '',
		flowName: view.flow?.name ?? details?.flowName ?? '',
		flowVersion: view.flow?.version ?? details?.flowVersion ?? 0,
		source: view.source ?? p?.source ?? details?.source ?? '',
		status: runStatus(view.execution.status, p?.status ?? details?.status),
		error: p?.error ?? details?.error,
		startedAt: p?.startedAt ?? view.execution.startTime,
		endedAt: p?.endedAt ?? view.execution.closeTime ?? undefined,
		nodes,
		callbackUrl: view.callbackUrl,
		events: mergeEvents(p?.events, details?.events),
		vars: p?.vars ?? details?.vars,
		compensation: p?.compensation ?? details?.compensation,
		sagas: p?.sagas ?? details?.sagas,
		key: view.key ?? p?.key ?? details?.key,
		generation: p?.generation ?? details?.generation,
		flow: view.flow ?? details?.flow,
		items: details?.items
	};
}

/**
 * The memo keeps each Signal name's count and time, and its latest payload when
 * small; /details fills in anything left out. Older runs kept only a count.
 */
function mergeEvents(memo: Progress['events'] | Record<string, number> | undefined, full?: Record<string, EventState>): Record<string, EventState> {
	const out: Record<string, EventState> = { ...(full ?? {}) };
	for (const [name, v] of Object.entries(memo ?? {})) {
		const count = typeof v === 'number' ? v : v.count;
		const prev = out[name];
		out[name] = typeof v === 'number' ? { ...prev, pending: prev?.pending ?? 0, count: Math.max(count, prev?.count ?? 0) } : { ...prev, ...v };
	}
	return out;
}

function stripUndefined<T extends object>(o: T): Partial<T> {
	return Object.fromEntries(Object.entries(o).filter(([, v]) => v !== undefined)) as Partial<T>;
}

/** The run's event URL for a Signal name: what an outside system POSTs to. */
export const eventUrl = (callbackUrl: string, name: string) => `${callbackUrl}/events/${name}`;
export const updateUrl = (callbackUrl: string, name: string) => `${callbackUrl}/updates/${name}`;

/** Waiting kinds that mean a person or outside system has to act. */
export const PERSON_WAITS = ['approval', 'condition', 'signal', 'collect', 'fix'];
