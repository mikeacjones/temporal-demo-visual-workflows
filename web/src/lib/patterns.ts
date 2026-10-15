import { toast } from 'svelte-sonner';
import { api } from './api';
import { shell } from './shell.svelte';
import { app, errorMessage, normalizeFlow } from './state.svelte';
import { tidy } from './layout';
import type { Definition, Pattern } from './types';

export const SUPPORT: Record<Pattern['support'], { label: string; color: string; hint: string }> = {
	visual: { label: 'draw it', color: 'var(--green)', hint: 'Built in the visual designer; open the example to try it.' },
	'built-in': { label: 'built in', color: 'var(--cyan)', hint: 'Every flow gets this without drawing anything.' },
	'not-in-builder': { label: 'not a flow concern', color: 'var(--slate-300)', hint: 'A worker or deployment choice; the page explains what to use instead.' }
};

/** Pattern examples are laid out automatically, like Tidy up in the editor. */
export function laidOut(flow: Definition): Definition {
	const f = normalizeFlow(structuredClone(flow));
	const pos = tidy(f, f.settings.direction ?? 'LR', new Map());
	for (const n of f.nodes) if (pos[n.id]) n.position = pos[n.id];
	return f;
}

/** Copies a pattern's example into the user's flows and opens it. */
export async function usePattern(p: Pattern) {
	try {
		const detail = await api.usePattern(p.id);
		const extra = p.children?.length ? ` (with its child flow${p.children.length > 1 ? 's' : ''})` : '';
		toast.success(`"${detail.flow.name}" is in your workflows${extra}`);
		await app.openCreated(detail);
	} catch (err) {
		toast.error(`Could not open the pattern: ${errorMessage(err)}`);
	}
}
