/**
 * Pure view logic for the quota accounts list: window names, order and
 * severity, reset wording, account order, the per-provider headline and the
 * bonus line. React-free so the tests can pin exact strings.
 *
 * Usage is always shown as percent used, the way claude.ai and ChatGPT report
 * it, never as headroom.
 */

import type {
  RoutingAccount,
  RoutingAccountStatus,
  RoutingBonus,
  RoutingProviderSummary,
  RoutingWindow,
} from '@/services/api/routingState';
import { parseIsoToMs } from '@/utils/quota';
import { DAY_MS, MINUTE_MS } from '@/utils/time/durations';
import { formatCompactDuration, type Translate } from '../routing/routingFormat';

const PREFIX = 'quota_accounts';

const isCodeReview = (window: RoutingWindow) => window.key.startsWith('code_review_rate_limit.');

/** `7-day Fable` → `Fable`, `GPT-5-Codex-Mini 5-hour` → `GPT-5-Codex-Mini`. */
const scopeName = (window: RoutingWindow): string => {
  const stripped = window.label.replace(/\b(7-day|5-hour|weekly)\b/gi, '').trim();
  if (stripped) return stripped;
  return window.modelScope.charAt(0).toUpperCase() + window.modelScope.slice(1);
};

/** `Weekly, all models`, `Weekly, Fable`, `5-hour session`, `Weekly, code review`. */
export function windowName(t: Translate, window: RoutingWindow): string {
  const span = window.weekly ? 'weekly' : 'session';
  if (window.modelScope) {
    return t(`${PREFIX}.window_${span}_scoped`, { scope: scopeName(window) });
  }
  if (isCodeReview(window)) return t(`${PREFIX}.window_${span}_code_review`);
  return t(`${PREFIX}.window_${span}`);
}

const windowRank = (window: RoutingWindow): number => {
  if (!window.routing) return 3;
  if (!window.weekly) return 2;
  return window.modelScope || isCodeReview(window) ? 1 : 0;
};

/** All-model weekly first, then scoped weekly, then sessions, then non-routing windows. */
export const orderWindows = (windows: readonly RoutingWindow[]): RoutingWindow[] =>
  [...windows].sort((a, b) => windowRank(a) - windowRank(b));

export type UsageLevel = 'normal' | 'high' | 'full';

export const usageLevel = (utilization: number): UsageLevel =>
  utilization >= 100 ? 'full' : utilization >= 80 ? 'high' : 'normal';

const NARROW_NO_BREAK_SPACE = String.fromCharCode(0x202f);

const formatters = new Map<string, Intl.DateTimeFormat>();

const format = (locale: string | undefined, options: Intl.DateTimeFormatOptions, date: Date) => {
  const key = `${locale ?? ''}|${JSON.stringify(options)}`;
  let formatter = formatters.get(key);
  if (!formatter) {
    try {
      formatter = new Intl.DateTimeFormat(locale, options);
    } catch {
      formatter = new Intl.DateTimeFormat(undefined, options);
    }
    formatters.set(key, formatter);
  }
  // ICU puts a narrow no-break space before AM/PM; keep plain text plain.
  return formatter.format(date).split(NARROW_NO_BREAK_SPACE).join(' ');
};

const startOfDay = (ms: number) => {
  const date = new Date(ms);
  date.setHours(0, 0, 0, 0);
  return date.getTime();
};

/**
 * `Today 2:40 PM`, `Tomorrow 1:00 PM`, `Sat 9:05 AM`, `Oct 29, 1:00 PM`; the
 * inline style reads inside a sentence (`tomorrow at 1:00 PM`). Rounded to the
 * nearest minute: upstream reports resets like 12:59:59.864.
 */
export function formatResetMoment(
  t: Translate,
  ms: number,
  now: number,
  locale?: string,
  style: 'cell' | 'inline' = 'cell'
): string {
  const date = new Date(Math.round(ms / MINUTE_MS) * MINUTE_MS);
  const days = Math.round((startOfDay(date.getTime()) - startOfDay(now)) / DAY_MS);
  const time = format(locale, { hour: 'numeric', minute: '2-digit' }, date);
  const suffix = style === 'inline' ? '_inline' : '';
  if (days === 0) return t(`${PREFIX}.today_at${suffix}`, { time });
  if (days === 1) return t(`${PREFIX}.tomorrow_at${suffix}`, { time });
  if (days > 1 && days < 7) {
    const day = format(locale, { weekday: 'short' }, date);
    return t(`${PREFIX}.weekday_at${suffix}`, { day, time });
  }
  const day = format(locale, { month: 'short', day: 'numeric' }, date);
  return t(`${PREFIX}.date_at${suffix}`, { date: day, time });
}

const STATUS_ORDER: Record<RoutingAccountStatus, number> = {
  draining: 0,
  standby: 1,
  limited: 2,
  unknown: 3,
};

const rankKey = (account: RoutingAccount) =>
  account.rank > 0 ? account.rank : Number.MAX_SAFE_INTEGER;

/** In use first, then standby by rank, then limited, then unknown. */
export const sortAccounts = (accounts: readonly RoutingAccount[]): RoutingAccount[] =>
  [...accounts].sort(
    (a, b) =>
      STATUS_ORDER[a.status] - STATUS_ORDER[b.status] ||
      rankKey(a) - rankKey(b) ||
      a.label.localeCompare(b.label)
  );

export type ProviderHeadline =
  | {
      kind: 'serving';
      label: string;
      urgent: boolean;
      /** The provider has one account, so "resets first" means nothing. */
      only: boolean;
      /** Weekly percent left, 0–100. */
      left: number;
      resetsAtMs: number | null;
    }
  | { kind: 'all_limited'; freeAtMs: number | null }
  | { kind: 'waiting' };

