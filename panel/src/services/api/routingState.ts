/**
 * Routing state (fork-only endpoint): which Claude/Codex account the proxy is
 * draining, who is standing by, who is limited, and their banked bonus resets.
 *
 * `GET  /routing-state`            — current state.
 * `POST /routing-state/refresh`    — `{ auth_index }` re-polls one account;
 *                                    `{}` re-polls every account (slow: N×20s).
 *
 * Both return null on a 404 so the panel degrades to upstream's cards against
 * a server that predates the endpoint. Other errors propagate as ApiError.
 */

import type { ApiError } from '@/types';
import { parseTimestampMs } from '@/utils/timestamp';
import { apiClient } from './client';

export type RoutingAccountStatus = 'draining' | 'standby' | 'limited' | 'unknown';

export const ROUTING_ACCOUNT_STATUSES: readonly RoutingAccountStatus[] = [
  'draining',
  'standby',
  'limited',
  'unknown',
];

export type RoutingBonus = {
  /** False when the reset-credit read failed; the UI then shows "unknown". */
  known: boolean;
  available: number;
  /** Window keys the reset clears, e.g. `seven_day`. */
  clears: string[];
  expiresAt?: string;
};

/** One usage window as the server's poller last read it. */
export type RoutingWindow = {
  /** `five_hour`, `seven_day`, `iguana_necktie`, `primary_window`, ... */
  key: string;
  /** Server label, e.g. `5-hour`, `7-day`, `7-day Fable`, `Weekly`. */
  label: string;
  /** Percent used, 0–100. */
  utilization: number;
  resetsAt?: string;
  weekly: boolean;
  /** Lowercase model or feature the window is limited to; empty for all models. */
  modelScope: string;
  /** False for windows that never block routing (Codex code review). */
  routing: boolean;
};

export type RoutingAccount = {
  authIndex: string;
  provider: 'claude' | 'codex' | string;
  /** Server label (account label, file name, or id). */
  label: string;
  status: RoutingAccountStatus;
  urgent: boolean;
  /** 0 when not ranked (limited, or a non-reset-aware strategy). */
  rank: number;
  sessions: number;
  /** "cooldown", "disabled", or a window label such as "5-hour"; empty when not limited. */
  limitedBy: string;
  freeAt?: string;
  weeklyResetsAt?: string;
  headroom: number;
  bonus: RoutingBonus;
  /** When the poller last read this account's usage; absent before the first read. */
  fetchedAt?: string;
  /** True when the last good read is older than the server's staleness limit. */
  stale: boolean;
  /** Why the last poll failed; empty when it succeeded. */
  lastError: string;
  windows: RoutingWindow[];
};

export type RoutingProviderSummary = {
  urgentAuthIndex: string;
  earliestFreeAt?: string;
};

export type RoutingState = {
  strategy: string;
  generatedAt?: string;
  providers: Record<string, RoutingProviderSummary>;
  accounts: RoutingAccount[];
};

/** One upstream fetch is bounded by the poller's 20s client timeout. */
export const ROUTING_STATE_REFRESH_ONE_TIMEOUT_MS = 45 * 1000;
/** Refresh-all runs sequentially: N accounts × up to 20s each. */
export const ROUTING_STATE_REFRESH_ALL_TIMEOUT_MS = 10 * 60 * 1000;

const asRecord = (value: unknown): Record<string, unknown> | null =>
  value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null;

const asString = (value: unknown): string => (typeof value === 'string' ? value.trim() : '');

const asNumber = (value: unknown, fallback = 0): number =>
  typeof value === 'number' && Number.isFinite(value) ? value : fallback;

const asInteger = (value: unknown): number => Math.max(0, Math.trunc(asNumber(value)));

const asIsoTime = (value: unknown): string | undefined => {
  const text = asString(value);
  if (!text) return undefined;
  const ms = parseTimestampMs(text);
  return Number.isFinite(ms) ? new Date(ms).toISOString() : undefined;
};

const asStatus = (value: unknown): RoutingAccountStatus =>
  ROUTING_ACCOUNT_STATUSES.includes(value as RoutingAccountStatus)
    ? (value as RoutingAccountStatus)
    : 'unknown';

const normalizeBonus = (value: unknown): RoutingBonus => {
  const bonus = asRecord(value);
  if (!bonus) return { known: false, available: 0, clears: [] };
  const clears = Array.isArray(bonus.clears)
    ? bonus.clears.map(asString).filter((key) => key.length > 0)
    : [];
  const expiresAt = asIsoTime(bonus.expires_at);
  return {
    known: bonus.known === true,
    available: asInteger(bonus.available),
    clears,
    ...(expiresAt ? { expiresAt } : {}),
  };
};

