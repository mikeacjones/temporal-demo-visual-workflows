<script lang="ts">
	import Plus from '@lucide/svelte/icons/plus';
	import X from '@lucide/svelte/icons/x';
	import Copy from '@lucide/svelte/icons/copy';
	import KeyRound from '@lucide/svelte/icons/key-round';
	import Undo2 from '@lucide/svelte/icons/undo-2';
	import { toast } from 'svelte-sonner';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Switch } from '$lib/components/ui/switch';
	import { Button } from '$lib/components/ui/button';
	import * as ToggleGroup from '$lib/components/ui/toggle-group';
	import Field from './Field.svelte';
	import Choice from './Choice.svelte';
	import DurationInput from './DurationInput.svelte';
	import ActivitySettings from './ActivitySettings.svelte';
	import HttpRequestFields from './HttpRequestFields.svelte';
	import ConnectorForm from './ConnectorForm.svelte';
	import TransformForm from './TransformForm.svelte';
	import ScheduleForm from './ScheduleForm.svelte';
	import { app } from '$lib/state.svelte';
	import { shell } from '$lib/shell.svelte';
	import { NODE_SPECS, uniqueKey } from '$lib/nodes';
	import { flowDefaults } from '$lib/activity';
	import { UNITS } from '$lib/duration';
	import type {
		AgentConfig,
		ApprovalConfig,
		ConditionConfig,
		DelayConfig,
		FlowNode,
		FirstOfConfig,
		ForEachConfig,
		HTTPConfig,
		LoopConfig,
		OnSignalConfig,
		OnUpdateConfig,
		RespondConfig,
		RunFlowConfig,
		SetVariableConfig,
		TransformConfig,
		TriggerConfig,
		WaitConditionConfig,
		WaitSignalConfig
	} from '$lib/types';
	import { isConnector } from '$lib/types';

	let { node }: { node: FlowNode } = $props();

	const spec = $derived(NODE_SPECS[node.type]);
	const problems = $derived(app.problemsFor(node.id));
	const keyOk = $derived(/^[a-z][a-z0-9_]*$/.test(node.key));
	const inherited = $derived(flowDefaults(app.flow?.settings.activity));
	const variables = $derived(app.flow?.settings.variables ?? []);
	const sharedLimits = $derived(shell.session?.rateLimits ?? []);
	const otherFlows = $derived(shell.flows.filter((f) => f.id !== app.flow?.id).map((f) => ({ value: f.id, label: `${f.name} (v${f.version})` })));

	const kinds = [
		{ value: 'manual', label: 'Manual — Start Workflow from the UI' },
		{ value: 'webhook', label: 'Webhook — POST starts a Workflow' },
		{ value: 'schedule', label: 'Schedule — Temporal Schedule' }
	];
	const raceRules = [
		{ value: 'arrives', label: 'First branch to arrive' },
		{ value: 'succeeds', label: 'First branch to succeed' }
	];
	const finishRules = [
		{ value: 'all', label: 'Every item succeeds' },
		{ value: 'first', label: 'The first item succeeds' },
		{ value: 'count', label: 'A number of items succeed' }
	];
	const eachModes = [
		{ value: 'flow', label: 'A flow, as a child Workflow' },
		{ value: 'steps', label: 'The steps on its body outcome' }
	];
	const ratePers = [
		{ value: 'second', label: 'per second' },
		{ value: 'minute', label: 'per minute' }
	];

	const waitRules = [
		{ value: 'result', label: 'Wait for its result' },
		{ value: 'start', label: "Don't wait: fire and forget" }
	];

	const closePolicies = [
		{ value: 'terminate', label: 'Terminate the child' },
		{ value: 'request_cancel', label: 'Ask the child to cancel' },
		{ value: 'abandon', label: 'Let the child keep running' }
	];

	// Typed views of the same proxied config object.
	const trigger = $derived(node.config as TriggerConfig);
	const onSignal = $derived(node.config as OnSignalConfig);
	const onUpdate = $derived(node.config as OnUpdateConfig);
	const http = $derived(node.config as HTTPConfig);
	const waitCond = $derived(node.config as WaitConditionConfig);
	const sig = $derived(node.config as WaitSignalConfig);
	const appr = $derived(node.config as ApprovalConfig);
	const delay = $derived(node.config as DelayConfig);
	const cond = $derived(node.config as ConditionConfig);
	const xform = $derived(node.config as TransformConfig);
	const setVar = $derived(node.config as SetVariableConfig);
	const respond = $derived(node.config as RespondConfig);
	const runFlow = $derived(node.config as RunFlowConfig);
	const forEach = $derived(node.config as ForEachConfig);
	const firstOf = $derived(node.config as FirstOfConfig);
	const loop = $derived(node.config as LoopConfig);
	// The main path's Respond answers whoever started the run (early return).
	const onMainPath = $derived.by(() => {
		const f = app.flow;
		if (!f) return false;
		const trigger = f.nodes.find((n) => n.type === 'trigger');
		const seen = new Set<string>(trigger ? [trigger.id] : []);
		const stack = trigger ? [trigger.id] : [];
		while (stack.length) {
			const id = stack.pop()!;
			for (const e of f.edges) if (e.source === id && !seen.has(e.target)) (seen.add(e.target), stack.push(e.target));
		}
		return seen.has(node.id);
	});
	const agent = $derived(node.config as AgentConfig);

	// A keyed flow's run can be reached by key, starting it if needed: one URL
	// per message it handles.
	const withStartUrls = $derived.by(() => {
		const f = app.flow;
		if (!f?.settings.keyedBy || !app.webhookUrl) return [];
		const urls: { kind: string; name: string; url: string }[] = [];
		for (const n of f.nodes) {
			if (n.type === 'onSignal') {
				const name = (n.config as OnSignalConfig).signalName;
				urls.push({ kind: 'Signal-With-Start', name, url: `${app.webhookUrl}/keys/<key>/events/${name}` });
			} else if (n.type === 'onUpdate') {
				const name = (n.config as OnUpdateConfig).updateName;
				urls.push({ kind: 'Update-With-Start', name, url: `${app.webhookUrl}/keys/<key>/updates/${name}` });
			}
		}
		return urls;
	});

	let httpFields = $state<ReturnType<typeof HttpRequestFields>>();

	function copy(text: string) {
		navigator.clipboard?.writeText(text);
		toast.success('Copied');
	}

	const jsonError = (text: string | undefined) => {
		if (!text?.trim()) return '';
		try {
			JSON.parse(text);
			return '';
		} catch (e) {
			return (e as Error).message;
		}
	};

	const delayExamples = ['vars.hold_minutes * 60', 'duration("2h")', 'date(input.deliverBy) - duration("1h")'];
