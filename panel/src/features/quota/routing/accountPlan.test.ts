import { beforeAll, describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import i18n from '@/i18n';
import { resolveAccountPlan } from '@/features/quota/routing/accountPlan';
import { bindQuotaClasses, QUOTA_CLASS_KEYS } from '@/features/quota/types';
import type { ClaudeQuotaState, CodexQuotaState } from '@/types';

const classes = bindQuotaClasses(
  Object.fromEntries(QUOTA_CLASS_KEYS.map((key) => [key, key])),
  'test-host'
);
const t = (key: string, options?: Record<string, unknown>) => i18n.t(key, options);
const read = (path: string) =>
  readFileSync(new URL(`../../../../${path}`, import.meta.url), 'utf8');

beforeAll(async () => {
  await i18n.changeLanguage('en');
});

describe('resolveAccountPlan', () => {
  test('labels Claude plans from the normalized key', () => {
    const quota: ClaudeQuotaState = { status: 'success', windows: [], planType: 'plan_max' };
    expect(resolveAccountPlan(t, 'claude', quota, classes)).toEqual({
      label: 'Max',
      valueClass: 'codexPlanValue',
    });
  });

  test('labels Codex plans and carries the tier badge class', () => {
    const plus: CodexQuotaState = { status: 'success', windows: [], planType: 'plus' };
    expect(resolveAccountPlan(t, 'codex', plus, classes)).toEqual({
      label: 'Plus',
      valueClass: 'codexPlanValue',
    });
    const pro: CodexQuotaState = { status: 'success', windows: [], planType: 'pro' };
    expect(resolveAccountPlan(t, 'codex', pro, classes)?.label).toBe('Pro 200');
    expect(resolveAccountPlan(t, 'codex', pro, classes)?.valueClass).toBe('elitePlanValue');
  });

  test('is null before quota loads, without a plan, and for other providers', () => {
    expect(resolveAccountPlan(t, 'claude', undefined, classes)).toBeNull();
    expect(resolveAccountPlan(t, 'claude', { status: 'loading', windows: [] }, classes)).toBeNull();
    expect(resolveAccountPlan(t, 'codex', { status: 'success', windows: [] }, classes)).toBeNull();
    expect(resolveAccountPlan(t, 'kimi', { status: 'success' }, classes)).toBeNull();
  });
});

describe('plan lives in the account cell for rows', () => {
  test('the card renders the plan and the bodies skip it in row layout', () => {
    const card = read('src/features/quota/components/QuotaCard.tsx');
    expect(card).toContain('resolveAccountPlan(');
    expect(card).toContain('styles.planLine');
    const claude = read('src/features/quota/providers/claude/ClaudeQuotaBody.tsx');
    expect(claude).toContain('const showPlan = Boolean(planType) && !rowLayout;');
    const codex = read('src/features/quota/providers/codex/CodexQuotaBody.tsx');
    expect(codex).toContain('const showPlan = Boolean(planLabel) && !rowLayout;');
    expect(codex).toContain('resolveCodexPlanLabel(t, planType)');
  });
});
