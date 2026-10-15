import { toast } from 'svelte-sonner';
import { api } from './api';
import { app, errorMessage } from './state.svelte';

/** Copies a template into the user's flows and opens it in the editor. */
export async function useTemplate(id: string, token?: string) {
	try {
		const detail = await api.useTemplate(id, token);
		toast.success(`"${detail.flow.name}" is yours to edit`);
		await app.openCreated(detail);
	} catch (err) {
		toast.error(`Could not use the template: ${errorMessage(err)}`);
	}
}
