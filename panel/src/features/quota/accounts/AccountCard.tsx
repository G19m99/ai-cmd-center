/**
 * One account as a card: who it is, whether it takes new requests, each usage
 * window as percent used with its reset, and a banked bonus reset with the
 * button that spends it.
 */

import type { CSSProperties } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import type { RoutingAccount } from '@/services/api/routingState';
import type { AuthFileItem } from '@/types';
import { parseIsoToMs } from '@/utils/quota';
import { useClaudeResetGrants } from '../providers/claude/ClaudeResetGrants';
import { formatCompactDuration, quotaAccountElementId } from '../routing/routingFormat';
import {
  bonusText,
  formatResetMoment,
  orderWindows,
  resetLine,
  usageLevel,
  windowName,
} from './accountView';
import styles from './Accounts.module.scss';

export interface AccountCardProps {
  account: RoutingAccount;
  /** The account's credential; needed to spend a bonus reset. */
  file: AuthFileItem | undefined;
  now: number;
  locale: string | undefined;
  canAct: boolean;
  resetting: boolean;
  onRefresh: () => void;
  onCodexReset: (file: AuthFileItem) => void;
}

/**
 * The grants live on the same Anthropic usage endpoint the server already
 * polls, and Anthropic rate-limits it. So nothing is read up front: the click
 * opens the usual confirmation, and the only read is the eligibility check right
 * before the claim, which also picks the grant. Nothing is spent without the
 * confirmation.
 */
function ClaudeBonusAction(props: {
  file: AuthFileItem;
  disabled: boolean;
  onRefresh: () => void;
}) {
  const { t } = useTranslation();
  const reset = useClaudeResetGrants(props.file, false, props.disabled, null, props.onRefresh);

  return (
    <>
      <Button
        variant="secondary"
        size="sm"
        disabled={reset.blocked}
        loading={reset.busy}
        onClick={reset.confirm}
      >
        {reset.buttonLabel === 'retry' ? t('claude_reset.retry') : t('quota_accounts.bonus_use')}
      </Button>
      {reset.message && (
        <p className={styles.bonusNote} role="status">
          {t(`claude_reset.${reset.message}`)}
        </p>
      )}
    </>
  );
}

export function AccountCard(props: AccountCardProps) {
  const { account, file, now, locale, canAct, resetting } = props;
  const { t } = useTranslation();
  const windows = orderWindows(account.windows);

  const freeAtMs = parseIsoToMs(account.freeAt);
  const status =
    account.status === 'limited'
      ? freeAtMs === null
        ? t('quota_accounts.status_limited')
        : t('quota_accounts.status_limited_until', {
            time: formatResetMoment(t, freeAtMs, now, locale, 'inline'),
          })
      : t(`quota_accounts.status_${account.status}`);

  const fetchedMs = parseIsoToMs(account.fetchedAt);
  const problem = account.lastError
    ? t('quota_accounts.stale_error', { error: account.lastError })
    : account.stale && fetchedMs !== null
      ? t('quota_accounts.stale_old', { duration: formatCompactDuration(now - fetchedMs) })
      : null;

  const bonus = bonusText(t, account.bonus, now, locale);
  const canSpendBonus = account.bonus.known && account.bonus.available > 0 && file !== undefined;

  const elementId = quotaAccountElementId(account.authIndex);

  return (
    <li
      className={styles.card}
      data-status={account.status}
      id={elementId}
      aria-labelledby={`${elementId}-name`}
    >
      <div className={styles.cardHead}>
        <span className={styles.email} id={`${elementId}-name`}>
          {account.label}
        </span>
        <span className={styles.status}>{status}</span>
      </div>
      {account.sessions > 0 && (
        <p className={styles.sessions}>
          {t('quota_accounts.sessions', { count: account.sessions })}
        </p>
      )}

      {problem && <p className={styles.problem}>{problem}</p>}

      {windows.length > 0 && (
        <ul className={styles.limits}>
          {windows.map((window) => {
            const used = Math.round(Math.max(0, Math.min(100, window.utilization)));
            const reset = resetLine(t, window, now, locale);
            return (
              <li key={window.key} data-level={usageLevel(window.utilization)}>
                <span className={styles.limitName}>{windowName(t, window)}</span>
                <span className={styles.percent}>{used}%</span>
                <span className={styles.meter} aria-hidden="true">
                  <span
                    className={styles.meterFill}
                    style={{ '--used': `${used}%` } as CSSProperties}
                  />
                </span>
                <span className={styles.reset}>
                  {reset.text}
                  {reset.relative && <span className={styles.relative}>{reset.relative}</span>}
                </span>
              </li>
            );
          })}
        </ul>
      )}

      {bonus && (
        <div className={styles.bonus} data-known={account.bonus.known}>
          <p className={styles.bonusText}>{bonus}</p>
          {canSpendBonus && account.provider === 'claude' && (
            <ClaudeBonusAction file={file} disabled={!canAct} onRefresh={props.onRefresh} />
          )}
          {canSpendBonus && account.provider === 'codex' && (
            <Button
              variant="secondary"
              size="sm"
              disabled={!canAct}
              loading={resetting}
              onClick={() => props.onCodexReset(file)}
            >
              {t('quota_accounts.bonus_use')}
            </Button>
          )}
        </div>
      )}
    </li>
  );
}
