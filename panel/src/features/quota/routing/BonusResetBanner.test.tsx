import { beforeAll, describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import i18n from '@/i18n';
import { BonusResetBanner } from '@/features/quota/routing/BonusResetBanner';
import { BonusResetBadge } from '@/features/quota/routing/BonusResetBadge';
import type { RoutingAccount } from '@/services/api/routingState';
import { routingAccount } from '@/features/quota/routing/routingFixtures';

const NOW = new Date(2026, 9, 20, 13, 40, 0).getTime();

const render = (accounts: RoutingAccount[], labelFor?: (account: RoutingAccount) => string) =>
  renderToStaticMarkup(createElement(BonusResetBanner, { accounts, now: NOW, labelFor }));

const text = (markup: string) =>
  markup
    .replace(/<[^>]+>/g, ' ')
    .replace(/\s+/g, ' ')
    .trim();

beforeAll(async () => {
  await i18n.changeLanguage('en');
});

describe('BonusResetBanner', () => {
  test('hidden when no account has a reset available', () => {
    expect(render([])).toBe('');
    expect(render([routingAccount(), routingAccount({ authIndex: 'b' })])).toBe('');
    expect(render([routingAccount({ bonus: { known: false, available: 0, clears: [] } })])).toBe(
      ''
    );
  });

  test('lists every account with a reset and links to its row', () => {
    const expiresAt = new Date(2026, 9, 25, 9, 0, 0).toISOString();
    const markup = render(
      [
        routingAccount({
          authIndex: 'a',
          label: 'alice@example.com',
          bonus: { known: true, available: 1, clears: ['seven_day'], expiresAt },
        }),
        routingAccount({ authIndex: 'b', label: 'bob@example.com' }),
        routingAccount({
          authIndex: 'c',
          provider: 'codex',
          label: 'codex@example.com',
          bonus: { known: true, available: 2, clears: ['five_hour', 'seven_day'] },
        }),
      ],
      (account) => `name:${account.label}`
    );
    const content = text(markup);
    expect(content).toContain('name:alice@example.com');
    expect(content).toContain('name:codex@example.com');
    expect(content).not.toContain('bob@example.com');
    expect(content).toContain('1 reset available · clears 7-day · expires 10/25');
    expect(content).toContain('2 resets available · clears 5-hour, 7-day');
    expect(markup.match(/<li/g)).toHaveLength(2);
    expect(markup).toContain('href="#quota-account-a"');
    expect(markup).toContain('href="#quota-account-c"');
  });

  test('has no redeem action of its own', () => {
    const markup = render([
      routingAccount({ bonus: { known: true, available: 1, clears: ['seven_day'] } }),
    ]);
    expect(markup).not.toContain('<button');
    expect(markup).not.toContain('Reset limits');
    expect(markup).not.toContain('Reset quota');
  });
});

describe('BonusResetBadge', () => {
  const renderBadge = (bonus: RoutingAccount['bonus']) =>
    text(renderToStaticMarkup(createElement(BonusResetBadge, { bonus, now: NOW })));

  test('describes an available reset', () => {
    const expiresAt = new Date(2026, 9, 25, 9, 0, 0).toISOString();
    expect(renderBadge({ known: true, available: 1, clears: ['seven_day'], expiresAt })).toBe(
      '1 reset available · clears 7-day · expires 10/25'
    );
  });

  test('labels the Codex windows instead of showing raw keys', () => {
    const expiresAt = new Date(2026, 9, 25, 9, 0, 0).toISOString();
    expect(
      renderBadge({
        known: true,
        available: 1,
        clears: ['primary_window', 'secondary_window'],
        expiresAt,
      })
    ).toBe('1 reset available · clears 5-hour, Weekly · expires 10/25');
  });

  test('reads unknown when the reset-credit read failed', () => {
    expect(renderBadge({ known: false, available: 0, clears: [] })).toBe('resets: unknown');
  });

  test('renders nothing when known and none available', () => {
    expect(renderBadge({ known: true, available: 0, clears: [] })).toBe('');
  });
});

describe('redeem stays manual', () => {
  const read = (path: string) =>
    readFileSync(new URL(`../../../../${path}`, import.meta.url), 'utf8');

  test('Claude and Codex redeem paths go through a confirmation', () => {
    const claude = read('src/features/quota/providers/claude/ClaudeResetGrants.tsx');
    const codex = read('src/features/quota/hooks/useQuotaActions.ts');
    expect(claude).toContain('showConfirmation({');
    expect(codex).toContain('showConfirmation({');
    const banner = read('src/features/quota/routing/BonusResetBanner.tsx');
    expect(banner).not.toContain('resetGrantOperations');
    expect(banner).not.toContain('resetQuota');
  });
});
