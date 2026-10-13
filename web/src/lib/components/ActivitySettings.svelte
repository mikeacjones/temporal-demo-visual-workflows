<script lang="ts">
	import ChevronRight from '@lucide/svelte/icons/chevron-right';
	import * as Collapsible from '$lib/components/ui/collapsible';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Switch } from '$lib/components/ui/switch';
	import ChipsInput from './ChipsInput.svelte';
	import { resolveActivity, retryPreview } from '$lib/activity';
	import { humanDuration } from '$lib/duration';
	import { MAX_START_TO_CLOSE_SECONDS, type ActivityOptions } from '$lib/types';

	// Retries & timeouts for anything that calls out. Blank fields inherit;
	// the inherited value shows as the placeholder.
	let {
		options = $bindable(),
		inherited,
		title = 'Retries & timeouts',
		inheritedFrom = 'flow defaults',
		open = false,
		showStatusCodes = true,
		showSlowRetry = true
	}: {
		options: ActivityOptions;
		inherited: ActivityOptions;
		title?: string;
		inheritedFrom?: string;
		open?: boolean;
		showStatusCodes?: boolean;
		/** Fast-then-slow retries run as a second Activity; HTTP steps support it. */
		showSlowRetry?: boolean;
	} = $props();

	const resolved = $derived(resolveActivity(options, inherited));
	const preview = $derived(retryPreview(resolved));
	const overridden = $derived(
		!!(options.startToCloseSeconds || options.scheduleToCloseSeconds || Object.values(options.retry ?? {}).some((v) => (Array.isArray(v) ? v.length : v)))
	);
	const tooLong = $derived((options.startToCloseSeconds ?? 0) > MAX_START_TO_CLOSE_SECONDS);

	type NumKey = 'startToCloseSeconds' | 'scheduleToCloseSeconds';
	type RetryKey = 'maxAttempts' | 'initialIntervalSeconds' | 'backoffCoefficient' | 'maximumIntervalSeconds';

	const num = (raw: string) => {
		const n = Number(raw);
		return raw === '' || !isFinite(n) || n <= 0 ? undefined : n;
	};
	function setTop(k: NumKey, raw: string) {
		const v = num(raw);
		options[k] = v === undefined ? undefined : Math.round(v);
	}
	function setRetry(k: RetryKey, raw: string) {
		options.retry ??= {};
		options.retry[k] = num(raw);
	}
	const ph = (v: number | undefined, unit = '') => (v ? `${v}${unit}` : 'none');

	function setSlow(k: 'maxAttempts' | 'initialIntervalSeconds' | 'backoffCoefficient' | 'maximumIntervalSeconds', raw: string) {
		options.slowRetry ??= { initialIntervalSeconds: 60 };
		options.slowRetry[k] = num(raw);
	}
</script>

