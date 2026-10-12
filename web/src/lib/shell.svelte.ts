import { api } from './api';
import type { FlowSummary, SessionInfo, WorkerStatus } from './types';
import { workerPill } from './status';

/** App-wide state: who is signed in, the worker, and the flow list used by pickers and names. */
class Shell {
	session = $state<SessionInfo | null>(null);
	worker = $state<WorkerStatus | null>(null);
	workerUnknown = $state(false);
	flows = $state<FlowSummary[]>([]);
	flowsLoaded = $state(false);
	navCollapsed = $state(false);

	pill = $derived(workerPill(this.worker, this.workerUnknown));
	serverless = $derived((this.worker?.mode ?? this.session?.workerMode) === 'serverless');

	private started = false;

	start() {
		if (this.started) return;
		this.started = true;
		api.session().then((s) => (this.session = s)).catch(() => (this.workerUnknown = true));
		this.pollWorker();
		this.loadFlows();
		setInterval(() => this.pollWorker(), 3000);
	}

	async pollWorker() {
		try {
			this.worker = await api.worker();
			this.workerUnknown = false;
		} catch {
			this.workerUnknown = true;
		}
	}

	async loadFlows() {
		try {
			this.flows = (await api.listFlows()) ?? [];
		} catch {
			/* pages show their own errors */
		} finally {
			this.flowsLoaded = true;
		}
	}

	flowName(id?: string) {
		return this.flows.find((f) => f.id === id)?.name ?? id ?? '';
	}

	temporalWorkflowsUrl() {
		const s = this.session;
		return s ? `${s.temporalUiUrl}/namespaces/${encodeURIComponent(s.namespace)}/workflows` : '';
	}
}

export const shell = new Shell();
