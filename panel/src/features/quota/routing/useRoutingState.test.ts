import { afterEach, describe, expect, spyOn, test } from 'bun:test';
import { apiClient } from '@/services/api/client';
import type { ApiError } from '@/types';
import { ROUTING_STATE_SAMPLE, routingAccount } from '@/features/quota/routing/routingFixtures';
import {
  indexRoutingAccounts,
  ROUTING_POLL_INTERVAL_MS,
  shouldPollRoutingState,
  useRoutingStateStore,
} from '@/features/quota/routing/useRoutingState';

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

  test('polls only while enabled, visible, and supported', () => {
    expect(ROUTING_POLL_INTERVAL_MS).toBe(30_000);
    expect(shouldPollRoutingState(true, 'visible', true)).toBe(true);
    expect(shouldPollRoutingState(false, 'visible', true)).toBe(false);
    expect(shouldPollRoutingState(true, 'hidden', true)).toBe(false);
    expect(shouldPollRoutingState(true, 'visible', false)).toBe(false);
  });
});
