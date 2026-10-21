<script lang="ts">
	import Choice from './Choice.svelte';

	// A time of day as two themed menus, in place of the browser's time
	// picker. 24-hour, like the Schedule it sets.
	let { hour = $bindable(), minute = $bindable() }: { hour: number; minute: number } = $props();

	const two = (n: number) => String(n).padStart(2, '0');
	const hours = Array.from({ length: 24 }, (_, h) => ({ value: String(h), label: two(h) }));
	const minutes = Array.from({ length: 60 }, (_, m) => ({ value: String(m), label: two(m) }));
</script>

<div class="mono grid grid-cols-[1fr_auto_1fr] items-center gap-1.5">
	<Choice bind:value={() => String(hour), (v) => (hour = Number(v))} options={hours} />
	<span class="text-[var(--slate-300)]">:</span>
	<Choice bind:value={() => String(minute), (v) => (minute = Number(v))} options={minutes} />
</div>
