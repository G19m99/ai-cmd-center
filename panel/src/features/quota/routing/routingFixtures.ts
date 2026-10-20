/**
 * Test fixtures for the routing-state feature.
 *
 * `ROUTING_STATE_SAMPLE` is the Task 5 server sample verbatim (reset-aware
 * strategy, two Claude accounts). Keep it in sync with
 * `server/internal/api/handlers/management/routing_state.go`.
 */

import type { RoutingAccount } from '@/services/api/routingState';

export const ROUTING_STATE_SAMPLE = {
  strategy: 'reset-aware',
  generated_at: '2026-10-20T15:51:14Z',
  providers: {
    claude: { urgent_auth_index: '8dbc2c9a3bc57d7c', earliest_free_at: '' },
    codex: { urgent_auth_index: '', earliest_free_at: '' },
  },
  accounts: [
    {
      auth_index: '8dbc2c9a3bc57d7c',
      provider: 'claude',
      label: 'alice@example.com',
      status: 'draining',
      urgent: true,
      rank: 1,
      sessions: 3,
      limited_by: '',
      free_at: '',
      weekly_resets_at: '2026-10-21T03:51:14Z',
      headroom: 38,
      quota: {
        fetched_at: '2026-10-20T15:51:14Z',
        stale: false,
        last_error: '',
        windows: [
          {
            key: 'five_hour',
            label: '5-hour',
            utilization: 28,
            resets_at: '2026-10-20T18:51:14Z',
            weekly: false,
            model_scope: '',
            routing: true,
          },
          {
            key: 'seven_day',
            label: '7-day',
            utilization: 62,
            resets_at: '2026-10-21T03:51:14Z',
            weekly: true,
            model_scope: '',
            routing: true,
          },
        ],
        bonus: { known: true, available: 1, clears: ['seven_day'], expires_at: '' },
      },
    },
    {
      auth_index: 'c0f0501ebd9fd741',
      provider: 'claude',
      label: 'bob@example.com',
      status: 'standby',
      urgent: false,
      rank: 2,
      sessions: 1,
      limited_by: '',
      free_at: '',
      weekly_resets_at: '2026-10-25T15:51:14Z',
      headroom: 90,
      quota: {
        fetched_at: '2026-10-20T15:51:14Z',
        stale: false,
        last_error: '',
        windows: [
          {
            key: 'seven_day',
            label: '7-day',
            utilization: 10,
            resets_at: '2026-10-25T15:51:14Z',
            weekly: true,
            model_scope: '',
            routing: true,
          },
        ],
        bonus: { known: true, available: 1, clears: ['seven_day'], expires_at: '' },
      },
    },
  ],
};

/** A normalized account with sane defaults; override what the case needs. */
export const routingAccount = (overrides: Partial<RoutingAccount> = {}): RoutingAccount => ({
  authIndex: 'idx-a',
  provider: 'claude',
  label: 'alice@example.com',
  status: 'standby',
  urgent: false,
  rank: 0,
  sessions: 0,
  limitedBy: '',
  headroom: 100,
  bonus: { known: true, available: 0, clears: [] },
  ...overrides,
});
