import { shell } from './shell.svelte';
import type { ConnectorConfig, ConnectorField, ConnectorOperation, ConnectorSpec, ConnectorType } from './types';

// Business connectors (internal/connectors): their specs come with the
// session. These helpers mirror the Go side's: resolving catalog fields,
// finding a field by path, and what a step still has to map.

export function connectorSpec(type: ConnectorType): ConnectorSpec | undefined {
	return shell.session?.connectors?.find((c) => c.type === type);
}

export function connectorOperation(type: ConnectorType, name: string): ConnectorOperation | undefined {
	return connectorSpec(type)?.operations.find((o) => o.name === name);
}

/** A field with its catalog fields filled in: Account's when the step's object is Account. */
export function resolveField(spec: ConnectorSpec, f: ConnectorField, settings: ConnectorConfig['settings']): ConnectorField {
	if (!f.catalog) return f;
	const name = settings?.[f.catalog];
	const fields = typeof name === 'string' ? spec.catalog?.[name] : undefined;
	if (!fields) return { ...f, fields: [] };
	return { ...f, fields: f.creating ? fields : fields.map((cf) => ({ ...cf, required: false })) };
}

/** The catalog entry a field's setting names, if the catalog knows it. */
export function catalogKnows(spec: ConnectorSpec, f: ConnectorField, settings: ConnectorConfig['settings']): boolean {
	const name = f.catalog ? settings?.[f.catalog] : undefined;
	return typeof name === 'string' && !!spec.catalog?.[name];
}

/** The values a new step of this operation starts with. */
export function operationDefaults(op: ConnectorOperation): Pick<ConnectorConfig, 'settings' | 'mapping'> {
	const settings: Record<string, string | boolean> = {};
	const mapping: Record<string, string> = {};
	for (const f of op.settings ?? []) if (f.default) settings[f.name] = f.default;
	for (const f of op.inputs ?? []) if (f.default) mapping[f.name] = f.default;
	return { settings, mapping };
}

/** A new connector step: the given operation (or the first), with its defaults. */
export function connectorDefaults(type: ConnectorType, operation?: string): ConnectorConfig {
	const spec = connectorSpec(type);
	const op = spec?.operations.find((o) => o.name === operation) ?? spec?.operations[0];
	return { operation: op?.name ?? '', ...(op ? operationDefaults(op) : { settings: {}, mapping: {} }), activity: { retry: {} } };
}

/** Mapped means a template that isn't blank, at the path or under it. */
export function isMapped(c: ConnectorConfig, path: string): boolean {
	return Object.entries(c.mapping ?? {}).some(([k, v]) => (k === path || k.startsWith(path + '.')) && v.trim() !== '');
}

/** The required inputs a step doesn't map, as the Go validation reports them. */
export function missingInputs(spec: ConnectorSpec, op: ConnectorOperation, c: ConnectorConfig): string[] {
	const missing: string[] = [];
	for (const raw of op.inputs ?? []) {
		const f = resolveField(spec, raw, c.settings);
		if (f.required && !isMapped(c, f.name)) {
			missing.push(f.name);
			continue;
		}
		if (f.type === 'object' && !(c.mapping?.[f.name] ?? '').trim()) {
			for (const sub of f.fields ?? []) if (sub.required && !sub.readOnly && !isMapped(c, `${f.name}.${sub.name}`)) missing.push(`${f.name}.${sub.name}`);
		}
	}
	return missing;
}

/** How many inputs the step maps: leaf paths with a template. */
export function mappedCount(c: ConnectorConfig): number {
	return Object.values(c.mapping ?? {}).filter((v) => v.trim() !== '').length;
}

/** The step card's eyebrow: SALESFORCE · UPSERT RECORD. */
export function connectorEyebrow(type: ConnectorType, c: ConnectorConfig): string {
	const spec = connectorSpec(type);
	const op = spec?.operations.find((o) => o.name === c.operation);
	return op ? `${spec!.label} · ${op.label}` : (spec?.label ?? type);
}

/** The step card's text: the operation's summary with its settings filled in. */
export function connectorSummary(type: ConnectorType, c: ConnectorConfig): string {
	const op = connectorOperation(type, c.operation);
	if (!op) return c.operation || 'choose an operation';
	const text = op.summary.replace(/\{(\w+)\}/g, (_, k: string) => String(c.settings?.[k] ?? '').trim() || '…');
	const extras = [c.undo && 'undo', c.activity?.slowRetry && 'slow retries'].filter(Boolean);
	return text + extras.map((x) => ' · ' + x).join('');
}

