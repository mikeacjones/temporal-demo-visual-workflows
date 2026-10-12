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
		ForEachConfig,
		HTTPConfig,
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

	let { node }: { node: FlowNode } = $props();

	const spec = $derived(NODE_SPECS[node.type]);
	const problems = $derived(app.problemsFor(node.id));
	const keyOk = $derived(/^[a-z][a-z0-9_]*$/.test(node.key));
	const inherited = $derived(flowDefaults(app.flow?.settings.activity));
	const variables = $derived(app.flow?.settings.variables ?? []);
	const otherFlows = $derived(shell.flows.filter((f) => f.id !== app.flow?.id).map((f) => ({ value: f.id, label: `${f.name} (v${f.version})` })));

	const kinds = [
		{ value: 'manual', label: 'Manual — Start Workflow from the UI' },
		{ value: 'webhook', label: 'Webhook — POST starts a Workflow' },
		{ value: 'schedule', label: 'Schedule — Temporal Schedule' }
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
	const agent = $derived(node.config as AgentConfig);

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
		{@render jsonField('Sample input (JSON)', 'Prefills the Run dialog. Steps read it as {{ input.field }}.', () => trigger.sampleInput, (v) => (trigger.sampleInput = v))}
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
		<div class="space-y-2 border border-[var(--line)] p-2.5">
			<div class="flex items-center justify-between">
				<span class="eyebrow flex items-center gap-1.5 !text-[0.62rem]"><Undo2 class="size-3" /> Undo</span>
				<Switch checked={!!http.undo} onCheckedChange={(v) => (http.undo = v ? { method: 'POST', url: 'https://', headers: {}, body: '' } : undefined)} />
			</div>
			{@render note('A compensating request. If the run fails later, completed undo requests run in reverse order (a saga).')}
			{#if http.undo}
				<HttpRequestFields bind:req={() => http.undo!, () => {}} />
			{/if}
		</div>
		<p class="text-[0.68rem] leading-snug text-[var(--slate-300)]">
			To let the called system report back later, send it
			<span class="mono text-[var(--green)]">{'{{ run.callbackUrl }}/events/<signal>'}</span>; a POST there signals this run.
		</p>
	{:else if node.type === 'transform'}
		{@render jsonField('Output (JSON template)', 'String values can be expressions: {{ steps.lookup.body.email }}', () => xform.output, (v) => (xform.output = v), 'min-h-40')}
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
		{@render jsonField('Response (JSON template)', 'Returned to whoever sent the Update.', () => respond.value, (v) => (respond.value = v))}
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
		<Field label="Timeout" hint="A durable timer; a timeout routes to the timeout outcome."><DurationInput bind:value={sig.timeoutSeconds} placeholder="wait forever" /></Field>
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
	{:else if node.type === 'condition'}
		<Field label="Expression" hint="True follows the true outcome, anything else follows false.">
			<Textarea class="mono min-h-16 text-xs" bind:value={cond.expression} spellcheck={false} />
		</Field>
	{:else if node.type === 'firstOf'}
		{@render note('Connect two or more branches into this step. The first branch to arrive continues the flow; Temporal cancels the others (their timers and Activities stop). Typical use: an approval racing a deadline.')}
	{:else if node.type === 'runFlow'}
		<Field label="Flow to run"><Choice bind:value={runFlow.flowId} options={otherFlows} placeholder="Choose a flow" /></Field>
		{@render jsonField('Child input (JSON template)', 'Becomes the child run\'s {{ input }}.', () => runFlow.input, (v) => (runFlow.input = v))}
		<Field label="If this run ends first"><Choice bind:value={runFlow.parentClosePolicy} options={closePolicies} placeholder="Terminate the child" /></Field>
		{@render note('Runs as a child Workflow with its own history; the run view links to it.')}
	{:else if node.type === 'forEach'}
		<Field label="Items" hint="An expression returning a list, e.g. input.orders or steps.lookup.body.items">
			<Input class="mono text-xs" bind:value={forEach.items} />
		</Field>
		<Field label="Flow to run for each item"><Choice bind:value={forEach.flowId} options={otherFlows} placeholder="Choose a flow" /></Field>
		{@render jsonField('Input per item (JSON template)', 'Sees {{ item }} and {{ index }}.', () => forEach.input, (v) => (forEach.input = v))}
		<Field label="At most at once" hint="1–20 child Workflows run concurrently.">
			<Input type="number" min="1" max="20" bind:value={forEach.concurrency} />
		</Field>
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
		<ActivitySettings bind:options={agent.modelActivity} {inherited} title="Model calls · retries & timeouts" showStatusCodes={false} />
		<ActivitySettings bind:options={agent.toolActivity} {inherited} title="Tool calls · retries & timeouts" />
	{/if}
</div>
