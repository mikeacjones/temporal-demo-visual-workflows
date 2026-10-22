<script lang="ts">
	import { untrack } from 'svelte';
	import { SvelteFlowProvider, type Edge } from '@xyflow/svelte';
	import X from '@lucide/svelte/icons/x';
	import Trash2 from '@lucide/svelte/icons/trash-2';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Switch } from '$lib/components/ui/switch';
	import { Textarea } from '$lib/components/ui/textarea';
	import Choice from '../Choice.svelte';
	import MapperCanvas from './MapperCanvas.svelte';
	import type { Row, SchemaData } from './SchemaNode.svelte';
	import { api } from '$lib/api';
	import { app } from '$lib/state.svelte';
	import { connectorSpec, missingInputs, resolveField, typeLabel } from '$lib/connectors';
	import { sampleEnv, sourceAccent, sourceRoots, type SampleEnv } from '$lib/datasources';
	import { directReference, findField, isConstant, listOf, missingMapped, references, resolveElement, templateFor, visibleHandle, wrap } from '$lib/mapping';
	import { isConnector, type ConnectorConfig, type ConnectorField, type ConnectorType, type FieldType, type FlowNode, type ListMapping, type RunState, type TransformConfig } from '$lib/types';

	// The visual mapper. For a connector step it maps the operation's inputs;
	// for a Transform it maps an output you define, with lists mapped element
	// by element. Either way: the data the step can see on the left, the target
	// on the right, a line per mapping, and previews computed by the app with
	// the Workflow's own code against sample data.
	let { node, close }: { node: FlowNode; close: () => void } = $props();

	const connector = $derived(isConnector(node.type));
	const cc = $derived(node.config as ConnectorConfig);
	const tc = $derived(node.config as TransformConfig);
	const spec = $derived(connector ? connectorSpec(node.type as ConnectorType) : undefined);
	const op = $derived(spec?.operations.find((o) => o.name === cc.operation));
	const accent = $derived(connector ? `var(--${node.type})` : 'var(--indigo-200)');
	// A Transform's output is yours to shape: add, retype, rename and remove fields.
	const editable = $derived(!connector);
	const mapping = $derived((node.config as { mapping?: Record<string, string> }).mapping ?? {});
	const lists = $derived<Record<string, ListMapping>>(connector ? {} : (tc.lists ?? {}));
	const topFields = $derived<ConnectorField[]>(connector ? (op?.inputs ?? []).map((f) => (spec ? resolveField(spec, f, cc.settings) : f)) : (tc.fields ?? []));

	// Sample data: the latest run of this flow if it has one, else the
	// trigger's sample input with placeholders for the other steps.
	let run = $state<RunState | null>(null);
	let sampleFrom = $state("the trigger's sample input, with placeholder values for other steps");
	$effect(() => {
		const flowId = untrack(() => app.flow?.id);
		if (!flowId) return;
		api.listRuns(flowId)
			.then(async (runs) => {
				const latest = runs?.[0];
				if (!latest) return;
				run = await api.runDetails(latest.workflowId);
				sampleFrom = `the latest run, ${latest.workflowId.split('-').slice(-2).join('-')}`;
			})
			.catch(() => {});
	});
	const sample = $derived<SampleEnv>(app.flow ? sampleEnv(app.flow, node, run) : {});
	const roots = $derived(app.flow ? sourceRoots(app.flow, node, sample) : []);

	// Expanded and collapsed fields. Each source starts open to its own
	// fields, with nested objects closed, except the run itself; the target
	// starts open.
	let srcOpen = $state<Record<string, boolean>>({});
	let dstOpen = $state<Record<string, boolean>>({});
	const srcIsOpen = (path: string, depth: number) => srcOpen[path] ?? (depth < 1 && path !== 'run');
	const dstIsOpen = (path: string) => dstOpen[path] ?? true;

	let selected = $state<string | null>(null);

	// Previews, refreshed as the mapping changes.
	let preview = $state<{ argument: Record<string, unknown> | null; errors: Record<string, string> }>({ argument: null, errors: {} });
	let timer: ReturnType<typeof setTimeout> | undefined;
	$effect(() => {
		const body = JSON.stringify({ config: node.config, sample });
		clearTimeout(timer);
		timer = setTimeout(() => {
			const { config, sample: env } = JSON.parse(body);
			const req = connector
				? api.previewConnector(node.type, config, env)
				: api.previewTransform({ fields: config.fields, mapping: config.mapping, lists: config.lists }, env);
			req.then((p) => (preview = p)).catch(() => {});
		}, 250);
	});

	/** A sample value at a target path; inside a list, its first element's. */
	function sampleAt(arg: unknown, path: string): unknown {
		let v = arg;
		for (const seg of path.split('.')) {
			const element = seg.endsWith('[]');
			v = v && typeof v === 'object' ? (v as Record<string, unknown>)[element ? seg.slice(0, -2) : seg] : undefined;
			if (element) v = Array.isArray(v) ? v[0] : undefined;
		}
		return v;
	}

	/** A sample value small enough for a row: lists as a count, objects folded. */
	function short(v: unknown): string | undefined {
		if (v === undefined) return undefined;
		if (Array.isArray(v)) return `${v.length} ${v.length === 1 ? 'item' : 'items'}`;
		if (v && typeof v === 'object') return '{…}';
		return typeof v === 'string' ? v : JSON.stringify(v);
	}

	/** A source field by its mapper path: steps.query.records[].Name. */
	function sourceField(path: string): ConnectorField | undefined {
		const root = roots.find((r) => path === r.path || path.startsWith(r.path + '.') || path.startsWith(r.path + '['));
		if (!root) return undefined;
		let f: ConnectorField | undefined = root.field;
		for (const m of path.slice(root.path.length).matchAll(/\.([A-Za-z_]\w*)|\[\]/g)) {
			if (!f) return undefined;
			if (m[0] === '[]') continue;
			f = f.fields?.find((x) => x.name === m[1]);
		}
		return f;
	}

	function sourceRows(): Row[] {
		const rows: Row[] = [];
		const walk = (fields: ConnectorField[], base: string, depth: number, tint: string) => {
			for (const f of fields) {
				const path = `${base}.${f.name}`;
				const kids = f.fields ?? [];
				const open = srcIsOpen(path, depth);
				rows.push({ path, depth, name: f.type === 'array' ? `${f.name}[]` : f.name, type: typeLabel(f), accent: tint, description: f.description, expandable: kids.length > 0, expanded: open });
				if (kids.length && open) walk(kids, f.type === 'array' ? `${path}[]` : path, depth + 1, tint);
			}
		};
		for (const r of roots) {
			const tint = sourceAccent(r.type);
			const kids = r.field.fields ?? [];
			const open = srcIsOpen(r.path, 0);
			rows.push({ path: r.path, depth: 0, title: r.label, name: r.path, type: typeLabel(r.field), accent: tint, expandable: kids.length > 0, expanded: open });
			if (open) walk(kids, r.path, 1, tint);
		}
		return rows;
	}

	function targetRows(): Row[] {
		const rows: Row[] = [];
		const row = (f: ConnectorField, path: string, depth: number, custom = false): Row => {
			const template = (mapping[path] ?? '').trim() ? mapping[path] : '';
			const state = !template ? (lists[path] ? 'direct' : '') : directReference(template) ? 'direct' : isConstant(template) ? 'constant' : 'expression';
			const v = template || lists[path] ? sampleAt(preview.argument, path) : undefined;
			return {
				path, depth, name: f.name, type: custom ? 'any' : typeLabel(f), required: f.required, description: f.description, custom,
				state, template, list: lists[path]?.from, error: preview.errors[path],
				preview: short(v)
			};
		};
		const walk = (fields: ConnectorField[], prefix: string, depth: number) => {
			let ordered = fields.filter((x) => !x.readOnly);
			// A connector's catalog object lists its required fields first; a
			// Transform's output keeps the order you gave it. Rows never move as
			// they're mapped, so a drag never lands on a row that just moved.
			if (connector && depth > 0) ordered = [...ordered].sort((a, b) => Number(!!b.required) - Number(!!a.required));
			for (const f of ordered) {
				const path = prefix + f.name;
				if (f.type === 'object') {
					const open = dstIsOpen(path);
					rows.push({ ...row(f, path, depth), expandable: true, expanded: open });
					if (!open) continue;
					walk(f.fields ?? [], path + '.', depth + 1);
					for (const k of Object.keys(mapping)) {
						const name = k.slice(path.length + 1);
						if (k.startsWith(path + '.') && !name.includes('.') && !(f.fields ?? []).some((x) => x.name === name)) rows.push(row({ name, type: '' }, k, depth + 1, true));
					}
					if (f.open || editable) rows.push({ path, depth: depth + 1, name: '', type: '', add: true });
				} else if (f.type === 'array' && editable && !f.items) {
					// A list of objects: its element's fields, mapped from `it`.
					const open = dstIsOpen(path);
					rows.push({ ...row(f, path, depth), name: `${f.name}[]`, expandable: true, expanded: open });
					if (!open) continue;
					walk(f.fields ?? [], path + '[].', depth + 1);
					rows.push({ path: path + '[]', depth: depth + 1, name: '', type: '', add: true });
				} else if (f.type === 'array' && lists[path]) {
					rows.push({ ...row(f, path, depth), name: `${f.name}[]` });
					rows.push(row({ name: 'each element', type: (f.items as FieldType) || 'any', description: 'Map each element from it; empty keeps the element as it is.' }, path + '[]', depth + 1));
				} else {
					rows.push(row(f, path, depth));
				}
			}
		};
		if (editable) {
			rows.push({ path: '$root', depth: 0, title: 'Output', name: `steps.${node.key}`, type: 'object', root: true, description: 'Drop a whole object here to copy its fields and map each one.' });
			walk(topFields, '', 1);
			rows.push({ path: '', depth: 1, name: '', type: '', add: true });
		} else {
			walk(topFields, '', 0);
		}
		return rows;
	}

	const srcRows = $derived(sourceRows());
	const dstRows = $derived(targetRows());
	const missing = $derived(connector ? (spec && op ? missingInputs(spec, op, cc) : []) : missingMapped(tc));

	const edges = $derived.by<Edge[]>(() => {
		const srcHandles = new Set(srcRows.map((r) => r.path));
		const dstHandles = new Set(dstRows.filter((r) => !r.add).map((r) => r.path));
		const tint = new Map(srcRows.map((r) => [r.path, r.accent]));
		const out: Edge[] = [];
		const line = (fromPath: string | null, toPath: string, cls: string, key: string) => {
			const from = fromPath && visibleHandle(fromPath, srcHandles);
			const to = visibleHandle(toPath, dstHandles);
			if (!from || !to || out.some((e) => e.id === `${from}=>${key}`)) return;
			out.push({ id: `${from}=>${key}`, source: 'src', sourceHandle: from, target: 'dst', targetHandle: to, class: cls, style: `stroke: ${tint.get(from) ?? accent}`, selected: selected === key });
		};
		for (const [path, template] of Object.entries(mapping)) {
			if (!template.trim()) continue;
			const list = lists[listOf(path) ?? ''];
			const cls = directReference(template) ? 'map-direct' : 'map-expression';
			for (const ref of references(template)) line(resolveElement(ref, list), path, cls, path);
		}
		for (const [path, l] of Object.entries(lists)) line(directReference(`{{ ${l.from} }}`), path, 'map-list', path);
		return out;
	});

	// Editing.
	function setMapping(path: string, template: string) {
		const cfg = node.config as { mapping?: Record<string, string> };
		cfg.mapping ??= {};
		if (template) cfg.mapping[path] = template;
		else delete cfg.mapping[path];
	}
	const targetField = (path: string) => findField(topFields, path);

	function connect(from: string, to: string) {
		if (to === '$root') return autoMap(from);
		const f = targetField(to);
		if (!f) return;
		const src = sourceField(from);
		// A list onto a list field: element by element.
		if (editable && f.type === 'array' && src?.type === 'array' && !from.includes('[]')) {
			tc.lists ??= {};
			tc.lists[to] = { from };
			setMapping(to, '');
			dstOpen[to] = true;
			selected = to;
			return;
		}
		// A field of the list's element onto a field of the target's element: from `it`.
		const list = listOf(to);
		const base = list && lists[list] ? directReference(`{{ ${lists[list].from} }}`) : null;
		if (base && from.startsWith(base + '[]')) setMapping(to, `{{ it${from.slice(base.length + 2)} }}`);
		else setMapping(to, templateFor(from, f));
		selected = to;
	}

	/** Copy a whole source object's fields into a Transform's output, each mapped from its source. */
	function autoMap(from: string) {
		const src = sourceField(from);
		if (!src) return;
		tc.fields ??= [];
		tc.lists ??= {};
		const copy = (f: ConnectorField, fromPath: string, toPrefix: string, into: ConnectorField[], inList: boolean) => {
			if (into.some((x) => x.name === f.name)) return;
			const at = toPrefix + f.name;
			const ref = inList ? `it${fromPath}` : fromPath;
			if (f.type === 'object' && f.fields?.length) {
				const obj: ConnectorField = { name: f.name, type: 'object', fields: [] };
				into.push(obj);
				for (const sub of f.fields) copy(sub, `${fromPath}.${sub.name}`, at + '.', obj.fields!, inList);
			} else if (f.type === 'array' && f.fields?.length && !inList) {
				const arr: ConnectorField = { name: f.name, type: 'array', fields: [] };
				into.push(arr);
				tc.lists![at] = { from: fromPath };
				for (const sub of f.fields) copy(sub, `.${sub.name}`, at + '[].', arr.fields!, true);
			} else {
				const scalar = f.type && f.type !== 'object' && f.type !== 'array' && f.type !== 'json';
				into.push({ name: f.name, type: scalar ? f.type : 'any' });
				setMapping(at, `{{ ${ref} }}`);
			}
		};
		const name = from.split(/[.[\]]+/).filter(Boolean).at(-1) ?? 'value';
		if (src.type === 'object' && src.fields?.length) for (const sub of src.fields) copy(sub, `${from}.${sub.name}`, '', tc.fields, false);
		else copy({ ...src, name }, from, '', tc.fields, false);
	}

	/** The fields an add row adds to: the output's, an object's, or a list element's. */
	function fieldsAt(parent: string): ConnectorField[] | undefined {
		if (!parent) return (tc.fields ??= []);
		const f = findField(tc.fields ?? [], parent.endsWith('[]') ? parent.slice(0, -2) : parent);
		if (!f) return undefined;
		return (f.fields ??= []);
	}

	function addField(parent: string, name: string) {
		if (connector) {
			setMapping(`${parent}.${name}`, ' ');
			selected = `${parent}.${name}`;
			return;
		}
		const into = fieldsAt(parent);
		if (!into || into.some((x) => x.name === name) || !/^[A-Za-z_]\w*$/.test(name)) return;
		into.push({ name, type: 'string' });
		selected = parent ? `${parent}.${name}` : name;
	}

	/** Remove a field and everything mapped at or under it. */
	function removeField(path: string) {
		if (connector) {
			setMapping(path, '');
		} else {
			const cut = path.lastIndexOf('.');
			const into = fieldsAt(cut < 0 ? '' : path.slice(0, cut));
			const name = path.slice(cut + 1);
			if (into) into.splice(into.findIndex((x) => x.name === name), 1);
			for (const k of Object.keys(mapping)) if (k === path || k.startsWith(path + '.') || k.startsWith(path + '[]')) setMapping(k, '');
			for (const k of Object.keys(tc.lists ?? {})) if (k === path || k.startsWith(path + '.')) delete tc.lists![k];
		}
		if (selected === path) selected = null;
	}

	/** Rename a Transform output field, carrying its mappings along. */
	function rename(path: string, name: string) {
		const f = targetField(path);
		if (!f || !/^[A-Za-z_]\w*$/.test(name) || name === f.name) return;
		const cut = path.lastIndexOf('.');
		const next = (cut < 0 ? '' : path.slice(0, cut + 1)) + name;
		if (targetField(next)) return;
		f.name = name;
		for (const [k, v] of Object.entries(mapping)) {
			if (k === path || k.startsWith(path + '.') || k.startsWith(path + '[]')) {
				setMapping(k, '');
				setMapping(next + k.slice(path.length), v);
			}
		}
		for (const [k, l] of Object.entries(tc.lists ?? {})) {
			if (k === path || k.startsWith(path + '.')) {
				delete tc.lists![k];
				tc.lists![next + k.slice(path.length)] = l;
			}
		}
		selected = next;
	}

	function setType(f: ConnectorField, type: string) {
		f.type = type as FieldType;
		if (type === 'object' || type === 'array') f.fields ??= [];
		if (type !== 'object' && type !== 'array') delete f.fields;
		if (type !== 'array') delete f.items;
	}

	const source: SchemaData = $derived({
		side: 'source',
		title: 'Available data',
		subtitle: 'Drag a field onto the target',
		accent: 'var(--line)',
		rows: srcRows,
		selected: null,
		toggle: (p) => (srcOpen[p] = !srcIsOpen(p, srcRows.find((r) => r.path === p)?.depth ?? 0)),
		select: () => {},
		addField: () => {},
		addLabel: '',
		removeField: () => {}
	});
	const target: SchemaData = $derived({
		side: 'target',
		title: connector ? `${spec?.label} · ${op?.label}` : `${node.label} · output`,
		subtitle: missing.length ? `${missing.length} required to map: ${missing.join(', ')}` : connector ? 'All required inputs mapped' : `${topFields.length} output fields`,
		accent,
		rows: dstRows,
		selected,
		toggle: (p) => (dstOpen[p] = !dstIsOpen(p)),
		select: (p) => (selected = p === '$root' ? null : p),
		addField,
		addLabel: connector ? 'Custom field' : 'Output field',
		removeField
	});

	const field = $derived(selected ? targetField(selected) : undefined);
	const template = $derived(selected ? (mapping[selected] ?? '') : '');
	const listMapping = $derived(selected ? lists[selected] : undefined);
	const result = $derived(selected ? sampleAt(preview.argument, selected) : undefined);
	const custom = $derived(connector && !!selected?.includes('.') && field?.type === '');
	const elementField = $derived(!!selected?.endsWith('[]'));
	const objectList = $derived(!!field && field.type === 'array' && editable && !field.items);
	const choices = $derived(field?.enum?.map((v, i) => ({ value: v, label: field.enumLabels?.[i] ?? v })) ?? field?.suggestions?.map((v) => ({ value: v, label: v })) ?? []);
	const types = ['string', 'number', 'integer', 'boolean', 'date', 'datetime', 'object', 'array', 'any'].map((t) => ({ value: t, label: t === 'array' ? 'list' : t }));
	const functions: { label: string; apply: (e: string) => string; tip: string }[] = [
		{ label: 'upper', apply: (e) => `upper(${e})`, tip: 'Upper case' },
		{ label: 'lower', apply: (e) => `lower(${e})`, tip: 'Lower case' },
		{ label: 'trim', apply: (e) => `trim(${e})`, tip: 'Trim spaces' },
		{ label: 'default', apply: (e) => `${e} ?? ''`, tip: 'A value when it is missing' },
		{ label: 'join', apply: (e) => `join(${e}, ', ')`, tip: 'A list as text' },
		{ label: 'date', apply: (e) => `date(${e}).Format('2006-01-02')`, tip: 'Format as YYYY-MM-DD' },
		{ label: '× 100', apply: (e) => `${e} * 100`, tip: 'To cents' },
		{ label: 'round', apply: (e) => `round(${e})`, tip: 'Round a number' },
		{ label: 'len', apply: (e) => `len(${e})`, tip: 'How many' },
		{ label: 'sum', apply: (e) => `sum(${e})`, tip: 'Add up a list of numbers' }
	];
	let nameDraft = $state('');
	$effect(() => {
		nameDraft = field?.name ?? '';
	});

	function setList(path: string, change: Partial<ListMapping>) {
		tc.lists ??= {};
		const prev = tc.lists[path];
		const from = 'from' in change ? (change.from ?? '') : (prev?.from ?? '');
		const where = 'where' in change ? change.where : prev?.where;
		if (!from) delete tc.lists[path];
		else tc.lists[path] = { from, ...(where ? { where } : {}) };
	}

	function portal(el: HTMLElement) {
		document.body.appendChild(el);
		return { destroy: () => el.remove() };
	}
