import type { TimeUnit } from './types';

export const UNITS: { value: TimeUnit; label: string; seconds: number }[] = [
	{ value: 'seconds', label: 'seconds', seconds: 1 },
	{ value: 'minutes', label: 'minutes', seconds: 60 },
	{ value: 'hours', label: 'hours', seconds: 3600 },
	{ value: 'days', label: 'days', seconds: 86400 }
];

/** Picks the largest unit that divides the value evenly, for amount+unit pickers. */
export function splitSeconds(total: number): { amount: number; unit: TimeUnit } {
	for (const u of [...UNITS].reverse()) {
		if (total >= u.seconds && total % u.seconds === 0) return { amount: total / u.seconds, unit: u.value };
	}
	return { amount: total, unit: 'seconds' };
}

export const unitSeconds = (unit: TimeUnit | undefined) => UNITS.find((u) => u.value === unit)?.seconds ?? 1;

/** "1h 30m", "45s", "2d". */
export function humanDuration(seconds: number): string {
	if (!isFinite(seconds)) return 'never';
	if (seconds < 1) return `${Math.round(seconds * 1000)}ms`;
	const parts: string[] = [];
	let s = Math.round(seconds);
	for (const [label, size] of [
		['d', 86400],
		['h', 3600],
		['m', 60],
		['s', 1]
	] as const) {
		if (s >= size) {
			parts.push(`${Math.floor(s / size)}${label}`);
			s %= size;
		}
		if (parts.length === 2) break;
	}
	return parts.join(' ') || '0s';
}
