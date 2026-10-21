import { shell } from './shell.svelte';
import type { ConnectorConfig, ConnectorOperation, ConnectorSpec, ConnectorType } from './types';

/** A connector's spec, from the session (internal/connectors). */
export function connectorSpec(type: ConnectorType): ConnectorSpec | undefined {
	return shell.session?.connectors?.find((c) => c.type === type);
}

export function connectorOperation(type: ConnectorType, name: string): ConnectorOperation | undefined {
	return connectorSpec(type)?.operations.find((o) => o.name === name);
}

/** The values an operation's fields start with. */
export function operationDefaults(op: ConnectorOperation): Record<string, string | boolean> {
	return Object.fromEntries(op.params.filter((p) => p.default).map((p) => [p.key, p.default!]));
}

/** A new connector step: its first operation, with that operation's defaults. */
export function connectorDefaults(type: ConnectorType): ConnectorConfig {
	const op = connectorSpec(type)?.operations[0];
	return { operation: op?.name ?? '', params: op ? operationDefaults(op) : {}, activity: { retry: {} } };
}

/** The step card's text: the operation's summary with its parameters filled in. */
export function connectorSummary(type: ConnectorType, c: ConnectorConfig): string {
	const op = connectorOperation(type, c.operation);
	if (!op) return c.operation || 'choose an operation';
	const text = op.summary.replace(/\{(\w+)\}/g, (_, k: string) => String(c.params?.[k] ?? '').trim() || '…');
	const extras = [c.undo && 'undo', c.activity?.slowRetry && 'slow retries'].filter(Boolean);
	return text + extras.map((x) => ' · ' + x).join('');
}
