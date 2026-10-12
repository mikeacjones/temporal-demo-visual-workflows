import { SYSTEM_ACTIVITY_DEFAULTS, type ActivityOptions, type RetryConfig } from './types';
import { humanDuration } from './duration';

/** Fills absent/zero fields of o from parent, like flow.ActivityOptions.Resolve. */
export function resolveActivity(o: ActivityOptions | undefined, parent: ActivityOptions): ActivityOptions {
	const r: RetryConfig = { ...(o?.retry ?? {}) };
	const p = parent.retry ?? {};
	return {
		startToCloseSeconds: o?.startToCloseSeconds || parent.startToCloseSeconds,
		scheduleToCloseSeconds: o?.scheduleToCloseSeconds || parent.scheduleToCloseSeconds,
		retry: {
			maxAttempts: r.maxAttempts || p.maxAttempts,
			initialIntervalSeconds: r.initialIntervalSeconds || p.initialIntervalSeconds,
			backoffCoefficient: r.backoffCoefficient || p.backoffCoefficient,
			maximumIntervalSeconds: r.maximumIntervalSeconds || p.maximumIntervalSeconds,
			nonRetryableErrorTypes: r.nonRetryableErrorTypes?.length ? r.nonRetryableErrorTypes : p.nonRetryableErrorTypes,
			nonRetryableStatusCodes: r.nonRetryableStatusCodes?.length ? r.nonRetryableStatusCodes : p.nonRetryableStatusCodes
		}
	};
}

/** The flow's defaults resolved against the system defaults. */
export const flowDefaults = (flowActivity: ActivityOptions | undefined) =>
	resolveActivity(flowActivity, SYSTEM_ACTIVITY_DEFAULTS);

export interface RetryPreview {
	/** Waits before each retry, in seconds. */
	waits: number[];
	/** Summary like "1s, 2s, 4s, 8s … gives up after ~15s". */
	text: string;
}

/**
 * Projects when Temporal would retry a failing Activity under this policy.
 * Ignores the time each attempt itself takes, so totals are lower bounds.
 */
export function retryPreview(o: ActivityOptions): RetryPreview {
	const r = o.retry;
	const initial = r.initialIntervalSeconds || 1;
	const coeff = r.backoffCoefficient || 2;
	const max = r.maximumIntervalSeconds || initial * 100;
	const attempts = r.maxAttempts || 0; // 0 = unlimited
	const waits: number[] = [];
	const shown = attempts ? Math.min(attempts - 1, 8) : 8;
	let w = initial;
	for (let i = 0; i < shown; i++) {
		waits.push(Math.min(w, max));
		w *= coeff;
	}
	const list = waits.map((x) => humanDuration(x)).join(', ');
	if (!attempts) {
		const capped = o.scheduleToCloseSeconds ? `gives up after ${humanDuration(o.scheduleToCloseSeconds)} total` : 'retries forever';
		return { waits, text: `${list} … then every ${humanDuration(max)}; ${capped}` };
	}
	if (attempts === 1) return { waits, text: 'No retries: one attempt' };
	let total = 0;
	w = initial;
	for (let i = 0; i < attempts - 1; i++) {
		total += Math.min(w, max);
		w *= coeff;
	}
	if (o.scheduleToCloseSeconds) total = Math.min(total, o.scheduleToCloseSeconds);
	const more = attempts - 1 > waits.length ? ' …' : '';
	return { waits, text: `${list}${more} · gives up after ${attempts} attempts (~${humanDuration(total)} of waiting)` };
}