</script>

{#snippet jsonField(label: string, hint: string, get: () => string | undefined, set: (v: string) => void, rows = 'min-h-28')}
	<Field {label} {hint}>
		<Textarea class="mono {rows} text-xs" bind:value={() => get() ?? '', set} spellcheck={false} aria-invalid={!!jsonError(get())} />
		{#if jsonError(get())}<p class="text-[0.68rem] text-[var(--red)]">{jsonError(get())}</p>{/if}
	</Field>
{/snippet}

{#snippet note(text: string)}
	<p class="text-[0.68rem] leading-snug text-[var(--slate-300)]">{text}</p>
{/snippet}

<div class="space-y-4">
	<div>
		<div class="eyebrow" style:color={spec.accent}>{spec.label}</div>
		<div class="mono mt-0.5 text-[0.66rem] text-[var(--indigo-200)]">Runs as: {spec.primitive}</div>
	</div>

	{#if problems.length}
		<div class="space-y-1 border border-[var(--red)]/60 bg-[var(--red)]/10 p-2 text-xs text-[var(--red)]">
			{#each problems as p, i (i)}<div>{p.message}</div>{/each}
		</div>
	{/if}

	<Field label="Label"><Input bind:value={node.label} /></Field>
	<Field label="Output key" hint={keyOk ? `Later steps read this output as {{ steps.${node.key} }}` : 'Use lower_snake_case'}>
		<div class="flex gap-1.5">
			<Input class="mono" bind:value={node.key} aria-invalid={!keyOk} />
			<Button variant="outline" size="sm" class="mono text-[0.65rem]" onclick={() => app.flow && (node.key = uniqueKey(node.label, app.flow.nodes, node.id))}>from label</Button>
		</div>
	</Field>

	<div class="h-px bg-[var(--line)]"></div>

	{#if node.type === 'trigger'}
		<Field label="Starts when"><Choice bind:value={trigger.kind} options={kinds} /></Field>
		{#if trigger.kind === 'schedule'}
			<ScheduleForm bind:trigger={() => trigger, () => {}} />
			<div class="flex items-center justify-between border border-[var(--line)] p-2.5">
				<div>
					<div class="text-sm">Schedule active</div>
					<div class="text-[0.68rem] text-[var(--slate-300)]">Runs the saved version; saving updates it</div>
				</div>
				<Switch checked={app.scheduleActive} onCheckedChange={(v) => app.setSchedule(v)} />
			</div>
		{/if}
		{#if trigger.kind === 'webhook'}
			<Field label="Webhook URL" hint={'POST JSON here to start a run; the body becomes {{ input }}.'}>
				{#if app.webhookUrl}
					<div class="flex gap-1.5">
						<Input class="mono text-[0.7rem]" readonly value={app.webhookUrl} />
						<Button variant="outline" size="icon-sm" onclick={() => copy(app.webhookUrl)}><Copy /></Button>
					</div>
				{:else}
					<p class="text-xs text-[var(--slate-300)]">Save the flow to get its webhook URL.</p>
				{/if}
			</Field>
		{/if}
		{#if trigger.kind !== 'schedule'}
			<div class="space-y-2 border border-[var(--line)] p-2.5">
				<div class="flex items-center justify-between">
					<span class="text-sm">Delay the start</span>
					<Switch checked={!!trigger.startDelay} onCheckedChange={(v) => (trigger.startDelay = v ? { mode: 'duration', amount: 30, unit: 'seconds' } : undefined)} />
				</div>
				{#if trigger.startDelay}
					<div class="grid grid-cols-[1fr_110px] gap-1.5">
						<Input type="number" min="1" bind:value={trigger.startDelay.amount} />
						<Choice bind:value={trigger.startDelay.unit} options={UNITS.map((u) => ({ value: u.value, label: u.label }))} />
					</div>
				{/if}
				{@render note('Delayed start: the Workflow is created at once, so its ID and callback URL exist and Signals can reach it, but it does no work until the delay passes. Temporal holds it; no timer in your code.')}
			</div>
		{/if}
		{@render jsonField('Sample input (JSON)', 'Prefills the Run dialog. Steps read it as {{ input.field }}.', () => trigger.sampleInput, (v) => (trigger.sampleInput = v))}
		{#if withStartUrls.length}
			<div class="space-y-2 border border-[var(--line)] p-2.5">
				<div class="flex items-center gap-1.5 text-sm"><KeyRound class="size-3.5 text-[var(--cyan)]" /> Send to a key's run, starting it if needed</div>
				{#each withStartUrls as u (u.url)}
					<div class="space-y-1">
						<div class="mono text-[0.62rem] text-[var(--indigo-200)]">{u.kind} · {u.name}</div>
						<div class="flex gap-1.5">
							<Input class="mono text-[0.66rem]" readonly value={u.url} />
							<Button variant="outline" size="icon-sm" onclick={() => copy(u.url)}><Copy /></Button>
						</div>
					</div>
				{/each}
				{@render note('POST JSON; it is both the message and, if the run starts, its input. Signal-With-Start is atomic and returns at once. Update-With-Start waits for the lane\'s Respond value; it isn\'t atomic, so if no worker answers in time the run may have started without the update. The flow\'s Workflow ID policies decide what happens when the key\'s run exists.')}
			</div>
		{/if}
	{:else if node.type === 'onStart'}
		{@render note('The init lane. It runs once, as the run starts, before the main path and before any On signal or On update lane handles a message. Messages that arrive meanwhile, including the one sent with Signal- or Update-With-Start, wait for it, so their lanes see what it set up. It reads the start input as {{ input }}. If it fails, the run fails and waiting messages are dropped.')}
		{@render note('A run that continues as new has already been set up, so its next run doesn\'t repeat it.')}
	{:else if node.type === 'onSignal'}
		<Field label="Signal name" hint="This lane runs once per event, in arrival order, sharing the run's variables.">
			<Input class="mono" bind:value={onSignal.signalName} />
		</Field>
		{@render note(`Steps in this lane read the event's payload as steps.${node.key} (and the latest as events.${onSignal.signalName || '<name>'}). Outside systems POST to the run's callback URL + /events/${onSignal.signalName || '<name>'}.`)}
	{:else if node.type === 'onUpdate'}
		<Field label="Update name"><Input class="mono" bind:value={onUpdate.updateName} /></Field>
		<Field label="Validator (optional)" hint={'Must be true to accept the Update. Sees request, vars and input, e.g. request.amount <= vars.limit'}>
			<Textarea class="mono min-h-16 text-xs" bind:value={onUpdate.validator} spellcheck={false} placeholder="accept everything" />
		</Field>
		{@render note(`Steps in this lane read the request as steps.${node.key}. A rejected Update leaves no trace in history; an accepted one runs this lane and returns its Respond step\'s value to the caller.`)}
	{:else if node.type === 'http'}
		<HttpRequestFields bind:this={httpFields} bind:req={() => http, () => {}} />
		<Button
			variant="outline"
			size="xs"
			class="mono"
			onclick={() => httpFields?.setHeader('Idempotency-Key', `{{ run.id }}-${node.key}`)}><KeyRound /> Add idempotency key</Button
		>
		<ActivitySettings bind:options={http.activity} {inherited} />
		<div class="space-y-1.5 border border-[var(--line)] p-2.5">
			<div class="flex items-center justify-between">
				<span class="text-sm">Pause for a fix when retries run out</span>
				<Switch bind:checked={() => !!http.pauseForFix, (v) => (http.pauseForFix = v || undefined)} />
			</div>
			{@render note('A resumable Activity: instead of failing, the step waits (for free) for a person to correct the URL or body and retry, or let it fail. The run view shows the request to edit.')}
		</div>
		{#if sharedLimits.length || http.rateLimit}
			<Field label="Shared rate limit" hint="A limit every run shares, for a downstream service's quota. Requests wait their turn on the limit's task queue, which Temporal dispatches no faster than the rate.">
				<Choice
					bind:value={() => http.rateLimit ?? '', (v) => (http.rateLimit = v || undefined)}
					options={[{ value: '', label: 'None' }, ...sharedLimits.map((l) => ({ value: l.name, label: `${l.name} — ${l.count} per ${l.per}` }))]}
				/>
			</Field>
		{/if}
		<div class="space-y-2 border border-[var(--line)] p-2.5">
			<div class="flex items-center justify-between">
				<span class="eyebrow flex items-center gap-1.5 !text-[0.62rem]"><Undo2 class="size-3" /> Undo</span>
				<Switch checked={!!http.undo} onCheckedChange={(v) => (http.undo = v ? { method: 'POST', url: 'https://', headers: {}, body: '' } : undefined)} />
			</div>
			{@render note('A compensating request. If the run fails later, completed undo requests run in reverse order (a saga). In a For each body every item\'s undo joins the run\'s; to put one item right when it fails, draw the compensation on its error outcome.')}
			{#if http.undo}
				<HttpRequestFields bind:req={() => http.undo!, () => {}} />
			{/if}
		</div>
		<p class="text-[0.68rem] leading-snug text-[var(--slate-300)]">
			To let the called system report back later, send it
			<span class="mono text-[var(--green)]">{'{{ run.callbackUrl }}/events/<signal>'}</span>; a POST there signals this run.
		</p>
	{:else if isConnector(node.type)}
		<ConnectorForm {node} {inherited} {sharedLimits} />
	{:else if node.type === 'transform'}
		<TransformForm {node} />
	{:else if node.type === 'setVariable'}
		<Field label="Assignments" hint={'Values are templates; "{{ vars.count + 1 }}" keeps its type.'}>
			<div class="space-y-1.5">
				{#each setVar.assignments as a, i (i)}
					<div class="grid grid-cols-[120px_1fr_auto] gap-1.5">
						<Choice bind:value={a.name} options={variables.map((v) => ({ value: v.name, label: v.name }))} placeholder="variable" />
						<Input class="mono text-xs" bind:value={a.value} placeholder={'{{ … }}'} />
						<Button variant="ghost" size="icon-sm" onclick={() => setVar.assignments.splice(i, 1)}><X /></Button>
					</div>
				{/each}
				<Button variant="outline" size="xs" class="mono" onclick={() => setVar.assignments.push({ name: '', value: '' })}><Plus /> assignment</Button>
			</div>
		</Field>
		{#if !variables.length}
			<p class="text-[0.68rem] text-[var(--yellow)]">
				No variables yet. <button class="underline" onclick={() => (app.settingsOpen = true)}>Declare them in flow settings</button>.
			</p>
		{/if}
		{@render note('Changing a variable re-checks waiting conditions and recalculates expression timers.')}
	{:else if node.type === 'respond'}
		{@render jsonField('Response (JSON template)', onMainPath ? 'Returned to whoever started the run.' : 'Returned to whoever sent the Update.', () => respond.value, (v) => (respond.value = v))}
		{#if onMainPath}
			{@render note('Early return: the run is started with Update-With-Start, and the caller (the Run button, or the webhook\'s HTTP response) gets this value as soon as the run reaches this step. The run carries on afterwards.')}
		{/if}
	{:else if node.type === 'waitForCondition'}
		<Field label="Continue when" hint={'Expression over events, vars and steps, e.g. events.order_status.status == "shipped" or vars.approved'}>
			<Textarea class="mono min-h-16 text-xs" bind:value={waitCond.condition} spellcheck={false} />
		</Field>
		<Field label="Event it is about (optional)" hint="Shows the latest event and the callback URL in the run view.">
			<Input class="mono" bind:value={() => waitCond.signalName ?? '', (v) => (waitCond.signalName = v || undefined)} placeholder="signal name" />
		</Field>
		<Field label="Timeout" hint="A durable timer; a timeout routes to the timeout outcome."><DurationInput bind:value={waitCond.timeoutSeconds} placeholder="wait forever" /></Field>
		{@render note('Re-checked each time an event arrives or a variable changes. No polling — the run is idle until then or the timeout.')}
	{:else if node.type === 'waitForSignal'}
		<Field label="Signal name" hint="Send it from the run view, the callback URL, or the Temporal UI."><Input class="mono" bind:value={sig.signalName} /></Field>
		<Field label="Only events where" hint="Optional; required in a For each body. An expression over event (and item) picking this step's events, e.g. event.orderId == item.id. Other events stay queued for others.">
			<Input class="mono text-xs" bind:value={() => sig.match ?? '', (v) => (sig.match = v || undefined)} placeholder="any event" />
		</Field>
		<Field label="Timeout" hint={sig.collect ? 'For the first event. A timeout routes to the timeout outcome.' : 'A durable timer; a timeout routes to the timeout outcome.'}>
			<DurationInput bind:value={sig.timeoutSeconds} placeholder="wait forever" />
		</Field>
		<div class="space-y-2 border border-[var(--line)] p-2.5">
			<div class="flex items-center justify-between">
				<span class="text-sm">Collect a batch</span>
				<Switch checked={!!sig.collect} onCheckedChange={(v) => (sig.collect = v ? { quietSeconds: 10 } : undefined)} />
			</div>
			{#if sig.collect}
				<Field label="Until quiet for" hint="The batch closes when no event has arrived for this long.">
					<DurationInput bind:value={sig.collect.quietSeconds} min={1} />
				</Field>
				<div class="grid grid-cols-2 gap-2">
					<Field label="At most events"><Input type="number" min="0" bind:value={() => sig.collect!.maxEvents ?? '', (v) => (sig.collect!.maxEvents = Number(v) || undefined)} placeholder="no limit" /></Field>
					<Field label="At most wait"><DurationInput bind:value={sig.collect.maxWaitSeconds} placeholder="no limit" /></Field>
				</div>
				<Field label="Drop duplicates by (optional)" hint="An expression over event, e.g. event.orderId">
					<Input class="mono text-xs" bind:value={() => sig.collect!.dedupeBy ?? '', (v) => (sig.collect!.dedupeBy = v || undefined)} placeholder="keep every event" />
				</Field>
			{/if}
			{@render note('Event accumulator: gather many senders\' events into one batch, deduplicated, then process them together. The output is { events, count, duplicates }.')}
		</div>
	{:else if node.type === 'approval'}
		<Field label="Question"><Input bind:value={appr.title} /></Field>
		<Field label="Details" hint={'Templates allowed: {{ input.amount }}'}><Textarea class="min-h-20 text-sm" bind:value={appr.details} /></Field>
		<Field label="Timeout" hint="No decision in time routes to timeout, not to rejected."><DurationInput bind:value={appr.timeoutSeconds} placeholder="wait forever" /></Field>
	{:else if node.type === 'delay'}
		<ToggleGroup.Root type="single" value={delay.mode ?? 'duration'} onValueChange={(v) => v && (delay.mode = v as DelayConfig['mode'])} variant="outline" size="sm" class="w-full">
			<ToggleGroup.Item value="duration" class="mono flex-1 text-xs">Duration</ToggleGroup.Item>
			<ToggleGroup.Item value="expression" class="mono flex-1 text-xs">Expression</ToggleGroup.Item>
		</ToggleGroup.Root>
		{#if delay.mode === 'expression'}
			<Field label="Wait for / until" hint="Seconds, a duration, or a time to wait until.">
				<Textarea class="mono min-h-14 text-xs" bind:value={delay.expression} spellcheck={false} />
			</Field>
			<div class="flex flex-wrap gap-1">
				{#each delayExamples as ex (ex)}
					<button class="mono border border-dashed border-[var(--line)] px-1.5 py-0.5 text-[0.62rem] text-[var(--slate-300)] hover:border-[var(--uv-light)]" onclick={() => (delay.expression = ex)}>{ex}</button>
				{/each}
			</div>
			{@render note('Recalculates when variables it uses change, so an event lane can move the deadline.')}
		{:else}
			<Field label="Wait">
				<div class="grid grid-cols-[1fr_110px] gap-1.5">
					<Input type="number" min="1" bind:value={delay.amount} />
					<Choice bind:value={delay.unit} options={UNITS.map((u) => ({ value: u.value, label: u.label }))} />
				</div>
			</Field>
			{@render note('A durable timer: it survives worker restarts and costs nothing while it waits.')}
		{/if}
	{:else if node.type === 'loop'}
		<Field label="Repeat until" hint={`Checked after each pass, over steps, vars and events. The body always runs once. The pass number is steps.${node.key}.pass.`}>
			<Textarea class="mono min-h-14 text-xs" bind:value={loop.until} spellcheck={false} />
		</Field>
		<Field label="At most passes" hint="Then the limit outcome, or the step fails if nothing is connected there. Empty: no limit.">
			<Input type="number" min="1" bind:value={() => loop.maxPasses || '', (v) => (loop.maxPasses = Number(v) || 0)} placeholder="no limit" />
		</Field>
		<div class="space-y-2 border border-[var(--line)] p-2.5">
			<div class="flex items-center justify-between">
				<span class="text-sm">Pause between passes</span>
				<Switch checked={!!loop.pause} onCheckedChange={(v) => (loop.pause = v ? { mode: 'duration', amount: 5, unit: 'seconds' } : undefined)} />
			</div>
			{#if loop.pause}
				<ToggleGroup.Root type="single" value={loop.pause.mode ?? 'duration'} onValueChange={(v) => v && loop.pause && (loop.pause.mode = v as DelayConfig['mode'])} variant="outline" size="sm" class="w-full">
					<ToggleGroup.Item value="duration" class="mono flex-1 text-xs">Duration</ToggleGroup.Item>
					<ToggleGroup.Item value="expression" class="mono flex-1 text-xs">Expression</ToggleGroup.Item>
				</ToggleGroup.Root>
				{#if loop.pause.mode === 'expression'}
					<Input class="mono text-xs" bind:value={loop.pause.expression} placeholder={`steps.${node.key}.pass * 5`} />
				{:else}
					<div class="grid grid-cols-[1fr_110px] gap-1.5">
						<Input type="number" min="1" bind:value={loop.pause.amount} />
						<Choice bind:value={loop.pause.unit} options={UNITS.map((u) => ({ value: u.value, label: u.label }))} />
					</div>
				{/if}
			{/if}
		</div>
		{@render note('Connect the body outcome to the steps to repeat, and the last of them back into this loop. Waits between passes are durable timers, so polling costs nothing while it sleeps. However many passes it takes, the run continues as new when Temporal suggests it and carries on from the same pass.')}
	{:else if node.type === 'end'}
		{@render note('Completes the run. Lanes already running finish first; anything still running or waiting on the main path stops. In a keyed flow this is how an entity ends: e.g. an On signal close_account lane leading here.')}
	{:else if node.type === 'condition'}
		<Field label="Expression" hint="True follows the true outcome, anything else follows false.">
			<Textarea class="mono min-h-16 text-xs" bind:value={cond.expression} spellcheck={false} />
		</Field>
	{:else if node.type === 'firstOf'}
		<Field label="Winner"><Choice bind:value={() => firstOf.wins ?? 'arrives', (v) => (firstOf.wins = v as FirstOfConfig['wins'])} options={raceRules} /></Field>
		{#if firstOf.wins === 'succeeds'}
			{@render note('A failed branch drops out of the race and the others keep going: ask several services and take the first good answer (a hedged request). If every branch fails, the step takes its error outcome, or fails the run when none is connected.')}
		{:else}
			{@render note('Connect two or more branches into this step. The first branch to arrive continues the flow; Temporal cancels the others (their timers and Activities stop). A failed branch fails the run. Typical use: an approval racing a deadline.')}
		{/if}
	{:else if node.type === 'runFlow'}
		<Field label="Flow to run"><Choice bind:value={runFlow.flowId} options={otherFlows} placeholder="Choose a flow" /></Field>
		{@render jsonField('Child input (JSON template)', 'Becomes the child run\'s {{ input }}.', () => runFlow.input, (v) => (runFlow.input = v))}
		<Field label="Wait">
			<Choice
				bind:value={() => runFlow.wait ?? 'result', (v) => ((runFlow.wait = v as RunFlowConfig['wait']), v === 'start' && (runFlow.parentClosePolicy = 'abandon'))}
				options={waitRules}
			/>
		</Field>
		{#if runFlow.wait === 'start'}
			{@render note('Fire and forget: the step finishes as soon as the child has started, with {{ steps.' + node.key + '.workflowId }} and runId, and this run carries on. The child is abandoned (Parent Close Policy ABANDON), so it keeps running when this run completes, fails, or continues as new.')}
		{:else}
			<Field label="If this run ends first"><Choice bind:value={runFlow.parentClosePolicy} options={closePolicies} placeholder="Terminate the child" /></Field>
			{@render note('Runs as a child Workflow with its own history; the run view links to it. This step waits for its result; the policy applies if this run closes first (for example, it is cancelled or an End run step ends it).')}
		{/if}
	{:else if node.type === 'forEach'}
		<Field label="Items" hint="An expression returning a list, e.g. input.orders or steps.lookup.body.items">
			<Input class="mono text-xs" bind:value={forEach.items} />
		</Field>
		<Field label="Run for each item"><Choice bind:value={() => forEach.run ?? 'flow', (v) => (forEach.run = v as ForEachConfig['run'])} options={eachModes} /></Field>
		{#if forEach.run === 'steps'}
			{@render note(`Draw the steps for one item on the body outcome and connect the last back into this step. They run inside this run, with no child Workflows, and see {{ item }}, {{ index }}, and the item's own step outputs. Each item's outputs are in steps.${node.key}.results. Approvals and fixes in a body are per item (pick the item in the run view), and a Wait for signal there needs a match to say which events are whose. Respond, End run, Loop and For each can't go in a body.`)}
		{:else}
			<Field label="Flow to run for each item"><Choice bind:value={forEach.flowId} options={otherFlows} placeholder="Choose a flow" /></Field>
			{@render jsonField('Input per item (JSON template)', 'Sees {{ item }} and {{ index }}.', () => forEach.input, (v) => (forEach.input = v))}
		{/if}
		<Field label="At most at once" hint="1–20 items run concurrently.">
			<Input type="number" min="1" max="20" bind:value={forEach.concurrency} />
		</Field>
		<div class="space-y-2 border border-[var(--line)] p-2.5">
			<div class="flex items-center justify-between">
				<span class="text-sm">Start at most</span>
				<Switch checked={!!forEach.rate} onCheckedChange={(v) => (forEach.rate = v ? { count: 5, per: 'second' } : undefined)} />
			</div>
			{#if forEach.rate}
				<div class="grid grid-cols-[1fr_130px] gap-1.5">
					<Input type="number" min="1" max="1000" bind:value={forEach.rate.count} />
					<Choice bind:value={forEach.rate.per} options={ratePers} />
				</div>
				{@render note('Items start no faster than this, spaced out by durable timers, to stay under a downstream service\'s quota. The limit is this step\'s, in this run.')}
			{/if}
		</div>
		<Field label="Finish when"><Choice bind:value={() => forEach.finishWhen ?? 'all', (v) => (forEach.finishWhen = v as ForEachConfig['finishWhen'])} options={finishRules} /></Field>
		{#if forEach.finishWhen === 'count'}
			<Field label="How many must succeed"><Input type="number" min="1" max="200" bind:value={() => forEach.count ?? 1, (v) => (forEach.count = Number(v))} /></Field>
		{/if}
		{#if forEach.finishWhen === 'first' || forEach.finishWhen === 'count'}
			{@render note('Once enough items succeed, Temporal cancels the rest. Failed items are tolerated until too few are left to get there. The output has the winners (item, index, and output) in the order they finished.')}
		{:else}
			{@render note('The first failed item cancels the rest. The output has every result, in item order.')}
		{/if}
	{:else if node.type === 'agent'}
		<Field label="Instructions" hint="The goal for Claude. Templates allowed."><Textarea class="min-h-28 text-sm" bind:value={agent.instructions} /></Field>
		<div class="grid grid-cols-2 gap-2">
			<Field label="Max turns"><Input type="number" min={1} max={25} bind:value={agent.maxTurns} /></Field>
			<div class="flex items-end justify-between gap-2 pb-1.5">
				<span class="text-xs">Can ask a human</span>
				<Switch bind:checked={agent.allowAskHuman} />
			</div>
		</div>
		<div class="space-y-2">
			<div class="flex items-center justify-between">
				<span class="eyebrow !text-[0.62rem]">HTTP tools</span>
				<Button variant="outline" size="xs" class="mono" onclick={() => agent.tools.push({ name: `tool_${agent.tools.length + 1}`, description: '', method: 'GET', url: 'https://', params: [] })}><Plus /> tool</Button>
			</div>
			{#each agent.tools as tool, ti (ti)}
				<div class="space-y-2 border border-[var(--line)] bg-[#0f1016] p-2.5">
					<div class="flex gap-1.5">
						<Input class="mono text-xs" bind:value={tool.name} placeholder="tool_name" />
						<Button variant="ghost" size="icon-sm" onclick={() => agent.tools.splice(ti, 1)}><X /></Button>
					</div>
					<Textarea class="min-h-12 text-xs" bind:value={tool.description} placeholder="What the tool does, for the model" />
					<div class="grid grid-cols-[88px_1fr] gap-1.5">
						<Choice bind:value={tool.method} options={['GET', 'POST', 'PUT', 'PATCH', 'DELETE'].map((m) => ({ value: m, label: m }))} />
						<Input class="mono text-[0.68rem]" bind:value={tool.url} placeholder={'https://…/{{ args.id }}'} />
					</div>
					{#each tool.params as p, pi (pi)}
						<div class="flex gap-1.5">
							<Input class="mono w-28 text-[0.68rem]" bind:value={p.name} placeholder="param" />
							<Input class="text-[0.68rem]" bind:value={p.description} placeholder="description" />
							<Button variant="ghost" size="icon-xs" onclick={() => tool.params.splice(pi, 1)}><X /></Button>
						</div>
					{/each}
					<Button variant="ghost" size="xs" class="mono" onclick={() => tool.params.push({ name: '', description: '' })}><Plus /> param</Button>
				</div>
			{/each}
			{#if agent.allowAskHuman}
				{@render note('Built-in ask_human waits durably for an answer from the run view.')}
			{/if}
		</div>
		<ActivitySettings bind:options={agent.modelActivity} {inherited} title="Model calls · retries & timeouts" showStatusCodes={false} showSlowRetry={false} />
		<ActivitySettings bind:options={agent.toolActivity} {inherited} title="Tool calls · retries & timeouts" showSlowRetry={false} />
	{/if}
</div>
