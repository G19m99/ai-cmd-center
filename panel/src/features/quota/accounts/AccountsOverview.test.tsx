import { beforeAll, describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import i18n from '@/i18n';
import { normalizeRoutingState, type RoutingState } from '@/services/api/routingState';
import { AccountsOverview, type AccountsOverviewProps } from './AccountsOverview';

const at = (day: number, hour: number, minute: number, second = 0, ms = 0) =>
  new Date(2026, 9, day, hour, minute, second, ms).toISOString();

// Wednesday 2026-10-21 1:00 PM local time.
const NOW = new Date(2026, 9, 21, 13, 0).getTime();

// The two accounts as the server reported them, re-based onto local time.
const STATE = normalizeRoutingState({
  strategy: 'reset-aware',
  generated_at: at(21, 13, 0),
  providers: { claude: { urgent_auth_index: '', earliest_free_at: '' } },
  accounts: [
    {
      auth_index: 'second',
      provider: 'claude',
      label: 'gershy_claude@example.com',
      status: 'standby',
      rank: 2,
      sessions: 0,
      weekly_resets_at: at(22, 15, 0),
      headroom: 7,
      quota: {
        fetched_at: at(21, 12, 57),
        windows: [
          { key: 'five_hour', label: '5-hour', utilization: 18, resets_at: at(21, 14, 40) },
          {
            key: 'seven_day',
            label: '7-day',
            utilization: 93,
            resets_at: at(22, 15, 0),
            weekly: true,
          },
          {
            key: 'iguana_necktie',
            label: '7-day Fable',
            utilization: 100,
            resets_at: at(22, 15, 0),
            weekly: true,
            model_scope: 'fable',
          },
        ],
        bonus: { known: true, available: 0, clears: [] },
      },
    },
    {
      auth_index: 'first',
      provider: 'claude',
      label: 'gershy@example.com',
      status: 'draining',
      rank: 1,
      sessions: 2,
      weekly_resets_at: at(22, 12, 59, 59, 864),
      headroom: 78,
      quota: {
        fetched_at: at(21, 12, 57),
        windows: [
          { key: 'five_hour', label: '5-hour', utilization: 0, resets_at: '' },
          {
            key: 'seven_day',
            label: '7-day',
            utilization: 22,
            resets_at: at(22, 12, 59, 59, 864),
            weekly: true,
          },
          {
            key: 'iguana_necktie',
            label: '7-day Fable',
            utilization: 33,
            resets_at: at(22, 12, 59, 59, 864),
            weekly: true,
            model_scope: 'fable',
          },
        ],
        bonus: {
          known: true,
          available: 1,
          clears: ['five_hour', 'seven_day', 'seven_day_overage_included'],
          expires_at: at(22, 12, 0),
        },
      },
    },
  ],
}) as RoutingState;

const props = (overrides: Partial<AccountsOverviewProps> = {}): AccountsOverviewProps => ({
  state: STATE,
  error: '',
  now: NOW,
  filesByIndex: new Map(),
  canAct: true,
  resettingName: null,
  onRefreshAccount: () => {},
  onCodexReset: () => {},
  ...overrides,
});

const render = (overrides: Partial<AccountsOverviewProps> = {}) =>
  renderToStaticMarkup(createElement(AccountsOverview, props(overrides)));

const text = (markup: string) =>
  markup
    .replace(/<[^>]+>/g, ' ')
    .replace(/&#x27;/g, "'")
    .replace(/&amp;/g, '&')
    .replace(/\s+/g, ' ')
    .trim();

beforeAll(async () => {
  await i18n.changeLanguage('en');
});

describe('AccountsOverview', () => {
  test('says where new requests go and why, in one sentence', () => {
    expect(text(render())).toContain(
      'New requests go to gershy@example.com. It has 78% of its week left and resets first, ' +
        'tomorrow at 1:00 PM.'
    );
  });

  test('shows real account emails, the account in use first', () => {
    const markup = render();
    expect(markup).not.toContain('•••');
    const first = markup.indexOf('>gershy@example.com<');
    const second = markup.indexOf('>gershy_claude@example.com<');
    expect(first).toBeGreaterThan(-1);
    expect(second).toBeGreaterThan(first);
  });

  test('reports usage as percent used with named limits and reset times', () => {
    const page = text(render());
    expect(page).toContain('Weekly, all models 93% Resets tomorrow at 3:00 PM in 1d 2h');
    expect(page).toContain('Weekly, Fable 100% Resets tomorrow at 3:00 PM in 1d 2h');
    expect(page).toContain('5-hour session 18% Resets today at 2:40 PM in 1h 40m');
    expect(page).toContain('Weekly, all models 22% Resets tomorrow at 1:00 PM in 23h 59m');
    expect(page).toContain('5-hour session 0% Not started');
  });

  test('gives every account its own card', () => {
    const markup = render();
    expect(markup.match(/<li[^>]*data-status="(draining|standby)"/g)).toHaveLength(2);
    expect(markup).not.toContain('<table');
    expect(markup).not.toContain('<h1');
  });

  test('marks how close each limit is', () => {
    const markup = render();
    expect(markup).toMatch(/<li data-level="high">(?:(?!<\/li>)[\s\S])*Weekly, all models/);
    expect(markup).toMatch(/<li data-level="full">(?:(?!<\/li>)[\s\S])*Weekly, Fable/);
  });

  test('status and open conversations per account', () => {
    const page = text(render());
    expect(page).toContain('gershy@example.com In use 2 open conversations');
    expect(page).toContain('gershy_claude@example.com Standby');
  });

  test('a bonus reset is described on its own account', () => {
    expect(text(render())).toContain(
      '1 bonus reset available until tomorrow at 12:00 PM. ' +
        'Using one clears the 5-hour session and weekly limits.'
    );
  });

  test('a provider without accounts points to OAuth Login', () => {
    const markup = render();
    expect(text(markup)).toContain('No ChatGPT account connected yet.');
    expect(markup).toContain('href="#/oauth"');
  });

  test('a failed check is shown on the account it belongs to', () => {
    const failing = normalizeRoutingState({
      accounts: [
        {
          auth_index: 'x',
          provider: 'claude',
          label: 'x@example.com',
          status: 'unknown',
          quota: { stale: true, last_error: 'usage endpoint returned 500', windows: [] },
        },
      ],
    }) as RoutingState;
    const page = text(render({ state: failing }));
    expect(page).toContain('x@example.com Usage unknown');
    expect(page).toContain('The last check failed: usage endpoint returned 500');
    expect(page).toContain('Waiting for the first usage check.');
  });

  test('the bonus button opens the confirmation without reading Anthropic first', () => {
    // Anthropic rate-limits the usage endpoint the grants live on, and the
    // server already polls it; the only read is the check right before the claim.
    // Static rendering cannot model a connected session (zustand serves its initial
    // state), so the wiring is checked in the source; the hook's own test pins
    // that confirming needs no prior read.
    const file = { name: 'claude-1-gershy@example.com.json', auth_index: 'first', type: 'claude' };
    const markup = render({ filesByIndex: new Map([['first', file]]) });
    expect(markup).toMatch(/<button[^>]*>(?:(?!<\/button>)[\s\S])*Use bonus reset/);
    const row = readFileSync(new URL('./AccountCard.tsx', import.meta.url), 'utf8');
    expect(row).toMatch(/useClaudeResetGrants\(\s*props\.file,\s*false,/);
    expect(row).toContain('disabled={reset.blocked}');
    expect(row).toContain('onClick={reset.confirm}');
  });

  test('no keyframes name camel-cases into a class name of the same module', () => {
    // CSS Modules export `meter-fill` as `meterFill`, silently replacing a `.meterFill` class.
    const scss = readFileSync(new URL('./Accounts.module.scss', import.meta.url), 'utf8');
    const classes = new Set([...scss.matchAll(/\.([a-zA-Z][\w-]*)/g)].map((match) => match[1]));
    const keyframes = [...scss.matchAll(/@keyframes\s+([\w-]+)/g)].map((match) => match[1]);
    expect(keyframes.length).toBeGreaterThan(0);
    for (const name of keyframes) {
      const camel = name.replace(/-([a-z])/g, (_, letter: string) => letter.toUpperCase());
      expect(classes.has(camel)).toBe(false);
    }
  });

  test('loading and load errors', () => {
    expect(text(render({ state: null }))).toContain('Loading usage…');
    expect(text(render({ state: null, error: 'boom' }))).toContain("Couldn't load usage: boom");
  });
});