</script>

<svelte:window onkeydown={(e) => e.key === 'Escape' && close()} />

{#snippet hint(s: string)}
	<p class="text-[0.64rem] leading-snug text-[var(--slate-500)]">{s}</p>
{/snippet}

<div use:portal class="fixed inset-0 z-50 flex flex-col bg-[#0c0d12]/95 backdrop-blur-sm" role="dialog" aria-modal="true" aria-label="Mapper">
	<header class="flex items-center gap-3 border-b border-[var(--line)] px-5 py-3">
		<div class="min-w-0">
			<div class="eyebrow" style:color={accent}>{connector ? 'Map inputs' : 'Map output'} · {node.label}</div>
			<div class="mono text-[0.64rem] text-[var(--slate-300)]">Previews use {sampleFrom}. The Workflow builds the same values, deterministically, when the step runs.</div>
		</div>
		<Button size="sm" class="btn-uv mono ml-auto" onclick={close}>Done</Button>
		<button class="text-[var(--slate-300)] hover:text-[var(--off-white)]" onclick={close} aria-label="Close the mapper"><X class="size-4" /></button>
	</header>
	<div class="flex min-h-0 flex-1">
		<div class="min-w-0 flex-1">
			<SvelteFlowProvider>
				<MapperCanvas {source} {target} {edges} {connect} clear={(to) => (lists[to] ? setList(to, { from: '' }) : setMapping(to, ''))} selectEdge={(to) => (selected = to)} />
			</SvelteFlowProvider>
		</div>
		<aside class="w-[360px] shrink-0 space-y-3 overflow-y-auto border-l border-[var(--line)] bg-[#111218] p-4">
			{#if selected && field}
				{#if editable && !elementField}
					<div class="space-y-2">
						<span class="eyebrow !text-[0.6rem]">Output field</span>
						<Input class="mono h-8 text-xs" bind:value={nameDraft} onblur={() => rename(selected!, nameDraft)} onkeydown={(e) => e.key === 'Enter' && rename(selected!, nameDraft)} />
						<div class="grid grid-cols-[1fr_auto] items-center gap-2">
							<Choice bind:value={() => field.type || 'any', (v) => setType(field, v)} options={types} />
							<label class="flex items-center gap-1.5 text-xs"><Switch bind:checked={() => !!field.required, (v) => (field.required = v || undefined)} /> required</label>
						</div>
						{#if field.type === 'array'}
							<Choice
								bind:value={() => field.items ?? '', (v) => (v ? ((field.items = v), delete field.fields) : ((field.items = undefined), (field.fields ??= [])))}
								options={[{ value: '', label: 'of objects' }, ...['string', 'number', 'integer', 'boolean', 'any'].map((t) => ({ value: t, label: `of ${t}` }))]}
							/>
						{/if}
					</div>
				{:else}
					<div>
						<div class="flex items-center gap-1.5">
							<span class="mono text-sm">{selected}</span>
							{#if field.required}<span class="text-[var(--red)]">*</span>{/if}
							<span class="mono ml-auto text-[0.62rem] text-[var(--slate-500)]">{typeLabel(field)}</span>
						</div>
						{#if field.description}<p class="mt-1 text-[0.68rem] text-[var(--slate-300)]">{field.description}</p>{/if}
					</div>
				{/if}

				{#if field.type === 'array' && editable}
					<div class="space-y-1.5 border border-[var(--line)] p-2.5">
						<span class="eyebrow !text-[0.6rem]">Each element of</span>
						<Input class="mono h-8 text-xs" placeholder="steps.query.records" bind:value={() => listMapping?.from ?? '', (v) => setList(selected!, { from: v })} />
						<Input class="mono h-8 text-xs" placeholder="only where, e.g. it.Amount > 1000" bind:value={() => listMapping?.where ?? '', (v) => setList(selected!, { where: v })} />
						{@render hint('Drag a list onto this field, or name one here. Each element becomes one element of this list: map its fields from it (it.Name), and itIndex is its position.')}
					</div>
				{/if}

				{#if field.type !== 'object' && !objectList && !listMapping}
					<div class="space-y-1">
						<span class="eyebrow !text-[0.6rem]">{elementField ? 'Each element' : 'Template'}</span>
						<Textarea
							class="mono min-h-20 text-xs"
							bind:value={() => (template.trim() ? template : ''), (v) => setMapping(selected!, v || (custom ? ' ' : ''))}
							spellcheck={false}
							placeholder={listOf(selected) ? '{{ it.Name }}' : '{{ input.company }}, a fixed value, or text with {{ placeholders }}'}
						/>
					</div>
					{#if choices.length}
						<div class="space-y-1">
							<span class="eyebrow !text-[0.6rem]">{field.enum ? 'Values' : 'Suggestions'}</span>
							<div class="flex flex-wrap gap-1">
								{#each choices as ch (ch.value)}
									<button
										class="mono border border-dashed px-1.5 py-0.5 text-[0.6rem] text-[var(--slate-300)] hover:border-[var(--uv-light)]"
										style:border-color={template === ch.value ? accent : 'var(--line)'}
										onclick={() => setMapping(selected!, ch.value)}>{ch.label}</button
									>
								{/each}
							</div>
						</div>
					{/if}
					<div class="space-y-1">
						<span class="eyebrow !text-[0.6rem]">Functions</span>
						<div class="flex flex-wrap gap-1">
							{#each functions as fn (fn.label)}
								<button
									class="mono border border-[var(--line)] px-1.5 py-0.5 text-[0.6rem] text-[var(--purple)] hover:border-[var(--uv-light)]"
									data-tip={fn.tip}
									onclick={() => setMapping(selected!, wrap(template, fn.apply))}>{fn.label}</button
								>
							{/each}
						</div>
						{@render hint('Expressions are expr-lang, run in the Workflow with the clock disabled, so replays always agree.')}
					</div>
				{/if}

				<div class="space-y-1">
					<span class="eyebrow !text-[0.6rem]">Sample result{listOf(selected) ? ' (first element)' : ''}</span>
					{#if preview.errors[selected]}
						<p class="mono border border-[var(--red)]/60 bg-[var(--red)]/10 p-2 text-[0.66rem] text-[var(--red)]">{preview.errors[selected]}</p>
					{:else if result !== undefined}
						<pre class="mono max-h-48 overflow-auto border border-[var(--line)] bg-[#0c0d12] p-2 text-[0.66rem] text-[var(--green)]">{JSON.stringify(result, null, 2)}</pre>
					{:else}
						<p class="text-[0.66rem] text-[var(--slate-500)]">Nothing mapped yet.</p>
					{/if}
				</div>
				<div class="flex gap-2">
					{#if template || listMapping}
						<Button variant="outline" size="xs" class="mono" onclick={() => (listMapping ? setList(selected!, { from: '' }) : setMapping(selected!, ''))}>Clear mapping</Button>
					{/if}
					{#if (editable && !elementField) || custom}
						<Button variant="outline" size="xs" class="mono text-[var(--red)]" onclick={() => removeField(selected!)}><Trash2 /> Remove field</Button>
					{/if}
				</div>
			{:else}
				<span class="eyebrow !text-[0.6rem]">How to map</span>
				<ul class="list-disc space-y-1.5 pl-4 text-[0.72rem] text-[var(--slate-300)]">
					{#if editable}
						<li>Add fields with the <b>Output field</b> rows, or drop a whole object from <b>Available data</b> onto <b>Output</b> to copy its fields, each mapped.</li>
						<li>Drag a list onto a list field to build it element by element, then drag the element's fields onto the list's fields. Inside, <span class="mono">it</span> is the element.</li>
					{/if}
					<li>Drag a field from <b>Available data</b> onto {editable ? 'a field' : 'an input'}. A solid line is a direct mapping.</li>
					<li>Click {editable ? 'a field' : 'an input'} to write a template: a fixed value, text with <span class="mono">{'{{ placeholders }}'}</span>, or an expression. Expressions draw a dashed line.</li>
					<li>Select a line and press Delete to clear that mapping.</li>
				</ul>
				{#if missing.length}
					<div class="border border-[var(--red)]/60 bg-[var(--red)]/10 p-2 text-[0.7rem] text-[var(--red)]">Required to map: {missing.join(', ')}</div>
				{/if}
				{#if preview.argument}
					<span class="eyebrow !text-[0.6rem]">{connector ? "The Activity's input" : 'The output'}</span>
					<pre class="mono max-h-80 overflow-auto border border-[var(--line)] bg-[#0c0d12] p-2 text-[0.66rem] text-[var(--green)]">{JSON.stringify(preview.argument, null, 2)}</pre>
				{/if}
			{/if}
		</aside>
	</div>
</div>
