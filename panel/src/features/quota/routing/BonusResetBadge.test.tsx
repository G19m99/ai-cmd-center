import { beforeAll, describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import i18n from '@/i18n';
import { BonusResetBadge } from '@/features/quota/routing/BonusResetBadge';
import type { RoutingAccount } from '@/services/api/routingState';

const NOW = new Date(2026, 9, 20, 13, 40, 0).getTime();

const text = (markup: string) =>
  markup
    .replace(/<[^>]+>/g, ' ')
    .replace(/\s+/g, ' ')
    .trim();

beforeAll(async () => {
  await i18n.changeLanguage('en');
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
  });

  test('the accounts list spends a reset only through those confirmed paths', () => {
    const row = read('src/features/quota/accounts/AccountRow.tsx');
    expect(row).not.toContain('resetGrantOperations');
    // The Claude button only ever reaches the hook's confirm(), which opens the dialog.
    expect(row).toContain('const { blocked, confirm, readFor } = reset;');
    expect(row).toContain('onClick={start}');
    expect(row).toContain('onClick={() => props.onCodexReset(file)}');
    const page = read('src/features/quota/QuotaPage.tsx');
    expect(page).toContain('resetQuota(file, QUOTA_ADAPTERS.codex)');
  });
});
