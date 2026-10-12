<script lang="ts">
	import * as Select from '$lib/components/ui/select';

	let {
		value = $bindable(),
		options,
		onchange,
		placeholder = 'Choose…'
	}: {
		value: string | undefined;
		options: { value: string; label: string }[];
		onchange?: (v: string) => void;
		placeholder?: string;
	} = $props();

	const current = $derived(options.find((o) => o.value === value)?.label ?? (value || placeholder));
</script>

<Select.Root type="single" bind:value={() => value ?? '', (v) => ((value = v), onchange?.(v))}>
	<Select.Trigger class="w-full truncate"><span class="truncate" class:text-muted-foreground={!value}>{current}</span></Select.Trigger>
	<Select.Content>
		{#each options as o (o.value)}
			<Select.Item value={o.value} label={o.label} />
		{/each}
	</Select.Content>
</Select.Root>