{#snippet field(label: string, value: number | undefined, placeholder: string, set: (raw: string) => void, step = '1')}
	<div class="space-y-1">
		<Label class="mono text-[0.6rem] tracking-wide text-[var(--indigo-100)] uppercase">{label}</Label>
		<Input type="number" min="0" {step} value={value ?? ''} {placeholder} oninput={(e) => set(e.currentTarget.value)} class="h-7 text-xs" />
	</div>
{/snippet}

<Collapsible.Root {open} class="border border-[var(--line)]">
	<Collapsible.Trigger class="group flex w-full items-center gap-2 px-2.5 py-2 text-left">
		<ChevronRight class="size-3.5 text-[var(--slate-300)] transition-transform group-data-[state=open]:rotate-90" />
		<span class="eyebrow !text-[0.62rem]">{title}</span>
		<span class="mono ml-auto text-[0.6rem] text-[var(--slate-300)]">{overridden ? 'customised' : `inherited from ${inheritedFrom}`}</span>
	</Collapsible.Trigger>
	<Collapsible.Content class="space-y-3 border-t border-[var(--line)] p-2.5">
		<div class="grid grid-cols-2 gap-2">
			{@render field('Attempt timeout s', options.startToCloseSeconds, ph(inherited.startToCloseSeconds), (r) => setTop('startToCloseSeconds', r))}
			{@render field('All attempts s', options.scheduleToCloseSeconds, ph(inherited.scheduleToCloseSeconds), (r) => setTop('scheduleToCloseSeconds', r))}
		</div>
		<p class="text-[0.64rem] leading-snug {tooLong ? 'text-[var(--red)]' : 'text-[var(--slate-300)]'}">
			Each attempt is capped at {MAX_START_TO_CLOSE_SECONDS}s so it fits inside a serverless worker invocation.
		</p>
		<div class="grid grid-cols-2 gap-2">
			{@render field('Max attempts', options.retry?.maxAttempts, ph(inherited.retry.maxAttempts), (r) => setRetry('maxAttempts', r))}
			{@render field('First wait s', options.retry?.initialIntervalSeconds, ph(inherited.retry.initialIntervalSeconds), (r) => setRetry('initialIntervalSeconds', r), '0.5')}
			{@render field('Backoff ×', options.retry?.backoffCoefficient, ph(inherited.retry.backoffCoefficient), (r) => setRetry('backoffCoefficient', r), '0.5')}
			{@render field('Max wait s', options.retry?.maximumIntervalSeconds, ph(inherited.retry.maximumIntervalSeconds), (r) => setRetry('maximumIntervalSeconds', r))}
		</div>
		<div class="space-y-1">
			<Label class="mono text-[0.6rem] tracking-wide text-[var(--indigo-100)] uppercase">Don't retry these error types</Label>
			<ChipsInput
				bind:values={() => options.retry?.nonRetryableErrorTypes, (v) => ((options.retry ??= {}), (options.retry.nonRetryableErrorTypes = v as string[] | undefined))}
				suggestions={['HTTPClientError', 'InvalidRequest', 'TemplateError']}
				placeholder={inherited.retry.nonRetryableErrorTypes?.join(', ') || 'error type'}
			/>
		</div>
		{#if showStatusCodes}
			<div class="space-y-1">
				<Label class="mono text-[0.6rem] tracking-wide text-[var(--indigo-100)] uppercase">Don't retry these HTTP statuses</Label>
				<ChipsInput
					numeric
					bind:values={() => options.retry?.nonRetryableStatusCodes, (v) => ((options.retry ??= {}), (options.retry.nonRetryableStatusCodes = v as number[] | undefined))}
					suggestions={['409', '422']}
					placeholder={inherited.retry.nonRetryableStatusCodes?.join(', ') || 'e.g. 409'}
				/>
				<p class="text-[0.62rem] text-[var(--slate-300)]">Most 4xx responses already fail fast; 429 and 5xx are retried.</p>
			</div>
		{/if}
		{#if showSlowRetry}
			<div class="space-y-2 border-t border-[var(--line)] pt-2">
				<div class="flex items-center justify-between">
					<span class="mono text-[0.6rem] tracking-wide text-[var(--indigo-100)] uppercase">Then retry slowly</span>
					<Switch
						checked={!!options.slowRetry}
						onCheckedChange={(v) => (options.slowRetry = v ? { initialIntervalSeconds: 60, backoffCoefficient: 1 } : undefined)}
					/>
				</div>
				{#if options.slowRetry}
					<div class="grid grid-cols-2 gap-2">
						{@render field('Slow attempts', options.slowRetry.maxAttempts, 'unlimited', (r) => setSlow('maxAttempts', r))}
						{@render field('Every s', options.slowRetry.initialIntervalSeconds, '60', (r) => setSlow('initialIntervalSeconds', r))}
					</div>
				{/if}
				<p class="text-[0.62rem] leading-snug text-[var(--slate-300)]">
					Fast/slow retries: when the attempts above are used up, a second Activity keeps retrying on a slow schedule, to outlast a long outage without hammering the
					service. {inherited.slowRetry && !options.slowRetry ? 'Inherited from flow defaults.' : ''}
				</p>
			</div>
		{/if}
		<div class="space-y-1.5 border-t border-[var(--line)] pt-2">
			<div class="mono text-[0.6rem] tracking-wide text-[var(--indigo-100)] uppercase">If it keeps failing</div>
			<div class="flex flex-wrap items-center gap-1">
				<span class="mono border border-[var(--line)] px-1 text-[0.6rem]">1</span>
				{#each preview.waits as w, i (i)}
					<span class="h-px w-2 bg-[var(--slate-500)]"></span>
					<span class="mono text-[0.58rem] text-[var(--pink)]">+{humanDuration(w)}</span>
					<span class="h-px w-2 bg-[var(--slate-500)]"></span>
					<span class="mono border border-[var(--line)] px-1 text-[0.6rem]">{i + 2}</span>
				{/each}
			</div>
			<p class="mono text-[0.62rem] leading-snug text-[var(--slate-300)]">{preview.text}</p>
		</div>
	</Collapsible.Content>
</Collapsible.Root>
