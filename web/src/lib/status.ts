import type { NodeStatus, Waiting, WorkerStatus } from './types';

export interface Tone {
	label: string;
	text: string;
	border: string;
	glow: string;
	pulse: boolean;
}

/** Colour semantics: green done, yellow running, purple waiting, pink retrying, slate idle, red only for failures. */
export function nodeTone(status: NodeStatus | undefined, retrying = false): Tone {
	if (retrying) return { label: 'retrying', text: 'var(--pink)', border: 'var(--pink)', glow: 'rgba(255,107,255,.45)', pulse: true };
	switch (status) {
		case 'running':
			return { label: 'running', text: 'var(--yellow)', border: 'var(--yellow)', glow: 'rgba(254,203,47,.45)', pulse: true };
		case 'waiting':
			return { label: 'waiting', text: 'var(--purple)', border: 'var(--purple)', glow: 'rgba(182,100,255,.5)', pulse: true };
		case 'completed':
			return { label: 'done', text: 'var(--green)', border: 'var(--green)', glow: '', pulse: false };
		case 'failed':
			return { label: 'failed', text: 'var(--red)', border: 'var(--red)', glow: '', pulse: false };
		case 'skipped':
			return { label: 'skipped', text: 'var(--slate-300)', border: 'var(--slate-500)', glow: '', pulse: false };
		case 'pending':
			return { label: 'pending', text: 'var(--slate-300)', border: 'var(--line)', glow: '', pulse: false };
		default:
			return { label: '', text: 'var(--slate-300)', border: '#2b3348', glow: '', pulse: false };
	}
}

export function executionTone(status: string | undefined): { text: string; label: string } {
	switch (status) {
		case 'Running':
			return { text: 'var(--yellow)', label: 'Running' };
		case 'Completed':
			return { text: 'var(--green)', label: 'Completed' };
		case 'Failed':
		case 'TimedOut':
			return { text: 'var(--red)', label: status === 'TimedOut' ? 'Timed out' : 'Failed' };
		case 'Canceled':
		case 'Terminated':
			return { text: 'var(--slate-300)', label: status };
		default:
			return { text: 'var(--slate-300)', label: status ?? '—' };
	}
}

/** `condition` is the node's configured expression, used when the Workflow does not echo it. */
/** `condition` is the node's configured expression, used when the Workflow does not echo it. */
export function waitingText(w: Waiting, condition?: string): string {
	switch (w.kind) {
		case 'signal':
			return `Waiting for signal "${w.signalName}"${w.until ? ` until ${clock(w.until)}` : ''}`;
		case 'approval':
			return `Waiting for a person to approve${w.until ? ` (times out ${clock(w.until)})` : ''}`;
		case 'timer':
			return `Durable timer — resumes at ${w.until ? clock(w.until) : '…'}`;
		case 'condition':
			return `Waiting until ${w.title || condition || 'the condition is true'}${w.until ? ` (times out ${clock(w.until)})` : ''}`;
		case 'children':
			return w.title || 'Waiting for child workflows';
		case 'collect':
			return `Collecting "${w.signalName}" events — ${w.details ?? '0 collected'}${w.until ? `, closes ${clock(w.until)} if quiet` : ''}`;
		case 'fix':
			return `Retries ran out — waiting for a person to fix and retry, or give up`;
		default:
			return w.title ?? `Waiting (${w.kind})`;
	}
}

export interface WorkerPill {
	text: string;
	color: string;
	/** True when nothing can make progress (no long-lived worker, or no API). */
	unavailable: boolean;
}

/** Serverless with no worker running is normal (scaled to zero), not an error. */
export function workerPill(w: WorkerStatus | null, unknown: boolean): WorkerPill {
	if (unknown || !w) return { text: 'API unreachable', color: 'var(--slate-300)', unavailable: true };
	if (w.mode === 'serverless') {
		return w.available
			? { text: `Serverless · ${w.workers} worker${w.workers === 1 ? '' : 's'} running`, color: 'var(--green)', unavailable: false }
			: { text: 'Serverless · scaled to zero', color: 'var(--indigo-200)', unavailable: false };
	}
	return w.available
		? { text: 'Worker online', color: 'var(--green)', unavailable: false }
		: { text: 'No worker — runs are paused, progress is safe', color: 'var(--pink)', unavailable: true };
}

export function clock(iso?: string | null): string {
	if (!iso) return '—';
	return new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

export function duration(from?: string | null, to?: string | null): string {
	if (!from) return '—';
	const ms = (to ? new Date(to).getTime() : Date.now()) - new Date(from).getTime();
	if (ms < 1000) return `${Math.max(ms, 0)}ms`;
	const s = Math.round(ms / 1000);
	if (s < 60) return `${s}s`;
	const m = Math.floor(s / 60);
	if (m < 60) return `${m}m ${s % 60}s`;
	return `${Math.floor(m / 60)}h ${m % 60}m`;
}

export function ago(iso?: string | null): string {
	if (!iso) return '';
	const s = Math.round((Date.now() - new Date(iso).getTime()) / 1000);
	if (s < 60) return `${Math.max(s, 0)}s ago`;
	if (s < 3600) return `${Math.floor(s / 60)}m ago`;
	if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
	return new Date(iso).toLocaleDateString();
}

export function pretty(v: unknown): string {
	if (v === undefined) return '';
	if (typeof v === 'string') {
		try {
			return JSON.stringify(JSON.parse(v), null, 2);
		} catch {
			return v;
		}
	}
	return JSON.stringify(v, null, 2);
}
