/**
 * Plan label for the account cell of a Claude/Codex row, resolved the same
 * way the provider bodies label it so the two can never disagree. React-free.
 */

import type { ClaudeQuotaState, CodexQuotaState } from '@/types';
import { normalizePlanType, PREMIUM_CODEX_PLAN_TYPES, resolvePlanTier } from '@/utils/quota';
import type { QuotaCardState } from '../providers';
import type { QuotaProviderType } from '../providers/types';
import type { QuotaClassMap } from '../types';
import type { Translate } from './routingFormat';

/** Codex plan name (`Plus`, `Pro 200`, …); null when the plan is unknown. */
export function resolveCodexPlanLabel(t: Translate, planType?: string | null): string | null {
  const normalized = normalizePlanType(planType);
  if (!normalized) return null;
  if (normalized === 'self_serve_business_prolite') return t('codex_quota.plan_business_premium');
  if (normalized === 'pro') return t('codex_quota.plan_pro');
  if (PREMIUM_CODEX_PLAN_TYPES.has(normalized) && normalized !== 'pro') {
    return t('codex_quota.plan_prolite');
  }
  if (normalized === 'plus') return t('codex_quota.plan_plus');
  if (normalized === 'team') return t('codex_quota.plan_team');
  if (normalized === 'free') return t('codex_quota.plan_free');
  return planType || normalized;
}

/** elite = Pro 20x liquid platinum, premium = gold card, else the plain value class. */
export function resolveCodexPlanValueClass(
  planType: string | null | undefined,
  classes: QuotaClassMap
): string {
  const tier = resolvePlanTier(planType);
  if (tier === 'elite') return classes.elitePlanValue;
  if (tier === 'premium') return classes.premiumPlanValue;
  return classes.codexPlanValue;
}

/** Claude plan name from its normalized `plan_*` key; null when unknown. */
export function resolveClaudePlanLabel(t: Translate, planType?: string | null): string | null {
  return planType ? t(`claude_quota.${planType}`) : null;
}

export interface AccountPlan {
  label: string;
  /** Class for the value span; carries the Codex premium/elite badge styling. */
  valueClass: string;
}

/** Plan for the account cell; null for other providers or before quota loads. */
export function resolveAccountPlan(
  t: Translate,
  type: QuotaProviderType,
  quota: QuotaCardState | undefined,
  classes: QuotaClassMap
): AccountPlan | null {
  if (!quota || quota.status !== 'success') return null;
  if (type === 'claude') {
    const label = resolveClaudePlanLabel(t, (quota as ClaudeQuotaState).planType);
    return label ? { label, valueClass: classes.codexPlanValue } : null;
  }
  if (type === 'codex') {
    const planType = (quota as CodexQuotaState).planType ?? null;
    const label = resolveCodexPlanLabel(t, planType);
    return label ? { label, valueClass: resolveCodexPlanValueClass(planType, classes) } : null;
  }
  return null;
}
