/**
 * Pure formatting for the routing column. React-free so the pill, badge and
 * banner can share one vocabulary and the tests can pin exact strings.
 */

import type { RoutingBonus } from '@/services/api/routingState';
import { parseIsoToMs } from '@/utils/quota';
import { DAY_MS, HOUR_MS, MINUTE_MS } from '@/utils/time/durations';
import type { QuotaProviderType } from '../providers/types';

export type Translate = (key: string, options?: Record<string, unknown>) => string;

/** Claude and Codex render Theo-style rows (routing column + window columns). */
export const isRoutedQuotaType = (type: QuotaProviderType): boolean =>
  type === 'claude' || type === 'codex';

/** DOM id of an account's row; the bonus banner links to it. */
export const quotaAccountElementId = (authIndex: string) => `quota-account-${authIndex}`;

/** `14h`, `1d 3h`, `3h 20m`, `5m`. Never negative; sub-minute reads `0m`. */
export function formatCompactDuration(ms: number): string {
  const total = Math.max(0, Number.isFinite(ms) ? ms : 0);
  const days = Math.floor(total / DAY_MS);
  const hours = Math.floor((total % DAY_MS) / HOUR_MS);
  const minutes = Math.floor((total % HOUR_MS) / MINUTE_MS);
  if (days > 0) return hours > 0 ? `${days}d ${hours}h` : `${days}d`;
  if (hours > 0) return minutes > 0 ? `${hours}h ${minutes}m` : `${hours}h`;
  return `${minutes}m`;
}

const timeFormatters = new Map<string, Intl.DateTimeFormat>();

const getFormatter = (locale: string | undefined, withDate: boolean): Intl.DateTimeFormat => {
  const key = `${locale ?? ''}|${withDate ? 'd' : 't'}`;
  const cached = timeFormatters.get(key);
  if (cached) return cached;
  const options: Intl.DateTimeFormatOptions = withDate
    ? { month: 'numeric', day: 'numeric', hour: 'numeric', minute: '2-digit' }
    : { hour: 'numeric', minute: '2-digit' };
  let formatter: Intl.DateTimeFormat;
  try {
    formatter = new Intl.DateTimeFormat(locale, options);
  } catch {
    formatter = new Intl.DateTimeFormat(undefined, options);
  }
  timeFormatters.set(key, formatter);
  return formatter;
};

/** `3:40 PM` for instants within a day of `now`, otherwise `10/22, 3:40 PM`. */
export function formatClockTime(ms: number, now: number, locale?: string): string {
  const withDate = Math.abs(ms - now) >= DAY_MS;
  return getFormatter(locale, withDate).format(new Date(ms));
}

const dateFormatters = new Map<string, Intl.DateTimeFormat>();

/** `10/25` — month/day only, for expiry badges. */
export function formatShortDate(ms: number, locale?: string): string {
  const key = locale ?? '';
  let formatter = dateFormatters.get(key);
  if (!formatter) {
    try {
      formatter = new Intl.DateTimeFormat(locale, { month: 'numeric', day: 'numeric' });
    } catch {
      formatter = new Intl.DateTimeFormat(undefined, { month: 'numeric', day: 'numeric' });
    }
    dateFormatters.set(key, formatter);
  }
  return formatter.format(new Date(ms));
}

const KNOWN_WINDOW_KEYS = new Set(['five_hour', 'seven_day', 'seven_day_overage_included']);

/** Human label for a quota window key the server reports (`seven_day` → `7-day`). */
export function windowLabel(t: Translate, key: string): string {
  return KNOWN_WINDOW_KEYS.has(key) ? t(`routing.window_${key}`) : key;
}

const KNOWN_LIMITERS = new Set(['cooldown', 'disabled']);

/** Localized reason an account is limited; window labels pass through. */
export function limitedByLabel(t: Translate, limitedBy: string): string {
  return KNOWN_LIMITERS.has(limitedBy) ? t(`routing.limited_by_${limitedBy}`) : limitedBy;
}

const MASK = '•••';

/**
 * Hide the identifying part of an account name while keeping it recognizable:
 * `alice@example.com` → `a•••@example.com`, `codex-work.json` → `co•••.json`.
 */
export function maskAccountName(name: string): string {
  const at = name.indexOf('@');
  if (at > 0) {
    return `${name.slice(0, 1)}${MASK}${name.slice(at)}`;
  }
  const dot = name.lastIndexOf('.');
  const stem = dot > 0 ? name.slice(0, dot) : name;
  const extension = dot > 0 ? name.slice(dot) : '';
  if (stem.length <= 2) return name;
  return `${stem.slice(0, 2)}${MASK}${extension}`;
}

/**
 * `1 reset available · clears 7-day · expires 10/25`, `resets: unknown` when
 * the server's reset-credit read failed, or null when known and none.
 */
export function describeBonus(
  t: Translate,
  bonus: RoutingBonus,
  locale: string | undefined
): string | null {
  if (!bonus.known) return t('routing.bonus_unknown');
  if (bonus.available <= 0) return null;
  const parts = [t('routing.bonus_available', { count: bonus.available })];
  if (bonus.clears.length > 0) {
    parts.push(
      t('routing.bonus_clears', {
        windows: bonus.clears.map((key) => windowLabel(t, key)).join(', '),
      })
    );
  }
  const expiresMs = parseIsoToMs(bonus.expiresAt);
  if (expiresMs !== null) {
    parts.push(t('routing.bonus_expires', { date: formatShortDate(expiresMs, locale) }));
  }
  return parts.join(' · ');
}
