import { describe, expect, spyOn, test } from 'bun:test';
import { apiClient } from '@/services/api/client';
import {
  fetchRoutingState,
  normalizeRoutingState,
  refreshRoutingState,
  ROUTING_STATE_REFRESH_ALL_TIMEOUT_MS,
  ROUTING_STATE_REFRESH_ONE_TIMEOUT_MS,
} from '@/services/api/routingState';
import type { ApiError } from '@/types';
import { ROUTING_STATE_SAMPLE } from '@/features/quota/routing/routingFixtures';

const apiError = (status: number, message = 'nope'): ApiError => {
  const error = new Error(message) as ApiError;
  error.name = 'ApiError';
  error.status = status;
  return error;
};

describe('normalizeRoutingState', () => {
  test('normalizes the Task 5 sample', () => {
    const state = normalizeRoutingState(ROUTING_STATE_SAMPLE);
    expect(state).not.toBeNull();
    expect(state?.strategy).toBe('reset-aware');
    expect(state?.generatedAt).toBe('2026-10-20T15:51:14.000Z');
    expect(state?.providers.claude?.urgentAuthIndex).toBe('8dbc2c9a3bc57d7c');
    expect(state?.providers.codex?.urgentAuthIndex).toBe('');
    expect(state?.accounts).toHaveLength(2);

    const [alice, bob] = state!.accounts;
    expect(alice).toEqual({
      authIndex: '8dbc2c9a3bc57d7c',
      provider: 'claude',
      label: 'alice@example.com',
      status: 'draining',
      urgent: true,
      rank: 1,
      sessions: 3,
      limitedBy: '',
      weeklyResetsAt: '2026-10-21T03:51:14.000Z',
      headroom: 38,
      bonus: { known: true, available: 1, clears: ['seven_day'] },
      fetchedAt: '2026-10-20T15:51:14.000Z',
      stale: false,
      lastError: '',
      windows: [
        {
          key: 'five_hour',
          label: '5-hour',
          utilization: 28,
          resetsAt: '2026-10-20T18:51:14.000Z',
          weekly: false,
          modelScope: '',
          routing: true,
        },
        {
          key: 'seven_day',
          label: '7-day',
          utilization: 62,
          resetsAt: '2026-10-21T03:51:14.000Z',
          weekly: true,
          modelScope: '',
          routing: true,
        },
      ],
    });
    expect(alice.freeAt).toBeUndefined();
    expect(bob.status).toBe('standby');
    expect(bob.rank).toBe(2);
    expect(bob.sessions).toBe(1);
    expect(bob.headroom).toBe(90);
  });

  test('keeps limited accounts with their free_at and label', () => {
    const state = normalizeRoutingState({
      strategy: 'reset-aware',
      accounts: [
        {
          auth_index: 'capped',
          provider: 'codex',
          status: 'limited',
          rank: 0,
          limited_by: '5-hour',
          free_at: '2026-10-20T18:51:14Z',
          headroom: 0,
          quota: { bonus: { known: false, available: 0, clears: [] } },
        },
      ],
    });
    const account = state?.accounts[0];
    expect(account?.status).toBe('limited');
    expect(account?.limitedBy).toBe('5-hour');
    expect(account?.freeAt).toBe('2026-10-20T18:51:14.000Z');
    expect(account?.bonus.known).toBe(false);
    expect(account?.label).toBe('capped');
    expect(account?.windows).toEqual([]);
    expect(account?.fetchedAt).toBeUndefined();
  });

  test('keeps a failed poll visible and drops unusable windows', () => {
    const state = normalizeRoutingState({
      accounts: [
        {
          auth_index: 'flaky',
          provider: 'claude',
          quota: {
            fetched_at: '',
            stale: true,
            last_error: 'usage endpoint returned 500',
            windows: [
              { key: 'seven_day', label: '7-day', utilization: 'n/a', weekly: true },
              { label: 'no key', utilization: 5 },
              {
                key: 'iguana_necktie',
                label: '7-day Fable',
                utilization: 100,
                resets_at: '',
                weekly: true,
                model_scope: 'fable',
                routing: true,
              },
            ],
          },
        },
      ],
    });
    const account = state?.accounts[0];
    expect(account?.stale).toBe(true);
    expect(account?.lastError).toBe('usage endpoint returned 500');
    expect(account?.fetchedAt).toBeUndefined();
    expect(account?.windows).toEqual([
      {
        key: 'iguana_necktie',
        label: '7-day Fable',
        utilization: 100,
        weekly: true,
        modelScope: 'fable',
        routing: true,
      },
    ]);
  });

  test('drops malformed accounts and tolerates missing collections', () => {
    const state = normalizeRoutingState({
      strategy: 'round-robin',
      accounts: [
        null,
        'nope',
        { provider: 'claude', status: 'standby' },
        { auth_index: '', provider: 'claude', status: 'standby' },
        { auth_index: 'ok', provider: 'claude', status: 'mystery', rank: 'x', headroom: 'y' },
      ],
    });
    expect(state?.strategy).toBe('round-robin');
    expect(state?.accounts).toHaveLength(1);
    expect(state?.accounts[0]).toMatchObject({
      authIndex: 'ok',
      status: 'unknown',
      rank: 0,
      headroom: 0,
      sessions: 0,
      bonus: { known: false, available: 0, clears: [] },
    });
    expect(normalizeRoutingState({ accounts: 'not-a-list' })?.accounts).toEqual([]);
    expect(normalizeRoutingState(null)).toBeNull();
    expect(normalizeRoutingState('text')).toBeNull();
  });
});

