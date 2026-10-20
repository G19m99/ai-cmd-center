import { afterEach, describe, expect, spyOn, test } from 'bun:test';
import { apiClient } from '@/services/api/client';
import type { ApiError } from '@/types';
import { ROUTING_STATE_SAMPLE, routingAccount } from '@/features/quota/routing/routingFixtures';
import {
  indexRoutingAccounts,
  isOlderRoutingState,
  ROUTING_POLL_INTERVAL_MS,
  shouldPollRoutingState,
  useRoutingStateStore,
} from '@/features/quota/routing/useRoutingState';
import { normalizeRoutingState } from '@/services/api/routingState';

const apiError = (status: number, message = 'nope'): ApiError => {
  const error = new Error(message) as ApiError;
  error.name = 'ApiError';
  error.status = status;
  return error;
};

afterEach(() => {
  useRoutingStateStore.getState().reset();
});

describe('routing state store', () => {
  test('load stores the normalized state', async () => {
    const get = spyOn(apiClient, 'get').mockResolvedValue(ROUTING_STATE_SAMPLE);
    try {
      await useRoutingStateStore.getState().load();
      const { state, supported, error } = useRoutingStateStore.getState();
      expect(supported).toBe(true);
      expect(error).toBe('');
      expect(state?.accounts.map((account) => account.status)).toEqual(['draining', 'standby']);
    } finally {
      get.mockRestore();
    }
  });

  test('a 404 marks the endpoint unsupported and stops polling', async () => {
    const get = spyOn(apiClient, 'get').mockRejectedValue(apiError(404));
    try {
      await useRoutingStateStore.getState().load();
      const { state, supported } = useRoutingStateStore.getState();
      expect(state).toBeNull();
      expect(supported).toBe(false);
      expect(shouldPollRoutingState(true, 'visible', supported)).toBe(false);
    } finally {
      get.mockRestore();
    }
  });

  test('other errors keep the last state and surface a message', async () => {
    const get = spyOn(apiClient, 'get').mockResolvedValue(ROUTING_STATE_SAMPLE);
    try {
      await useRoutingStateStore.getState().load();
      get.mockRejectedValue(apiError(500, 'upstream down'));
      await useRoutingStateStore.getState().load();
      const { state, supported, error } = useRoutingStateStore.getState();
      expect(state?.accounts).toHaveLength(2);
      expect(supported).toBe(true);
      expect(error).toBe('upstream down');
    } finally {
      get.mockRestore();
    }
  });

  test('refreshAccount posts the index and replaces the state', async () => {
    const refreshed = {
      ...ROUTING_STATE_SAMPLE,
      accounts: [ROUTING_STATE_SAMPLE.accounts[1]],
    };
    const post = spyOn(apiClient, 'post').mockResolvedValue(refreshed);
    try {
      await useRoutingStateStore.getState().refreshAccount('c0f0501ebd9fd741');
      expect(post).toHaveBeenCalledWith(
        '/routing-state/refresh',
        { auth_index: 'c0f0501ebd9fd741' },
        expect.anything()
      );
      expect(useRoutingStateStore.getState().state?.accounts).toHaveLength(1);
    } finally {
      post.mockRestore();
    }
  });

  test('a poll that lands during a row refresh does not discard the refresh result', async () => {
    const refreshed = {
      ...ROUTING_STATE_SAMPLE,
      generated_at: '2026-10-20T15:52:00Z',
      accounts: [{ ...ROUTING_STATE_SAMPLE.accounts[1], sessions: 9 }],
    };
    let resolvePost: (value: unknown) => void = () => {};
    const post = spyOn(apiClient, 'post').mockReturnValue(
      new Promise<unknown>((resolve) => {
        resolvePost = resolve;
      })
    );
    const get = spyOn(apiClient, 'get').mockResolvedValue(ROUTING_STATE_SAMPLE);
    try {
      // The POST is in flight (up to 45s); the 30s poll starts and finishes first.
      const refresh = useRoutingStateStore.getState().refreshAccount('c0f0501ebd9fd741');
      await useRoutingStateStore.getState().load();
      expect(useRoutingStateStore.getState().state?.accounts).toHaveLength(2);

      resolvePost(refreshed);
      await refresh;
      const { state, error } = useRoutingStateStore.getState();
      expect(error).toBe('');
      expect(state?.generatedAt).toBe('2026-10-20T15:52:00.000Z');
      expect(state?.accounts.map((account) => account.sessions)).toEqual([9]);
    } finally {
      post.mockRestore();
      get.mockRestore();
    }
  });

  test('a poll generated before the state shown is dropped', async () => {
    const fresh = { ...ROUTING_STATE_SAMPLE, generated_at: '2026-10-20T15:52:00Z' };
    const stale = {
      ...ROUTING_STATE_SAMPLE,
      generated_at: '2026-10-20T15:51:00Z',
      accounts: [ROUTING_STATE_SAMPLE.accounts[0]],
    };
    const post = spyOn(apiClient, 'post').mockResolvedValue(fresh);
    const get = spyOn(apiClient, 'get').mockResolvedValue(stale);
    try {
      await useRoutingStateStore.getState().refreshAccount('c0f0501ebd9fd741');
      await useRoutingStateStore.getState().load();
      const { state, error, loading } = useRoutingStateStore.getState();
      expect(error).toBe('');
      expect(loading).toBe(false);
      expect(state?.generatedAt).toBe('2026-10-20T15:52:00.000Z');
      expect(state?.accounts).toHaveLength(2);
    } finally {
      post.mockRestore();
      get.mockRestore();
    }
  });

  test('a newer poll still supersedes an older poll', async () => {
    let resolveFirst: (value: unknown) => void = () => {};
    const get = spyOn(apiClient, 'get')
      .mockReturnValueOnce(
        new Promise<unknown>((resolve) => {
          resolveFirst = resolve;
        })
      )
      .mockResolvedValueOnce({
        ...ROUTING_STATE_SAMPLE,
        generated_at: '',
        accounts: [ROUTING_STATE_SAMPLE.accounts[0]],
      });
    try {
      const first = useRoutingStateStore.getState().load();
      await useRoutingStateStore.getState().load();
      expect(useRoutingStateStore.getState().state?.accounts).toHaveLength(1);
      resolveFirst(ROUTING_STATE_SAMPLE);
      await first;
      expect(useRoutingStateStore.getState().state?.accounts).toHaveLength(1);
    } finally {
      get.mockRestore();
    }
  });

  test('refreshAccount swallows a 409 (poller not running)', async () => {
    const post = spyOn(apiClient, 'post').mockRejectedValue(apiError(409, 'poller not running'));
    try {
      await useRoutingStateStore.getState().refreshAccount('x');
      expect(useRoutingStateStore.getState().error).toBe('');
    } finally {
      post.mockRestore();
    }
  });
});

