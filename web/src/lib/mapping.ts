import type { ConnectorField, ListMapping, TransformConfig } from './types';

// The mapper's view of a mapping: each input's template, read as the source
// fields it uses. A template that is exactly one reference ({{ input.company }})
// is a direct mapping, drawn as a solid line; one that computes something
// (upper(...), text around a placeholder) is an expression, drawn dashed.

// `it` and `itIndex` are a list mapping's current element and its index.
const ROOT = '(?:input|steps|vars|item|index|run|events|key|itIndex|it)';
const REF = new RegExp(`\\b${ROOT}(?:\\.[A-Za-z_][\\w]*|\\[\\d+\\])*`, 'g');
const PLACEHOLDER = /\{\{([\s\S]*?)\}\}/g;

/** Normalise a reference to a mapper path: list indexes become []. */
export const asPath = (ref: string) => ref.replace(/\[\d+\]/g, '[]');

/** The source references in a template, as mapper paths. */
export function references(template: string): string[] {
	const out = new Set<string>();
	for (const m of template.matchAll(PLACEHOLDER)) {
		// Ignore member access on a local (map(list, .Name)) and string literals.
		const expr = m[1].replace(/'[^']*'|"[^"]*"/g, '');
		for (const r of expr.matchAll(REF)) {
			const before = expr[r.index! - 1];
			if (before === '.' || before === '#') continue;
			out.add(asPath(r[0]));
		}
	}
	return [...out];
}

/** The reference a template is, when it is nothing but one: {{ input.company }}. */
export function directReference(template: string): string | null {
	const m = /^\s*\{\{\s*([A-Za-z_][\w.[\]]*)\s*\}\}\s*$/.exec(template);
	return m && new RegExp(`^${ROOT}\\b`).test(m[1]) ? asPath(m[1]) : null;
}

/** A fixed value: a template without placeholders. */
export const isConstant = (template: string) => template.trim() !== '' && !template.includes('{{');

/**
 * The template a drag from a source field onto a target writes. From a field
 * inside a list (records[].Name): onto a list target, every element's
 * (map(records, .Name)); onto anything else, the first element's.
 */
export function templateFor(source: string, target: ConnectorField): string {
	const i = source.indexOf('[]');
	if (i < 0) return `{{ ${source} }}`;
	const list = source.slice(0, i);
	const rest = source.slice(i + 2);
	if (target.type === 'array' && rest && !rest.slice(1).includes('[]')) return `{{ map(${list}, ${rest}) }}`;
	if (target.type === 'array' && !rest) return `{{ ${list} }}`;
	return `{{ ${source.replaceAll('[]', '[0]')} }}`;
}

/** The nearest of a path and its ancestors that has a handle (a collapsed parent stands in for its fields). */
export function visibleHandle(path: string, handles: Set<string>): string | null {
	let p = path;
	while (p) {
		if (handles.has(p)) return p;
		const cut = Math.max(p.lastIndexOf('.'), p.lastIndexOf('[]'));
		if (cut <= 0) return null;
		p = p.slice(0, cut);
	}
	return null;
}

/** Wrap a template's single expression in a function, or start one: upper(input.name). */
export function wrap(template: string, fn: (expr: string) => string): string {
	const m = /^\s*\{\{\s*([\s\S]+?)\s*\}\}\s*$/.exec(template);
	if (m) return `{{ ${fn(m[1])} }}`;
	if (!template.trim()) return `{{ ${fn('…')} }}`;
	return `{{ ${fn(JSON.stringify(template).replaceAll('"', "'"))} }}`;
}

/** Read a dotted path from a built argument: values.Name. */
export function valueAt(arg: unknown, path: string): unknown {
	let v = arg;
	for (const k of path.split('.')) v = v && typeof v === 'object' ? (v as Record<string, unknown>)[k] : undefined;
	return v;
}

/** The field at a path among fields: customer.email, or lines[].sku inside a list. Mirrors flow.FindField. */
export function findField(fields: ConnectorField[], path: string): ConnectorField | undefined {
	const [head, ...rest] = path.split('.');
	const element = head.endsWith('[]');
	const name = element ? head.slice(0, -2) : head;
	const f = fields.find((x) => x.name === name);
	if (!f) return undefined;
	if (element) {
		if (f.type !== 'array') return undefined;
		if (!rest.length) return { name: `${name}[]`, type: (f.items as ConnectorField['type']) || 'any' };
		return findField(f.fields ?? [], rest.join('.'));
	}
	if (!rest.length) return f;
	if (f.type !== 'object') return undefined;
	const sub = findField(f.fields ?? [], rest.join('.'));
	if (sub) return sub;
	return f.open && rest.length === 1 ? { name: rest[0], type: '' } : undefined;
}

/** The list a path is inside, if any: lines for lines[].sku. */
export const listOf = (path: string): string | null => (path.includes('[]') ? path.slice(0, path.indexOf('[]')) : null);

/** A Transform's required output fields with nothing mapped. Mirrors flow's missingMapped. */
export function missingMapped(c: TransformConfig): string[] {
	const mapping = c.mapping ?? {};
	return (c.fields ?? [])
		.filter((f) => f.required && !c.lists?.[f.name])
		.filter((f) => !Object.entries(mapping).some(([k, v]) => (k === f.name || k.startsWith(f.name + '.')) && v.trim()))
		.map((f) => f.name);
}

/** A reference inside a list mapping, as a source path: it.Name in lines (from steps.q.records) is steps.q.records[].Name. */
export function resolveElement(ref: string, list: ListMapping | undefined): string | null {
	if (ref !== 'it' && !ref.startsWith('it.')) return ref === 'itIndex' ? null : ref;
	if (!list) return null;
	const from = directReference(`{{ ${list.from} }}`);
	if (!from) return null;
	return `${from}[]${ref.slice(2)}`;
}

/** Output fields and a mapping from a JSON template: the switch from template to mapper. */
export function fromTemplate(template: string): { fields: ConnectorField[]; mapping: Record<string, string> } | null {
	let tree: unknown;
	try {
		tree = JSON.parse(template);
	} catch {
		return null;
	}
	if (!tree || typeof tree !== 'object' || Array.isArray(tree)) return null;
	const mapping: Record<string, string> = {};
	const walk = (obj: Record<string, unknown>, prefix: string): ConnectorField[] =>
		Object.entries(obj).map(([name, v]) => {
			const path = prefix + name;
			if (v && typeof v === 'object' && !Array.isArray(v)) return { name, type: 'object', fields: walk(v as Record<string, unknown>, path + '.') };
			if (typeof v === 'string') {
				mapping[path] = v;
				// A lone reference keeps its value's type; text with placeholders is text.
				return { name, type: directReference(v) || /^\s*\{\{[^}]*\}\}\s*$/.test(v) ? 'any' : 'string' };
			}
			mapping[path] = JSON.stringify(v);
			return { name, type: typeof v === 'number' ? 'number' : typeof v === 'boolean' ? 'boolean' : 'json' };
		});
	return { fields: walk(tree as Record<string, unknown>, ''), mapping };
}

/** A JSON template from mapped fields, for the switch back. Lists can't be written this way and are dropped. */
export function toTemplate(c: TransformConfig): string {
	const out: Record<string, unknown> = {};
	for (const [path, v] of Object.entries(c.mapping ?? {})) {
		if (path.includes('[]') || !v.trim()) continue;
		const keys = path.split('.');
		let m = out;
		for (const k of keys.slice(0, -1)) m = (m[k] ??= {}) as Record<string, unknown>;
		m[keys.at(-1)!] = v;
	}
	return JSON.stringify(out, null, 2);
}