/** Which account takes new requests and why, from one provider's accounts. */
export function providerHeadline(
  accounts: readonly RoutingAccount[],
  summary?: RoutingProviderSummary
): ProviderHeadline {
  const serving = sortAccounts(accounts).find((account) => account.status === 'draining');
  if (serving) {
    return {
      kind: 'serving',
      label: serving.label,
      urgent: serving.urgent,
      only: accounts.length === 1,
      left: Math.round(Math.max(0, Math.min(100, serving.headroom))),
      resetsAtMs: parseIsoToMs(serving.weeklyResetsAt),
    };
  }
  if (accounts.length > 0 && accounts.every((account) => account.status === 'limited')) {
    const freeTimes = accounts
      .map((account) => parseIsoToMs(account.freeAt))
      .filter((ms): ms is number => ms !== null);
    const earliest = freeTimes.length > 0 ? Math.min(...freeTimes) : null;
    return { kind: 'all_limited', freeAtMs: parseIsoToMs(summary?.earliestFreeAt) ?? earliest };
  }
  return { kind: 'waiting' };
}

export function headlineText(
  t: Translate,
  headline: ProviderHeadline,
  provider: string,
  now: number,
  locale?: string
): string {
  if (headline.kind === 'waiting') return t(`${PREFIX}.headline_waiting`);
  if (headline.kind === 'all_limited') {
    return headline.freeAtMs === null
      ? t(`${PREFIX}.headline_all_limited`, { provider })
      : t(`${PREFIX}.headline_all_limited_until`, {
          provider,
          time: formatResetMoment(t, headline.freeAtMs, now, locale, 'inline'),
        });
  }
  const lead = t(`${PREFIX}.headline_serving`, { account: headline.label });
  const left = headline.left;
  const reset =
    headline.resetsAtMs === null
      ? null
      : formatResetMoment(t, headline.resetsAtMs, now, locale, 'inline');
  let reason: string;
  if (reset === null) reason = t(`${PREFIX}.headline_left`, { left });
  else if (headline.urgent) reason = t(`${PREFIX}.headline_urgent`, { left, reset });
  else if (headline.only) reason = t(`${PREFIX}.headline_left_until`, { left, reset });
  else reason = t(`${PREFIX}.headline_left_first`, { left, reset });
  return `${lead} ${reason}`;
}

/** Window keys a bonus reset clears, in the plain words the bonus line uses. */
const CLEAR_NAMES: Record<string, 'clear_session' | 'clear_weekly'> = {
  five_hour: 'clear_session',
  primary_window: 'clear_session',
  seven_day: 'clear_weekly',
  seven_day_overage_included: 'clear_weekly',
  secondary_window: 'clear_weekly',
};

/**
 * `1 bonus reset available until tomorrow at 12:00 PM. Using one clears the
 * 5-hour session and weekly limits.`, a notice when the read failed, or null
 * when there is nothing to redeem.
 */
export function bonusText(
  t: Translate,
  bonus: RoutingBonus,
  now: number,
  locale?: string
): string | null {
  if (!bonus.known) return t(`${PREFIX}.bonus_unknown`);
  if (bonus.available <= 0) return null;
  const count = bonus.available;
  const expiresMs = parseIsoToMs(bonus.expiresAt);
  const available =
    expiresMs === null
      ? t(`${PREFIX}.bonus_available`, { count })
      : t(`${PREFIX}.bonus_available_until`, {
          count,
          time: formatResetMoment(t, expiresMs, now, locale, 'inline'),
        });
  const names = [
    ...new Set(
      bonus.clears
        .map((key) => CLEAR_NAMES[key])
        .filter((name): name is 'clear_session' | 'clear_weekly' => Boolean(name))
    ),
  ].map((name) => t(`${PREFIX}.${name}`));
  if (names.length === 0) return available;
  // At most two names: the 5-hour session and the weekly limit.
  const windows =
    names.length === 1 ? names[0] : t(`${PREFIX}.list_two`, { first: names[0], second: names[1] });
  return `${available} ${t(`${PREFIX}.bonus_clears`, { count: names.length, windows })}`;
}

/** Oldest successful usage read across the accounts; null before the first. */
export function oldestFetchMs(accounts: readonly RoutingAccount[]): number | null {
  const times = accounts
    .map((account) => parseIsoToMs(account.fetchedAt))
    .filter((ms): ms is number => ms !== null);
  return times.length > 0 ? Math.min(...times) : null;
}

export function checkedText(t: Translate, fetchedMs: number | null, now: number): string {
  if (fetchedMs === null) return t(`${PREFIX}.checked_never`);
  const age = now - fetchedMs;
  if (age < MINUTE_MS) return t(`${PREFIX}.checked_just_now`);
  return t(`${PREFIX}.checked_ago`, { duration: formatCompactDuration(age) });
}

/**
 * A limit's reset as a phrase (`Resets tomorrow at 1:00 PM`), plus how long
 * until it when it is ahead (`in 23h 59m`). A window that has not started
 * reads `Not started`.
 */
export function resetLine(
  t: Translate,
  window: RoutingWindow,
  now: number,
  locale?: string
): { text: string; relative: string | null } {
  const ms = parseIsoToMs(window.resetsAt);
  if (ms === null) {
    return {
      text: t(`${PREFIX}.${window.utilization > 0 ? 'reset_unknown' : 'reset_not_started'}`),
      relative: null,
    };
  }
  return {
    text: t(`${PREFIX}.resets_at`, { time: formatResetMoment(t, ms, now, locale, 'inline') }),
    relative:
      ms > now ? t(`${PREFIX}.in_duration`, { duration: formatCompactDuration(ms - now) }) : null,
  };
}
