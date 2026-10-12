import type { Definition } from './types';

/** Saves a flow as a JSON file. */
export function downloadFlow(flow: Definition) {
	const blob = new Blob([JSON.stringify(flow, null, 2)], { type: 'application/json' });
	const a = document.createElement('a');
	a.href = URL.createObjectURL(blob);
	a.download = `${flow.name.toLowerCase().replace(/[^a-z0-9]+/g, '-') || 'flow'}.json`;
	a.click();
	URL.revokeObjectURL(a.href);
}

/** Reads a flow from a JSON file the user picked. The server assigns a new id and version. */
export async function readFlowFile(file: File): Promise<Partial<Definition>> {
	const parsed = JSON.parse(await file.text());
	if (!parsed || !Array.isArray(parsed.nodes)) throw new Error('That file is not a flow export (no nodes).');
	const { id: _id, version: _v, updatedAt: _u, ...rest } = parsed;
	return rest;
}
