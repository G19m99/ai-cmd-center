/**
 * Per-provider rollup for the summary cards: account count, pooled weekly
 * headroom and the next weekly reset. React-free.
 */

import type { RoutingAccount } from '@/services/api/routingState';
import { parseIsoToMs } from '@/utils/quota';

export interface ProviderSummary {
  accounts: number;
  headroomTotal: number;
  headroomMax: number;
  nextWeeklyResetMs: number | null;
  limited: number;
}

export function summarizeProvider(
  accounts: readonly RoutingAccount[],
  provider: string,
  now: number
): ProviderSummary {
  const mine = accounts.filter((account) => account.provider === provider);
  let nextWeeklyResetMs: number | null = null;
  let headroomTotal = 0;
  let limited = 0;
  for (const account of mine) {
    headroomTotal += Math.max(0, Math.min(100, account.headroom));
    if (account.status === 'limited') limited += 1;
    const resetMs = parseIsoToMs(account.weeklyResetsAt);
    if (
      resetMs !== null &&
      resetMs > now &&
      (nextWeeklyResetMs === null || resetMs < nextWeeklyResetMs)
    ) {
      nextWeeklyResetMs = resetMs;
    }
  }
  return {
    accounts: mine.length,
    headroomTotal: Math.round(headroomTotal),
    headroomMax: mine.length * 100,
    nextWeeklyResetMs,
    limited,
  };
}
