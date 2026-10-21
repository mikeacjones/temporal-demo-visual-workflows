/**
 * Themed confirmation, in place of the browser's confirm(). One dialog
 * (ConfirmHost, in the root layout) serves every request:
 *
 *     if (!(await confirmAction({ title: 'Delete "Orders"?', confirmLabel: 'Delete', destructive: true }))) return;
 */
export interface ConfirmRequest {
	title: string;
	description?: string;
	confirmLabel?: string;
	cancelLabel?: string;
	/** Styles the confirm button as destructive. */
	destructive?: boolean;
}

class ConfirmState {
	request = $state<ConfirmRequest | null>(null);
	#resolve: ((ok: boolean) => void) | null = null;

	ask(req: ConfirmRequest): Promise<boolean> {
		this.#resolve?.(false); // a newer question replaces an unanswered one
		this.request = req;
		return new Promise((resolve) => (this.#resolve = resolve));
	}

	answer(ok: boolean) {
		this.#resolve?.(ok);
		this.#resolve = null;
		this.request = null;
	}
}

export const confirmState = new ConfirmState();

export function confirmAction(req: ConfirmRequest): Promise<boolean> {
	return confirmState.ask(req);
}
