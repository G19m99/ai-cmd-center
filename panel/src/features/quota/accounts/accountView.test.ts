import { beforeAll, describe, expect, test } from 'bun:test';
import i18n from '@/i18n';
import type { RoutingWindow } from '@/services/api/routingState';
import { routingAccount } from '@/features/quota/routing/routingFixtures';
import {
  bonusText,
  checkedText,
  formatResetMoment,
  headlineText,
  orderWindows,
  providerHeadline,
  sortAccounts,
  usageLevel,
  windowName,
} from './accountView';

const t = (key: string, options?: Record<string, unknown>) => i18n.t(key, options) as string;

const at = (day: number, hour: number, minute: number, second = 0, ms = 0) =>
  new Date(2026, 9, day, hour, minute, second, ms).getTime();

// Wednesday 2026-10-21 1:00 PM local time.
const NOW = at(21, 13, 0);

const win = (overrides: Partial<RoutingWindow> = {}): RoutingWindow => ({
  key: 'seven_day',
  label: '7-day',
  utilization: 0,
  weekly: true,
  modelScope: '',
  routing: true,
  ...overrides,
});

beforeAll(async () => {
  await i18n.changeLanguage('en');
});

describe('windowName', () => {
  test('names Claude windows the way claude.ai does', () => {
    expect(windowName(t, win())).toBe('Weekly, all models');
    expect(
      windowName(t, win({ key: 'iguana_necktie', label: '7-day Fable', modelScope: 'fable' }))
    ).toBe('Weekly, Fable');
    expect(windowName(t, win({ key: 'five_hour', label: '5-hour', weekly: false }))).toBe(
      '5-hour session'
    );
  });

  test('names Codex windows, including code review and model-specific limits', () => {
    expect(windowName(t, win({ key: 'secondary_window', label: 'Weekly' }))).toBe(
      'Weekly, all models'
    );
    expect(windowName(t, win({ key: 'primary_window', label: '5-hour', weekly: false }))).toBe(
      '5-hour session'
    );
    expect(
      windowName(
        t,
        win({
          key: 'code_review_rate_limit.secondary_window',
          label: 'Code review Weekly',
          routing: false,
        })
      )
    ).toBe('Weekly, code review');
    expect(
      windowName(
        t,
        win({
          key: 'gpt-5-codex-mini:primary_window',
          label: 'GPT-5-Codex-Mini 5-hour',
          weekly: false,
          modelScope: 'gpt-5-codex-mini',
        })
      )
    ).toBe('5-hour session, GPT-5-Codex-Mini');
  });
});

describe('orderWindows', () => {
  test('all-model weekly first, then scoped weekly, then sessions, then non-routing', () => {
    const ordered = orderWindows([
      win({ key: 'five_hour', weekly: false }),
      win({ key: 'code_review_rate_limit.secondary_window', routing: false }),
      win({ key: 'iguana_necktie', modelScope: 'fable' }),
      win({ key: 'seven_day' }),
    ]);
    expect(ordered.map((window) => window.key)).toEqual([
      'seven_day',
      'iguana_necktie',
      'five_hour',
      'code_review_rate_limit.secondary_window',
    ]);
  });
});

describe('usageLevel', () => {
  test('80% and up is high, 100% is full', () => {
    expect(usageLevel(79.9)).toBe('normal');
    expect(usageLevel(80)).toBe('high');
    expect(usageLevel(99.5)).toBe('high');
    expect(usageLevel(100)).toBe('full');
  });
});

describe('formatResetMoment', () => {
  test('uses today, tomorrow, the weekday, then the date', () => {
    expect(formatResetMoment(t, at(21, 14, 40), NOW, 'en-US')).toBe('Today 2:40 PM');
    expect(formatResetMoment(t, at(22, 15, 0), NOW, 'en-US')).toBe('Tomorrow 3:00 PM');
    expect(formatResetMoment(t, at(24, 9, 5), NOW, 'en-US')).toBe('Sat 9:05 AM');
    expect(formatResetMoment(t, at(29, 13, 0), NOW, 'en-US')).toBe('Oct 29, 1:00 PM');
  });

  test('rounds to the nearest minute, so 12:59:59.864 reads 1:00 PM', () => {
    expect(formatResetMoment(t, at(22, 12, 59, 59, 864), NOW, 'en-US')).toBe('Tomorrow 1:00 PM');
  });

  test('has an inline form for sentences', () => {
    expect(formatResetMoment(t, at(22, 13, 0), NOW, 'en-US', 'inline')).toBe('tomorrow at 1:00 PM');
    expect(formatResetMoment(t, at(24, 9, 5), NOW, 'en-US', 'inline')).toBe('Sat at 9:05 AM');
  });
});

