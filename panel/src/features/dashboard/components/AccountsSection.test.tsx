import { beforeAll, describe, expect, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import i18n from '@/i18n';
import { normalizeRoutingState } from '@/services/api/routingState';
import { AccountsSectionView } from './AccountsSection';

const minutesAgo = (minutes: number) =>
  new Date(Date.now() - minutes * 60_000 - 5_000).toISOString();
const inHours = (hours: number) => new Date(Date.now() + hours * 3_600_000).toISOString();

const STATE = normalizeRoutingState({
  strategy: 'reset-aware',
  accounts: [
    {
      auth_index: 'first',
      provider: 'claude',
      label: 'first@example.com',
      status: 'draining',
      rank: 1,
      headroom: 70,
      weekly_resets_at: inHours(20),
      quota: {
        fetched_at: minutesAgo(3),
        windows: [
          {
            key: 'seven_day',
            label: '7-day',
            utilization: 30,
            resets_at: inHours(20),
            weekly: true,
          },
        ],
      },
    },
    {
      auth_index: 'second',
      provider: 'claude',
      label: 'second@example.com',
      status: 'standby',
      rank: 2,
      headroom: 10,
      quota: {
        fetched_at: minutesAgo(3),
        windows: [
          {
            key: 'seven_day',
            label: '7-day',
            utilization: 90,
            resets_at: inHours(30),
            weekly: true,
          },
        ],
      },
    },
  ],
});

const text = (markup: string) =>
  markup
    .replace(/<[^>]+>/g, ' ')
    .replace(/&#x27;/g, "'")
    .replace(/\s+/g, ' ')
    .trim();

beforeAll(async () => {
  await i18n.changeLanguage('en');
});

// Static rendering reads a zustand store's initial state, so the view is
// rendered with its props directly; AccountsSection only wires hooks to it.
describe('AccountsSectionView', () => {
  const render = () =>
    renderToStaticMarkup(
      createElement(AccountsSectionView, {
        state: STATE,
        error: '',
        now: Date.now(),
        filesByIndex: new Map(),
        canAct: true,
        checking: false,
        resettingName: null,
        onCheckNow: () => {},
        onRefreshAccount: () => {},
        onCodexReset: () => {},
      })
    );

  test('heads the account cards with when usage was checked and a way to check now', () => {
    const page = text(render());
    expect(page).toContain('Account usage');
    expect(page).toContain('Usage for each connected account, checked every 5 minutes.');
    expect(page).toContain('Checked 3m ago');
    expect(page).toContain('Check now');
  });

  test('lists each account as a card under its provider', () => {
    const markup = render();
    expect(text(markup)).toContain('Claude 2 accounts');
    expect(markup.match(/<li[^>]*data-status="(draining|standby)"/g)).toHaveLength(2);
    expect(markup.indexOf('>first@example.com<')).toBeLessThan(
      markup.indexOf('>second@example.com<')
    );
  });
});
