import type { ScheduleConfig, TriggerConfig } from './types';

export const WEEKDAYS = ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun'] as const;
const DAY_NAMES: Record<string, string> = { mon: 'Mon', tue: 'Tue', wed: 'Wed', thu: 'Thu', fri: 'Fri', sat: 'Sat', sun: 'Sun' };

export const TIMEZONES = [
	'UTC',
	'America/Los_Angeles',
	'America/Denver',
	'America/Chicago',
	'America/New_York',
	'America/Sao_Paulo',
	'Europe/London',
	'Europe/Berlin',
	'Europe/Paris',
	'Africa/Johannesburg',
	'Asia/Dubai',
	'Asia/Kolkata',
	'Asia/Singapore',
	'Asia/Tokyo',
	'Australia/Sydney'
];

export const OVERLAP_OPTIONS = [
	{ value: 'skip', label: 'Skip if still running' },
	{ value: 'buffer_one', label: 'Queue one' },
	{ value: 'buffer_all', label: 'Queue all' },
	{ value: 'cancel_other', label: 'Cancel the running one' },
	{ value: 'terminate_other', label: 'Terminate the running one' },
	{ value: 'allow_all', label: 'Run in parallel' }
];

export type ScheduleType = 'interval' | 'cron' | 'calendar';

export function scheduleType(s?: ScheduleConfig): ScheduleType {
	if (s?.cron) return 'cron';
	if (s?.calendar) return 'calendar';
	return 'interval';
}

const pad = (n: number) => String(n).padStart(2, '0');

/** A human summary of a trigger's schedule, e.g. "Every 5 minutes" or "Mon, Wed at 09:30 (Europe/London)". */
export function scheduleSummary(t: TriggerConfig): string {
	const s = t.schedule;
	const tz = s?.timezone && s.timezone !== 'UTC' ? ` (${s.timezone})` : ' UTC';
	if (s?.cron) return `Cron ${s.cron}${tz}`;
	if (s?.calendar) {
		const days = s.calendar.days?.length ? s.calendar.days.map((d) => DAY_NAMES[d] ?? d).join(', ') : 'Every day';
		return `${days} at ${pad(s.calendar.hour)}:${pad(s.calendar.minute)}${tz}`;
	}
	if (s?.every) return `Every ${s.every} ${s.everyUnit ?? 'seconds'}`.replace(/^Every 1 (\w+)s$/, 'Every $1');
	if (t.everySeconds) return `Every ${t.everySeconds} seconds`;
	return 'Schedule';
}