describe('helpers', () => {
  test('indexRoutingAccounts keys accounts by auth index', () => {
    const map = indexRoutingAccounts([
      routingAccount({ authIndex: 'a' }),
      routingAccount({ authIndex: 'b', provider: 'codex' }),
    ]);
    expect(map.get('a')?.provider).toBe('claude');
    expect(map.get('b')?.provider).toBe('codex');
    expect(map.size).toBe(2);
  });

  test('isOlderRoutingState compares generated_at and tolerates missing values', () => {
    const at = (generatedAt: string) =>
      normalizeRoutingState({ ...ROUTING_STATE_SAMPLE, generated_at: generatedAt });
    const older = at('2026-10-20T15:51:00Z');
    const newer = at('2026-10-20T15:52:00Z');
    const unknown = at('');
    if (!older || !newer || !unknown) throw new Error('fixtures failed to normalize');
    expect(isOlderRoutingState(older, newer)).toBe(true);
    expect(isOlderRoutingState(newer, older)).toBe(false);
    expect(isOlderRoutingState(newer, newer)).toBe(false);
    expect(isOlderRoutingState(unknown, newer)).toBe(false);
    expect(isOlderRoutingState(older, unknown)).toBe(false);
    expect(isOlderRoutingState(older, null)).toBe(false);
  });

  test('polls only while enabled, visible, and supported', () => {
    expect(ROUTING_POLL_INTERVAL_MS).toBe(30_000);
    expect(shouldPollRoutingState(true, 'visible', true)).toBe(true);
    expect(shouldPollRoutingState(false, 'visible', true)).toBe(false);
    expect(shouldPollRoutingState(true, 'hidden', true)).toBe(false);
    expect(shouldPollRoutingState(true, 'visible', false)).toBe(false);
  });
});
