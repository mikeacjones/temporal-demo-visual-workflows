<script lang="ts">
	import { onMount } from 'svelte';

	// Themed tooltips, in place of the browser's title tooltips. Any element
	// with data-tip="…" gets one on hover or keyboard focus; components pass
	// data-* attributes through to their element, so this works on Buttons
	// and table cells too. One floating element serves the whole app.
	let text = $state('');
	let x = $state(0);
	let y = $state(0);
	let below = $state(false);
	let visible = $state(false);
	let tip = $state<HTMLDivElement>();

	const DELAY = 350;
	const GAP = 6;

	onMount(() => {
		let target: HTMLElement | null = null;
		let timer: ReturnType<typeof setTimeout> | undefined;

		const place = () => {
			if (!target || !tip) return;
			const r = target.getBoundingClientRect();
			const t = tip.getBoundingClientRect();
			below = r.top - t.height - GAP < 4;
			y = below ? r.bottom + GAP : r.top - t.height - GAP;
			x = Math.min(Math.max(4, r.left + r.width / 2 - t.width / 2), window.innerWidth - t.width - 4);
		};
		const show = (el: HTMLElement) => {
			const next = el.dataset.tip;
			if (!next) return;
			target = el;
			clearTimeout(timer);
			timer = setTimeout(async () => {
				if (target !== el || !el.isConnected) return;
				text = next;
				visible = true;
				await Promise.resolve(); // let the text render before measuring
				requestAnimationFrame(place);
			}, visible ? 0 : DELAY);
		};
		const hide = () => {
			clearTimeout(timer);
			target = null;
			visible = false;
		};
		const tipOf = (e: Event) => (e.target instanceof Element ? (e.target.closest('[data-tip]') as HTMLElement | null) : null);

		// Hit-test under the pointer rather than trusting the event's target:
		// disabled buttons don't always get pointer events, and their tips
		// often say why they're disabled.
		let frame = 0;
		const over = (e: PointerEvent) => {
			if (e.pointerType === 'touch' || frame) return;
			frame = requestAnimationFrame(() => {
				frame = 0;
				const el = document.elementFromPoint(e.clientX, e.clientY)?.closest('[data-tip]') as HTMLElement | null;
				if (el && el !== target) show(el);
				else if (!el && target) hide();
			});
		};
		const focusIn = (e: FocusEvent) => {
			const el = tipOf(e);
			if (el && (e.target as Element).matches(':focus-visible')) show(el);
		};
		const key = (e: KeyboardEvent) => e.key === 'Escape' && hide();

		document.addEventListener('pointermove', over);
		document.documentElement.addEventListener('pointerleave', hide);
		document.addEventListener('pointerdown', hide, true);
		document.addEventListener('focusin', focusIn);
		document.addEventListener('focusout', hide);
		document.addEventListener('keydown', key);
		window.addEventListener('scroll', hide, true);
		window.addEventListener('blur', hide);
		return () => {
			clearTimeout(timer);
			document.removeEventListener('pointermove', over);
			cancelAnimationFrame(frame);
			document.documentElement.removeEventListener('pointerleave', hide);
			document.removeEventListener('pointerdown', hide, true);
			document.removeEventListener('focusin', focusIn);
			document.removeEventListener('focusout', hide);
			document.removeEventListener('keydown', key);
			window.removeEventListener('scroll', hide, true);
			window.removeEventListener('blur', hide);
		};
	});
</script>

<div
	bind:this={tip}
	role="tooltip"
	class="tooltip-host mono pointer-events-none fixed z-[100] max-w-xs border border-[var(--line)] bg-[var(--popover)] px-2.5 py-1.5 text-[0.68rem] leading-snug break-words text-[var(--off-white)] shadow-lg"
	class:below
	class:shown={visible}
	style:left="{x}px"
	style:top="{y}px"
	aria-hidden={!visible}
>
	{text}
</div>

<style>
	.tooltip-host {
		opacity: 0;
		transform: translateY(2px);
		transition:
			opacity 90ms ease-out,
			transform 90ms ease-out;
	}
	.tooltip-host.below {
		transform: translateY(-2px);
	}
	.tooltip-host.shown {
		opacity: 1;
		transform: none;
	}
</style>
