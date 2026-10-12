<script lang="ts">
	import { page } from '$app/state';
	import LayoutDashboard from '@lucide/svelte/icons/layout-dashboard';
	import Workflow from '@lucide/svelte/icons/workflow';
	import ListChecks from '@lucide/svelte/icons/list-checks';
	import CalendarClock from '@lucide/svelte/icons/calendar-clock';
	import LayoutTemplate from '@lucide/svelte/icons/layout-template';
	import ExternalLink from '@lucide/svelte/icons/external-link';
	import { shell } from '$lib/shell.svelte';

	// The platform's left navigation: a slim rail so the editor keeps its width.
	const items = [
		{ href: '/', label: 'Overview', icon: LayoutDashboard, match: (p: string) => p === '/' },
		{ href: '/workflows', label: 'Workflows', icon: Workflow, match: (p: string) => p.startsWith('/workflows') },
		{ href: '/executions', label: 'Executions', icon: ListChecks, match: (p: string) => p.startsWith('/executions') },
		{ href: '/schedules', label: 'Schedules', icon: CalendarClock, match: (p: string) => p.startsWith('/schedules') },
		{ href: '/templates', label: 'Templates', icon: LayoutTemplate, match: (p: string) => p.startsWith('/templates') }
	];
	const path = $derived(page.url.pathname);
</script>

<nav class="flex h-full w-[76px] shrink-0 flex-col items-center border-r border-[var(--line)] bg-[#0f1015]/95 py-3" aria-label="Main">
	<a href="/" class="mb-4 grid place-items-center" title="Visual Workflows · Temporal">
		<img src="/favicon.svg" alt="Temporal" class="size-8" />
	</a>
	<div class="flex flex-col gap-1">
		{#each items as it (it.href)}
			{@const active = it.match(path)}
			{@const Icon = it.icon}
			<a
				href={it.href}
				class="flex w-[64px] flex-col items-center gap-1 border-l-2 px-1 py-2 text-center transition-colors hover:bg-[var(--accent)]"
				style:border-color={active ? 'var(--green)' : 'transparent'}
				style:background={active ? 'rgba(68,76,231,.16)' : undefined}
				style:color={active ? 'var(--off-white)' : 'var(--slate-300)'}
				aria-current={active ? 'page' : undefined}
			>
				<Icon class="size-[18px]" />
				<span class="mono text-[0.55rem] tracking-wide uppercase">{it.label}</span>
			</a>
		{/each}
	</div>
	<div class="mt-auto flex flex-col items-center gap-2">
		{#if shell.temporalWorkflowsUrl()}
			<a href={shell.temporalWorkflowsUrl()} target="_blank" rel="noreferrer" class="flex flex-col items-center gap-0.5 text-[var(--slate-300)] hover:text-[var(--off-white)]" title="Open the Temporal UI">
				<ExternalLink class="size-4" />
				<span class="mono text-[0.5rem] uppercase">Temporal UI</span>
			</a>
		{/if}
		{#if shell.session?.signedIn && shell.session.displayName}
			<span class="mono grid size-8 place-items-center border border-[var(--line)] text-[0.65rem] text-[var(--indigo-100)] uppercase" title={shell.session.displayName}>
				{shell.session.displayName.slice(0, 2)}
			</span>
		{/if}
	</div>
</nav>
