import { toast } from 'svelte-sonner';
import { goto } from '$app/navigation';
import { api, ApiError } from './api';
import { blankFlow } from './nodes';
import { toRunState } from './progress';
import { shell } from './shell.svelte';
import { splitSeconds } from './duration';
import type {
	Definition,
	Direction,
	NodeState,
	NodeType,
	PendingActivity,
	Problem,
	RunState,
	RunSummary,
	RunView,
	TriggerConfig
} from './types';

export type Mode = 'build' | 'runs';

export function errorMessage(err: unknown) {
	return err instanceof Error ? err.message : String(err);
}

/** Definitions saved before settings existed get sane defaults; original per-step fields are folded in. */
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function normalizeFlow(d: Definition): Definition {
	d.settings ??= { activity: { retry: {} } };
	d.settings.activity ??= { retry: {} };
	d.settings.activity.retry ??= {};
	d.settings.variables ??= [];
	d.settings.direction ??= 'LR';
	d.edges ??= [];
	for (const n of d.nodes ?? []) {
		const c = n.config as Record<string, any>;
		if (n.type === 'http') {
			// Fold the original per-step fields into activity options.
			c.activity ??= { retry: {} };
			c.activity.retry ??= {};
			if (c.timeoutSeconds && !c.activity.startToCloseSeconds) c.activity.startToCloseSeconds = c.timeoutSeconds;
			if (c.retry) c.activity.retry = { ...c.retry, ...stripEmpty(c.activity.retry) };
			delete c.timeoutSeconds;
			delete c.retry;
		}
		if (n.type === 'agent') {
			c.modelActivity ??= { retry: {} };
			c.toolActivity ??= { retry: {} };
		}
		if (n.type === 'delay' && !c.mode) {
			const { amount, unit } = splitSeconds(c.seconds ?? 10);
			Object.assign(c, { mode: 'duration', amount, unit });
			delete c.seconds;
		}
	}
	return d;
}

function stripEmpty(o: Record<string, unknown>) {
	return Object.fromEntries(Object.entries(o).filter(([, v]) => v !== undefined && v !== 0 && !(Array.isArray(v) && !v.length)));
}

/** The editor and the run view. One instance per tab; the API scopes everything to the session. */
class AppState {
	flow = $state<Definition | null>(null);
	webhookUrl = $state('');
	scheduleActive = $state(false);
	/** Bumped when nodes or edges are added or removed, or positions are rewritten, so the canvas rebuilds. */
	structure = $state(0);
	problems = $state<Problem[]>([]);
	saving = $state(false);
	private savedSnapshot = $state('');

	mode = $state<Mode>('build');
	addRequest = $state<{ type: NodeType; seq: number } | null>(null);
	/** Set by the Tidy up button; the build canvas consumes it with measured node sizes. */
	tidyRequest = $state(0);
	leftCollapsed = $state(false);
	rightCollapsed = $state(false);
	selectedNodeId = $state<string | null>(null);
	settingsOpen = $state(false);
	historyOpen = $state(false);

	runs = $state<RunSummary[]>([]);
	selectedRunId = $state<string | null>(null);
	runView = $state<RunView | null>(null);
	/** Full state with outputs, fetched only when asked for (it may wake a worker). */
	details = $state<RunState | null>(null);
	detailsLoading = $state(false);
	runLoadError = $state('');
	/** The early response of the run this browser just started, if its flow responds early. */
	earlyResponse = $state<{ workflowId: string; response?: unknown; error?: string } | null>(null);

	dirty = $derived(this.flow !== null && JSON.stringify(this.flow) !== this.savedSnapshot);
	direction = $derived<Direction>(this.flow?.settings?.direction ?? 'LR');
	lastState = $derived(this.runView ? toRunState(this.runView, this.details) : null);
	runActive = $derived(this.runView?.execution.status === 'Running');

	selectedNode = $derived.by(() => {
		const id = this.selectedNodeId;
		const nodes = this.mode === 'runs' || !this.flow ? this.lastState?.flow.nodes : this.flow.nodes;
		return nodes?.find((n) => n.id === id) ?? null;
	});

	trigger = $derived(this.flow?.nodes.find((n) => n.type === 'trigger') ?? null);

	private setFlow(flow: Definition) {
		this.flow = normalizeFlow(flow);
		this.savedSnapshot = JSON.stringify(this.flow);
		this.problems = [];
		this.structure++;
	}

	async openFlow(id: string) {
		if (this.flow?.id === id) return;
		try {
			const detail = await api.getFlow(id);
			this.setFlow(detail.flow);
			this.webhookUrl = detail.webhookUrl;
			this.scheduleActive = detail.scheduleActive;
			this.selectedNodeId = null;
			this.closeRun();
			this.runs = [];
		} catch (err) {
			toast.error(`Could not open flow: ${errorMessage(err)}`);
		}
	}

	closeFlow() {
		this.flow = null;
		this.savedSnapshot = '';
	}

	async newFlow(from?: Partial<Definition>) {
		try {
			const created = await api.createFlow(from ?? blankFlow());
			await shell.loadFlows();
			this.mode = 'build';
			await goto(`/workflows/${encodeURIComponent(created.id)}`);
			toast.success('New flow created. Drag steps in from the palette.');
		} catch (err) {
			toast.error(`Could not create flow: ${errorMessage(err)}`);
		}
	}

	/** Loads an older version into the editor; saving makes it the newest version. */
	restoreVersion(def: Definition) {
		if (!this.flow) return;
		const restored = normalizeFlow(structuredClone({ ...def, id: this.flow.id, version: this.flow.version }));
		this.flow = restored;
		this.selectedNodeId = null;
		this.structure++;
		toast.success(`Loaded version ${def.version}. Save to make it the current version.`);
	}