/** Null for a window without a key or a numeric utilization. */
const normalizeWindow = (value: unknown): RoutingWindow | null => {
  const window = asRecord(value);
  if (!window) return null;
  const key = asString(window.key);
  const utilization = window.utilization;
  if (!key || typeof utilization !== 'number' || !Number.isFinite(utilization)) return null;
  const resetsAt = asIsoTime(window.resets_at);
  return {
    key,
    label: asString(window.label) || key,
    utilization,
    ...(resetsAt ? { resetsAt } : {}),
    weekly: window.weekly === true,
    modelScope: asString(window.model_scope),
    routing: window.routing !== false,
  };
};

/** Null for anything that cannot be matched to a card (no usable auth_index). */
export function normalizeRoutingAccount(value: unknown): RoutingAccount | null {
  const entry = asRecord(value);
  if (!entry) return null;
  const authIndex = asString(entry.auth_index);
  if (!authIndex) return null;
  const provider = asString(entry.provider);
  if (!provider) return null;
  const quota = asRecord(entry.quota);
  const freeAt = asIsoTime(entry.free_at);
  const weeklyResetsAt = asIsoTime(entry.weekly_resets_at);
  const fetchedAt = asIsoTime(quota?.fetched_at);
  const windows = Array.isArray(quota?.windows)
    ? quota.windows
        .map(normalizeWindow)
        .filter((window): window is RoutingWindow => window !== null)
    : [];
  return {
    authIndex,
    provider,
    label: asString(entry.label) || authIndex,
    status: asStatus(entry.status),
    urgent: entry.urgent === true,
    rank: asInteger(entry.rank),
    sessions: asInteger(entry.sessions),
    limitedBy: asString(entry.limited_by),
    ...(freeAt ? { freeAt } : {}),
    ...(weeklyResetsAt ? { weeklyResetsAt } : {}),
    headroom: asNumber(entry.headroom),
    bonus: normalizeBonus(quota?.bonus),
    ...(fetchedAt ? { fetchedAt } : {}),
    stale: quota?.stale === true,
    lastError: asString(quota?.last_error),
    windows,
  };
}

export function normalizeRoutingState(value: unknown): RoutingState | null {
  const payload = asRecord(value);
  if (!payload) return null;
  const providers: Record<string, RoutingProviderSummary> = {};
  const rawProviders = asRecord(payload.providers);
  if (rawProviders) {
    for (const [key, raw] of Object.entries(rawProviders)) {
      const summary = asRecord(raw);
      if (!summary) continue;
      const earliestFreeAt = asIsoTime(summary.earliest_free_at);
      providers[key] = {
        urgentAuthIndex: asString(summary.urgent_auth_index),
        ...(earliestFreeAt ? { earliestFreeAt } : {}),
      };
    }
  }
  const accounts = Array.isArray(payload.accounts)
    ? payload.accounts
        .map(normalizeRoutingAccount)
        .filter((account): account is RoutingAccount => account !== null)
    : [];
  const generatedAt = asIsoTime(payload.generated_at);
  return {
    strategy: asString(payload.strategy) || 'unknown',
    ...(generatedAt ? { generatedAt } : {}),
    providers,
    accounts,
  };
}

export const isRoutingStateUnsupported = (error: unknown): boolean =>
  Boolean(error) && typeof error === 'object' && (error as ApiError).status === 404;

/** Null when the server predates the endpoint (404). */
export async function fetchRoutingState(): Promise<RoutingState | null> {
  try {
    const data = await apiClient.get<unknown>('/routing-state');
    return normalizeRoutingState(data);
  } catch (error: unknown) {
    if (isRoutingStateUnsupported(error)) return null;
    throw error;
  }
}

/**
 * Re-poll one account (`authIndex`) or every account (omitted). Resolves to the
 * fresh state, or null on 404. A 409 (poller not running), 404 for an unknown
 * index, 400 and 502 propagate as ApiError for the caller to decide.
 */
export async function refreshRoutingState(authIndex?: string): Promise<RoutingState | null> {
  const index = authIndex?.trim() ?? '';
  const body = index ? { auth_index: index } : {};
  const timeout = index
    ? ROUTING_STATE_REFRESH_ONE_TIMEOUT_MS
    : ROUTING_STATE_REFRESH_ALL_TIMEOUT_MS;
  try {
    const data = await apiClient.post<unknown>('/routing-state/refresh', body, { timeout });
    return normalizeRoutingState(data);
  } catch (error: unknown) {
    // A 404 is either an older server or an index that vanished between the
    // list and the click; both mean "nothing to show", so null covers both.
    if (isRoutingStateUnsupported(error)) return null;
    throw error;
  }
}

export const routingStateApi = {
  fetch: fetchRoutingState,
  refresh: refreshRoutingState,
};
