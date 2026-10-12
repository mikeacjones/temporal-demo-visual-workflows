<script lang="ts">
	import { Input } from '$lib/components/ui/input';
	import Choice from './Choice.svelte';
	import { UNITS, splitSeconds, unitSeconds } from '$lib/duration';
	import type { TimeUnit } from '$lib/types';

	// Amount + unit picker that stores whole seconds. Empty or 0 means "none"
	// (wait forever / inherit), shown with the given placeholder.
	let {
		value = $bindable(),
		placeholder = 'none',
		min = 0
	}: { value: number | undefined; placeholder?: string; min?: number } = $props();

	let unit = $state<TimeUnit>(splitSeconds(value ?? 0).unit);
	const amount = $derived(value ? value / unitSeconds(unit) : undefined);

	function setAmount(raw: string) {
		const n = Number(raw);
		value = raw === '' || !isFinite(n) || n <= 0 ? (min > 0 ? min : 0) : Math.round(n * unitSeconds(unit));
	}

	function setUnit(u: string) {
		const a = amount;
		unit = u as TimeUnit;
		if (a) value = Math.round(a * unitSeconds(unit));
	}
</script>

<div class="grid grid-cols-[1fr_110px] gap-1.5">
	<Input type="number" min="0" step="any" value={amount ?? ''} {placeholder} oninput={(e) => setAmount(e.currentTarget.value)} />
	<Choice value={unit} options={UNITS.map((u) => ({ value: u.value, label: u.label }))} onchange={setUnit} />
</div>