/** The mapping badge on a step card: what's still required, or how much is mapped. */
export function connectorBadge(type: ConnectorType, c: ConnectorConfig): { text: string; missing: boolean } | undefined {
	const spec = connectorSpec(type);
	const op = spec?.operations.find((o) => o.name === c.operation);
	if (!spec || !op?.inputs?.length) return undefined;
	const missing = missingInputs(spec, op, c).length;
	if (missing) return { text: `${missing} required to map`, missing: true };
	return { text: `${mappedCount(c)} mapped`, missing: false };
}

/**
 * A step's outputs, for showing what it returns. A SOQL query's records get
 * the fields its SELECT names, typed from the catalog where it knows them.
 */
export function outputFields(spec: ConnectorSpec, op: ConnectorOperation, c: ConnectorConfig): ConnectorField[] {
	return (op.outputs ?? []).map((raw) => {
		const f = resolveField(spec, raw, c.settings);
		if (spec.type === 'salesforce' && op.name === 'query' && f.name === 'records') {
			return { ...f, fields: soqlFields(spec, c.mapping?.query ?? '') };
		}
		return f;
	});
}

/** The fields a SOQL query selects, nesting relationships (Account.Name). */
export function soqlFields(spec: ConnectorSpec, soql: string): ConnectorField[] {
	const m = /^\s*select\s+([\s\S]+?)\s+from\s+(\w+)/i.exec(soql);
	if (!m || /count\(\)/i.test(m[1])) return [];
	const object = Object.keys(spec.catalog ?? {}).find((k) => k.toLowerCase() === m[2].toLowerCase()) ?? m[2];
	const typed = (obj: string, name: string): ConnectorField =>
		spec.catalog?.[obj]?.find((f) => f.name.toLowerCase() === name.toLowerCase()) ?? { name, type: '' };
	const out: ConnectorField[] = [];
	for (const raw of m[1].split(',').map((s) => s.trim()).filter(Boolean)) {
		const [rel, sub] = raw.split('.', 2);
		if (!sub) {
			out.push({ ...typed(object, rel), name: rel, required: false });
			continue;
		}
		let parent = out.find((f) => f.name === rel);
		if (!parent) out.push((parent = { name: rel, type: 'object', fields: [] }));
		parent.fields!.push({ ...typed(rel, sub), name: sub, required: false });
	}
	return out;
}

/** Steps saved with params (settings and inputs in one map, objects as JSON templates) as settings and a mapping. */
export function migrateParams(c: ConnectorConfig): void {
	if (!c.params || c.mapping || c.settings) return;
	const settings: Record<string, string | boolean> = {};
	const mapping: Record<string, string> = {};
	const flatten = (prefix: string, obj: Record<string, unknown>) => {
		for (const [k, v] of Object.entries(obj)) {
			if (v && typeof v === 'object' && !Array.isArray(v)) flatten(`${prefix}.${k}`, v as Record<string, unknown>);
			else mapping[`${prefix}.${k}`] = typeof v === 'string' ? v : JSON.stringify(v);
		}
	};
	for (const [k, v] of Object.entries(c.params)) {
		if (typeof v === 'boolean') settings[k] = v;
		else if (k !== 'blocks' && v.trim().startsWith('{')) {
			try {
				flatten(k, JSON.parse(v));
			} catch {
				mapping[k] = v;
			}
		} else mapping[k] = v;
	}
	c.settings = settings;
	c.mapping = mapping;
	delete c.params;
}

/** Move a migrated step's settings out of its mapping, once its operation says which are settings. */
export function settleSettings(op: ConnectorOperation, c: ConnectorConfig): void {
	for (const f of op.settings ?? []) {
		if (c.mapping && f.name in c.mapping) {
			c.settings ??= {};
			c.settings[f.name] = c.mapping[f.name];
			delete c.mapping[f.name];
		}
	}
}

export const typeLabel = (f: ConnectorField): string =>
	f.type === 'array' ? `list${f.items ? ` of ${f.items}` : f.fields?.length ? ' of objects' : ''}` : f.type || 'any';