describe('fetchRoutingState', () => {
  test('GETs the management endpoint and normalizes', async () => {
    const get = spyOn(apiClient, 'get').mockResolvedValue(ROUTING_STATE_SAMPLE);
    try {
      const state = await fetchRoutingState();
      expect(get).toHaveBeenCalledWith('/routing-state');
      expect(state?.accounts.map((account) => account.authIndex)).toEqual([
        '8dbc2c9a3bc57d7c',
        'c0f0501ebd9fd741',
      ]);
    } finally {
      get.mockRestore();
    }
  });

  test('returns null on 404 (older servers) and rethrows other errors', async () => {
    const get = spyOn(apiClient, 'get').mockRejectedValue(apiError(404));
    try {
      expect(await fetchRoutingState()).toBeNull();
      get.mockRejectedValue(apiError(500, 'boom'));
      await expect(fetchRoutingState()).rejects.toThrow('boom');
    } finally {
      get.mockRestore();
    }
  });
});

describe('refreshRoutingState', () => {
  test('POSTs the auth index with a per-account timeout', async () => {
    const post = spyOn(apiClient, 'post').mockResolvedValue(ROUTING_STATE_SAMPLE);
    try {
      const state = await refreshRoutingState('8dbc2c9a3bc57d7c');
      expect(post).toHaveBeenCalledWith(
        '/routing-state/refresh',
        { auth_index: '8dbc2c9a3bc57d7c' },
        { timeout: ROUTING_STATE_REFRESH_ONE_TIMEOUT_MS }
      );
      expect(state?.accounts).toHaveLength(2);
    } finally {
      post.mockRestore();
    }
  });

  test('refresh-all sends an empty body with a generous timeout', async () => {
    const post = spyOn(apiClient, 'post').mockResolvedValue(ROUTING_STATE_SAMPLE);
    try {
      await refreshRoutingState();
      expect(post).toHaveBeenCalledWith(
        '/routing-state/refresh',
        {},
        { timeout: ROUTING_STATE_REFRESH_ALL_TIMEOUT_MS }
      );
      expect(ROUTING_STATE_REFRESH_ALL_TIMEOUT_MS).toBeGreaterThan(
        ROUTING_STATE_REFRESH_ONE_TIMEOUT_MS
      );
    } finally {
      post.mockRestore();
    }
  });

  test('404 means unsupported (null); 409 and 502 propagate', async () => {
    const post = spyOn(apiClient, 'post').mockRejectedValue(apiError(404));
    try {
      expect(await refreshRoutingState('x')).toBeNull();
      post.mockRejectedValue(apiError(409, 'quota poller not running'));
      await expect(refreshRoutingState('x')).rejects.toMatchObject({ status: 409 });
    } finally {
      post.mockRestore();
    }
  });
});
