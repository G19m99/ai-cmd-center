/**
 * Routing-state store + page hook.
 *
 * The store holds the last good `RoutingState`, whether the server supports
 * the endpoint (a 404 turns polling off for the session), and the "show
 * emails" preference. The hook polls every 30s while the page is mounted,
 * enabled, and the document is visible; `refreshAccount` re-polls one account
 * after the card's own "Refresh quota".
 *
 * Stale guards: every request captures the client's connection revision and a
 * request id; results from an older connection or a superseded request are
 * dropped, matching the quota cache's generation guard.
 */

import { useCallback, useEffect, useMemo } from 'react';
import { create } from 'zustand';
import { apiClient } from '@/services/api/client';
import {
  fetchRoutingState,
  refreshRoutingState,
  type RoutingAccount,
  type RoutingState,
} from '@/services/api/routingState';
import type { ApiError } from '@/types';

export const ROUTING_POLL_INTERVAL_MS = 30_000;
const SHOW_EMAILS_KEY = 'quotaPage.routing.showEmails';

const readShowEmails = (): boolean => {
  if (typeof window === 'undefined') return false;
  try {
    return window.localStorage.getItem(SHOW_EMAILS_KEY) === '1';
  } catch {
    return false;
  }
};

const writeShowEmails = (value: boolean) => {
  if (typeof window === 'undefined') return;
  try {
    window.localStorage.setItem(SHOW_EMAILS_KEY, value ? '1' : '0');
  } catch {
    // Preference only; losing it is harmless.
  }
};

interface RoutingStateStore {
  state: RoutingState | null;
  /** False once the server answered 404: an older build without the endpoint. */
  supported: boolean;
  loading: boolean;
  /** Last non-404 failure message; cleared by the next success. */
  error: string;
  showEmails: boolean;
  setShowEmails: (value: boolean) => void;
  load: () => Promise<void>;
  /** POST refresh for one account; swallows 409/404 (nothing to show the user). */
  refreshAccount: (authIndex: string) => Promise<void>;
  /** POST refresh for every account (slow). */
  refreshAll: () => Promise<void>;
  reset: () => void;
}

let requestSeq = 0;

export const useRoutingStateStore = create<RoutingStateStore>((set) => {
  const begin = () => {
    const id = ++requestSeq;
    const revision = apiClient.getConnectionRevision();
    return () => id === requestSeq && revision === apiClient.getConnectionRevision();
  };

  const commit = (
    isCurrent: () => boolean,
    run: () => Promise<RoutingState | null>,
    swallow: (error: unknown) => boolean = () => false,
    nullMeansUnsupported = true
  ) =>
    (async () => {
      set({ loading: true });
      try {
        const next = await run();
        if (!isCurrent()) return;
        if (next === null) {
          // GET 404: the server predates the endpoint. A refresh 404 can also be
          // a vanished index, so only the poll flips `supported`.
          if (nullMeansUnsupported) {
            set({ state: null, supported: false, loading: false, error: '' });
          } else {
            set({ loading: false });
          }
          return;
        }
        set({ state: next, supported: true, loading: false, error: '' });
      } catch (error: unknown) {
        if (!isCurrent()) return;
        if (swallow(error)) {
          set({ loading: false });
          return;
        }
        const message = error instanceof Error ? error.message : String(error);
        set({ loading: false, error: message });
      }
    })();

  return {
    state: null,
    supported: true,
    loading: false,
    error: '',
    showEmails: readShowEmails(),
    setShowEmails: (value) => {
      writeShowEmails(value);
      set({ showEmails: value });
    },
    load: () => commit(begin(), fetchRoutingState),
    refreshAccount: (authIndex) =>
      commit(
        begin(),
        () => refreshRoutingState(authIndex),
        // 409: poller not running (strategy is not reset-aware). Not actionable here.
        (error) => (error as ApiError | undefined)?.status === 409,
        false
      ),
    refreshAll: () =>
      commit(
        begin(),
        () => refreshRoutingState(),
        (error) => (error as ApiError | undefined)?.status === 409,
        false
      ),
    reset: () => {
      requestSeq += 1;
      set({ state: null, supported: true, loading: false, error: '' });
    },
  };
});

export const indexRoutingAccounts = (
  accounts: readonly RoutingAccount[]
): Map<string, RoutingAccount> => new Map(accounts.map((account) => [account.authIndex, account]));

export const shouldPollRoutingState = (
  enabled: boolean,
  visibility: DocumentVisibilityState,
  supported: boolean
): boolean => enabled && supported && visibility === 'visible';

export function useRoutingState(enabled: boolean) {
  const state = useRoutingStateStore((store) => store.state);
  const supported = useRoutingStateStore((store) => store.supported);
  const loading = useRoutingStateStore((store) => store.loading);
  const error = useRoutingStateStore((store) => store.error);
  const showEmails = useRoutingStateStore((store) => store.showEmails);
  const setShowEmails = useRoutingStateStore((store) => store.setShowEmails);
  const load = useRoutingStateStore((store) => store.load);
  const refreshAccount = useRoutingStateStore((store) => store.refreshAccount);
  const refreshAll = useRoutingStateStore((store) => store.refreshAll);
  const reset = useRoutingStateStore((store) => store.reset);

  useEffect(() => {
    if (!enabled) {
      reset();
      return;
    }
    const tick = () => {
      if (
        shouldPollRoutingState(
          true,
          typeof document === 'undefined' ? 'visible' : document.visibilityState,
          useRoutingStateStore.getState().supported
        )
      ) {
        void load();
      }
    };
    tick();
    const timer = setInterval(tick, ROUTING_POLL_INTERVAL_MS);
    const onVisibility = () => {
      if (document.visibilityState === 'visible') tick();
    };
    document.addEventListener('visibilitychange', onVisibility);
    return () => {
      clearInterval(timer);
      document.removeEventListener('visibilitychange', onVisibility);
    };
  }, [enabled, load, reset]);

  const accountsByIndex = useMemo(
    () => indexRoutingAccounts(state?.accounts ?? []),
    [state?.accounts]
  );

  const refreshAccountIfSupported = useCallback(
    (authIndex: string | null) => {
      if (!authIndex || !useRoutingStateStore.getState().supported) return;
      void refreshAccount(authIndex);
    },
    [refreshAccount]
  );

  return {
    state,
    supported,
    loading,
    error,
    accountsByIndex,
    showEmails,
    setShowEmails,
    load,
    refreshAccount: refreshAccountIfSupported,
    refreshAll,
  };
}