	async save(): Promise<boolean> {
		if (!this.flow) return false;
		this.saving = true;
		try {
			const saved = normalizeFlow(await api.saveFlow($state.snapshot(this.flow)));
			// Keep the canvas as drawn: the server echoes the same nodes.
			this.flow = saved;
			this.savedSnapshot = JSON.stringify(saved);
			this.problems = [];
			shell.loadFlows();
			toast.success(`Saved version ${saved.version}`);
			return true;
		} catch (err) {
			if (err instanceof ApiError && err.problems.length > 0) {
				this.problems = err.problems;
				toast.error(`Fix ${err.problems.length} problem${err.problems.length === 1 ? '' : 's'} before saving`, {
					description: err.problems
						.slice(0, 3)
						.map((p) => p.message)
						.join(' · ')
				});
			} else {
				toast.error(`Save failed: ${errorMessage(err)}`);
			}
			return false;
		} finally {
			this.saving = false;
		}
	}

	problemsFor(nodeId: string): Problem[] {
		return this.problems.filter((p) => p.nodeId === nodeId);
	}

	requestAdd(type: NodeType) {
		this.addRequest = { type, seq: (this.addRequest?.seq ?? 0) + 1 };
	}

	toggleFocus() {
		const focus = !(this.leftCollapsed && this.rightCollapsed);
		this.leftCollapsed = focus;
		this.rightCollapsed = focus;
		shell.navCollapsed = focus;
	}

	setDirection(d: Direction) {
		if (!this.flow) return;
		this.flow.settings.direction = d;
		this.structure++;
	}

	sampleInput(): string {
		const t = this.trigger?.config as TriggerConfig | undefined;
		return t?.sampleInput?.trim() || '{}';
	}

	async startRun(input?: unknown) {
		if (!this.flow) return;
		if (this.dirty && !(await this.save())) return;
		try {
			const res = await api.startRun(this.flow.id, input);
			const workflowId = res.workflowId;
			if (res.responded) {
				this.earlyResponse = { workflowId, response: res.response };
				toast.success('The run responded and carries on', { description: JSON.stringify(res.response) });
			} else if (res.responseError) {
				this.earlyResponse = { workflowId, error: res.responseError };
				toast.warning('Run started, but it did not respond', { description: res.responseError });
			} else {
				this.earlyResponse = null;
				toast.success('Run started', { description: workflowId });
			}
			this.mode = 'runs';
			await this.loadRuns(false);
			await this.openRun(workflowId);
		} catch (err) {
			toast.error(`Could not start run: ${errorMessage(err)}`);
		}
	}

	async setSchedule(enabled: boolean) {
		if (!this.flow) return;
		if (this.dirty && !(await this.save())) return;
		try {
			const res = await api.setSchedule(this.flow.id, enabled);
			this.scheduleActive = res.scheduleActive;
			shell.loadFlows();
			toast.success(res.scheduleActive ? 'Temporal Schedule is running this flow' : 'Schedule removed');
		} catch (err) {
			toast.error(`Could not update schedule: ${errorMessage(err)}`);
		}
	}

	async loadRuns(selectLatest: boolean) {
		if (!this.flow) return;
		try {
			this.runs = (await api.listRuns(this.flow.id)) ?? [];
			if (selectLatest && !this.selectedRunId && this.runs.length > 0) await this.openRun(this.runs[0].workflowId);
		} catch {
			/* background refresh; keep the last list */
		}
	}

	closeRun() {
		this.selectedRunId = null;
		this.runView = null;
		this.details = null;
	}

	async openRun(workflowId: string) {
		if (this.selectedRunId !== workflowId) {
			this.selectedRunId = workflowId;
			this.runView = null;
			this.details = null;
			this.selectedNodeId = null;
		}
		await this.refreshRun();
	}

	async refreshRun() {
		const wid = this.selectedRunId;
		if (!wid) return;
		try {
			const view = await api.getRun(wid);
			if (wid !== this.selectedRunId) return;
			if (view.flow) normalizeFlow(view.flow);
			this.runView = view;
			this.runLoadError = '';
		} catch (err) {
			this.runLoadError = errorMessage(err);
		}
	}

	/** Reads live state with outputs. On serverless this can wake a worker, so it is opt-in. */
	async loadDetails() {
		const wid = this.selectedRunId;
		if (!wid) return;
		this.detailsLoading = true;
		try {
			const d = await api.runDetails(wid);
			if (wid === this.selectedRunId) this.details = d;
		} catch (err) {
			toast.error(`Could not load outputs: ${errorMessage(err)}`);
		} finally {
			this.detailsLoading = false;
		}
	}

	nodeRunState(nodeId: string): NodeState | undefined {
		return this.lastState?.nodes[nodeId];
	}

	pendingActivity(nodeId: string): PendingActivity | undefined {
		return this.runView?.pendingActivities?.find((a) => a.nodeId === nodeId);
	}

	async cancelRun() {
		if (!this.selectedRunId) return;
		try {
			await api.cancel(this.selectedRunId);
			toast.success('Cancellation requested');
			await this.refreshRun();
		} catch (err) {
			toast.error(`Cancel failed: ${errorMessage(err)}`);
		}
	}

	async setMode(mode: Mode) {
		if (this.mode === mode) return;
		this.mode = mode;
		this.selectedNodeId = null;
		if (mode === 'runs') await this.loadRuns(true);
	}
}

export const app = new AppState();