describe('sortAccounts', () => {
  test('in use first, then standby by rank, then limited, then unknown', () => {
    const sorted = sortAccounts([
      routingAccount({ authIndex: 'u', status: 'unknown' }),
      routingAccount({ authIndex: 'l', status: 'limited' }),
      routingAccount({ authIndex: 's3', status: 'standby', rank: 3 }),
      routingAccount({ authIndex: 'd', status: 'draining', rank: 1 }),
      routingAccount({ authIndex: 's2', status: 'standby', rank: 2 }),
    ]);
    expect(sorted.map((account) => account.authIndex)).toEqual(['d', 's2', 's3', 'l', 'u']);
  });
});

describe('providerHeadline and headlineText', () => {
  const weeklyResetsAt = new Date(at(22, 12, 59, 59, 864)).toISOString();

  test('names the account taking new requests, its weekly headroom and reset', () => {
    const headline = providerHeadline([
      routingAccount({ authIndex: 'b', status: 'standby', rank: 2 }),
      routingAccount({
        authIndex: 'a',
        label: 'gershy@example.com',
        status: 'draining',
        rank: 1,
        headroom: 78.4,
        weeklyResetsAt,
      }),
    ]);
    expect(headline).toEqual({
      kind: 'serving',
      label: 'gershy@example.com',
      urgent: false,
      only: false,
      left: 78,
      resetsAtMs: Date.parse(weeklyResetsAt),
    });
    expect(headlineText(t, headline, 'Claude', NOW, 'en-US')).toBe(
      'New requests go to gershy@example.com. It has 78% of its week left and resets first, ' +
        'tomorrow at 1:00 PM.'
    );
  });

  test('explains why open conversations move to an urgent account', () => {
    const headline = providerHeadline([
      routingAccount({
        label: 'a@example.com',
        status: 'draining',
        rank: 1,
        urgent: true,
        headroom: 78,
        weeklyResetsAt: new Date(at(22, 13, 0)).toISOString(),
      }),
      routingAccount({ authIndex: 'b', status: 'standby', rank: 2 }),
    ]);
    expect(headlineText(t, headline, 'Claude', NOW, 'en-US')).toBe(
      'New requests go to a@example.com. It resets tomorrow at 1:00 PM with 78% unused, ' +
        'so open conversations move to it too.'
    );
  });

  test('a single account has no "first" to compare against', () => {
    const headline = providerHeadline([
      routingAccount({ label: 'a@example.com', status: 'draining', rank: 1, headroom: 40 }),
    ]);
    expect(headlineText(t, headline, 'Codex', NOW, 'en-US')).toBe(
      'New requests go to a@example.com. It has 40% of its week left.'
    );
  });

  test('every account limited: says when the first one frees up', () => {
    const headline = providerHeadline(
      [
        routingAccount({ status: 'limited', freeAt: new Date(at(22, 15, 0)).toISOString() }),
        routingAccount({ authIndex: 'b', status: 'limited' }),
      ],
      { urgentAuthIndex: '', earliestFreeAt: new Date(at(21, 15, 30)).toISOString() }
    );
    expect(headline).toEqual({ kind: 'all_limited', freeAtMs: at(21, 15, 30) });
    expect(headlineText(t, headline, 'Claude', NOW, 'en-US')).toBe(
      'Every Claude account is at a limit. The first one frees up today at 3:30 PM.'
    );
  });

  test('nothing checked yet', () => {
    const headline = providerHeadline([routingAccount({ status: 'unknown' })]);
    expect(headline).toEqual({ kind: 'waiting' });
    expect(headlineText(t, headline, 'Claude', NOW, 'en-US')).toBe(
      'Waiting for the first usage check.'
    );
  });
});

describe('bonusText', () => {
  test('says how many, until when, and which limits a reset clears', () => {
    expect(
      bonusText(
        t,
        {
          known: true,
          available: 1,
          clears: ['five_hour', 'seven_day', 'seven_day_overage_included'],
          expiresAt: new Date(at(22, 12, 0)).toISOString(),
        },
        NOW,
        'en-US'
      )
    ).toBe(
      '1 bonus reset available until tomorrow at 12:00 PM. ' +
        'Using one clears the 5-hour session and weekly limits.'
    );
    expect(
      bonusText(t, { known: true, available: 2, clears: ['secondary_window'] }, NOW, 'en-US')
    ).toBe('2 bonus resets available. Using one clears the weekly limit.');
  });

  test('nothing to say when none are available; a failed read says so', () => {
    expect(bonusText(t, { known: true, available: 0, clears: [] }, NOW, 'en-US')).toBeNull();
    expect(bonusText(t, { known: false, available: 0, clears: [] }, NOW, 'en-US')).toBe(
      "Couldn't check for bonus resets."
    );
  });
});

describe('checkedText', () => {
  test('relative age of the oldest usage read', () => {
    expect(checkedText(t, NOW - 20_000, NOW)).toBe('Checked just now');
    expect(checkedText(t, NOW - 3 * 60_000, NOW)).toBe('Checked 3m ago');
    expect(checkedText(t, null, NOW)).toBe('Not checked yet');
  });
});
