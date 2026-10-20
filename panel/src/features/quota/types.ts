/**
 * 额度渲染层的类型化样式契约。
 *
 * 额度 body 在两个宿主穿不同外衣：额度页（QuotaBody.module.scss）与
 * 认证文件卡片（AuthFileQuota.module.scss）。宿主通过 bindQuotaClasses
 * 把自己的 CSS Module 绑定成 QuotaClassMap —— 缺任何一个类名会在模块
 * 初始化时抛错并列出缺失清单，替代旧字符串 styleMap 的静默 class="undefined"。
 */

import type { ReactNode } from 'react';

export interface QuotaClassMap {
  // 额度行（五个提供商共用）
  quotaRow: string;
  quotaRowHeader: string;
  quotaModel: string;
  quotaMeta: string;
  quotaPercent: string;
  quotaReset: string;
  quotaResetRelative: string;
  quotaResetRelativeSoon: string;
  quotaAmount: string;
  quotaMessage: string;
  // 套餐 chip 行（codex 命名，claude/antigravity/kimi/xai 复用；
  // premium=金卡、elite=Pro 20x 液态铂金 —— 均为定稿资产，样式不可改）
  codexPlan: string;
  codexPlanItem: string;
  codexPlanLabel: string;
  codexPlanValue: string;
  premiumPlanValue: string;
  elitePlanValue: string;
  // Codex 重置积分
  codexResetCredits: string;
  codexResetCreditsTitle: string;
  codexResetCreditRow: string;
  codexResetCreditRowSoon: string;
  codexResetCreditLabel: string;
  codexResetCreditTime: string;
  codexResetCreditsError: string;
  // Antigravity 分组
  antigravityQuotaGroup: string;
  antigravityQuotaGroupHeader: string;
  antigravityQuotaGroupTitle: string;
  antigravityQuotaGroupDescription: string;
  // 水位条（QuotaMeter）
  quotaBar: string;
  quotaBarFill: string;
  quotaBarFillHigh: string;
  quotaBarFillMedium: string;
  quotaBarFillLow: string;
  // 行布局（额度页 Claude/Codex 行：窗口横排成列）。可选：认证文件卡片宿主不提供，
  // body 在缺失时退回纵向堆叠。
  quotaRowMeta?: string;
  quotaRowWindows?: string;
}

/** 可选类名：宿主提供则启用行布局，缺失不报错。 */
export const QUOTA_OPTIONAL_CLASS_KEYS: readonly ('quotaRowMeta' | 'quotaRowWindows')[] = [
  'quotaRowMeta',
  'quotaRowWindows',
];

export const QUOTA_CLASS_KEYS: readonly (keyof QuotaClassMap)[] = [
  'quotaRow',
  'quotaRowHeader',
  'quotaModel',
  'quotaMeta',
  'quotaPercent',
  'quotaReset',
  'quotaResetRelative',
  'quotaResetRelativeSoon',
  'quotaAmount',
  'quotaMessage',
  'codexPlan',
  'codexPlanItem',
  'codexPlanLabel',
  'codexPlanValue',
  'premiumPlanValue',
  'elitePlanValue',
  'codexResetCredits',
  'codexResetCreditsTitle',
  'codexResetCreditRow',
  'codexResetCreditRowSoon',
  'codexResetCreditLabel',
  'codexResetCreditTime',
  'codexResetCreditsError',
  'antigravityQuotaGroup',
  'antigravityQuotaGroupHeader',
  'antigravityQuotaGroupTitle',
  'antigravityQuotaGroupDescription',
  'quotaBar',
  'quotaBarFill',
  'quotaBarFillHigh',
  'quotaBarFillMedium',
  'quotaBarFillLow',
];

/** 宿主 CSS Module → 类型化契约。缺键即抛（fail-loud），`source` 用于报错定位。 */
export function bindQuotaClasses(module: Record<string, string>, source: string): QuotaClassMap {
  const missing = QUOTA_CLASS_KEYS.filter((key) => !module[key]);
  if (missing.length > 0) {
    throw new Error(`[quota] ${source} 缺少额度契约类名: ${missing.join(', ')}`);
  }
  const bound = {} as Record<keyof QuotaClassMap, string>;
  for (const key of QUOTA_CLASS_KEYS) {
    bound[key] = module[key];
  }
  for (const key of QUOTA_OPTIONAL_CLASS_KEYS) {
    if (module[key]) bound[key] = module[key];
  }
  return bound;
}

/** 'stack' = 纵向堆叠（默认）；'row' = 窗口横排成列（额度页 Claude/Codex 行）。 */
export type QuotaBodyLayout = 'stack' | 'row';

export interface QuotaBodyProps<TState> {
  quota: TState;
  classes: QuotaClassMap;
  /** Row layout needs `classes.quotaRowWindows`; without it the body stacks. */
  layout?: QuotaBodyLayout;
  /** Host-owned chips appended to the provider's plan row (`codexPlanItem` spans). */
  planItems?: ReactNode;
  /** Host-owned content placed right after the plan row, before usage rows. */
  afterPlan?: ReactNode;
}
