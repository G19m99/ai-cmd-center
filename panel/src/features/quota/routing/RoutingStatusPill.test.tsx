import { beforeAll, describe, expect, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import i18n from '@/i18n';
import { RoutingStatusPill } from '@/features/quota/routing/RoutingStatusPill';
import type { RoutingAccount } from '@/services/api/routingState';
import { routingAccount } from '@/features/quota/routing/routingFixtures';
import { HOUR_MS } from '@/utils/time/durations';

// A fixed clock: 2026-10-20 13:40 local time.
const NOW = new Date(2026, 9, 20, 13, 40, 0).getTime();

const render = (account: RoutingAccount, now = NOW) =>
  renderToStaticMarkup(createElement(RoutingStatusPill, { account, now }));

const text = (markup: string) =>
  markup
    .replace(/<[^>]+>/g, ' ')
    .replace(/\s+/g, ' ')
    .trim();

beforeAll(async () => {
  await i18n.changeLanguage('en');
});

describe('RoutingStatusPill', () => {
  test('draining shows the rank', () => {
    const markup = render(routingAccount({ status: 'draining', rank: 1, sessions: 3 }));
    expect(markup).toContain('● Draining · #1');
    expect(markup).toContain('3 sessions');
    expect(markup).toContain('data-status="draining"');
  });

  test('standby shows the rank', () => {
    const markup = render(routingAccount({ status: 'standby', rank: 2, sessions: 1 }));
    expect(markup).toContain('Standby · #2');
    expect(markup).toContain('1 session');
    expect(markup).not.toContain('1 sessions');
  });

  test('limited shows the local free time and the limiting window', () => {
    const freeAt = new Date(2026, 9, 20, 15, 40, 0).toISOString();
    const markup = render(
      routingAccount({ status: 'limited', rank: 0, limitedBy: '5-hour', freeAt, sessions: 0 })
    );
    expect(text(markup)).toContain('Limited until 3:40 PM (5-hour)');
    expect(markup).toContain('0 sessions');
  });

  test('limited by cooldown and by disabled read as such', () => {
    const freeAt = new Date(2026, 9, 20, 15, 40, 0).toISOString();
    expect(
      text(render(routingAccount({ status: 'limited', limitedBy: 'cooldown', freeAt })))
    ).toContain('Limited until 3:40 PM (cooldown)');
    expect(text(render(routingAccount({ status: 'limited', limitedBy: 'disabled' })))).toContain(
      'Limited (disabled)'
    );
  });

  test('unknown quota', () => {
    expect(render(routingAccount({ status: 'unknown', rank: 2 }))).toContain('Quota unknown');
  });

  test('urgent draining shows the weekly countdown and headroom', () => {
    const weeklyResetsAt = new Date(NOW + 14 * HOUR_MS).toISOString();
    const markup = render(
      routingAccount({ status: 'draining', urgent: true, rank: 1, weeklyResetsAt, headroom: 38 })
    );
    expect(markup).toContain('⚡ Urgent: resets in 14h, 38% left');
    expect(markup).toContain('data-urgent="true"');
  });

  test('standby without a rank omits the position', () => {
    const markup = text(render(routingAccount({ status: 'standby', rank: 0 })));
    expect(markup).toContain('Standby');
    expect(markup).not.toContain('#0');
  });
});
