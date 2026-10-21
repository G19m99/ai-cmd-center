/**
 * One account in a provider panel: who it is, whether it takes new requests,
 * each usage window as percent used with its reset, and a banked bonus reset
 * with the button that spends it.
 */

import { useEffect, useState, type CSSProperties } from 'react';
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
  resetCell,
  usageLevel,
  windowName,
} from './accountView';
import styles from './Accounts.module.scss';

export interface AccountRowProps {
  account: RoutingAccount;
  /** The account's credential; needed to spend a bonus reset. */
  file: AuthFileItem | undefined;
  now: number;
  locale: string | undefined;
  /** Column headings are visible on the first row of a panel only. */
  showColumnHeads: boolean;
  canAct: boolean;
  resetting: boolean;
  onRefresh: () => void;
  onCodexReset: (file: AuthFileItem) => void;
}

/**
 * Reading the grants calls the same Anthropic usage endpoint the server already
 * polls, so it waits for a click instead of running on every page load. The
 * click reads once, then opens the usual confirmation; nothing is spent without it.
 */
function ClaudeBonusAction(props: {
  file: AuthFileItem;
  disabled: boolean;
  onRefresh: () => void;
}) {
  const { t } = useTranslation();
  // Number of reads asked for by clicks; 0 keeps the hook from reading at all.
  const [request, setRequest] = useState(0);
  const [waitingFor, setWaitingFor] = useState<number | null>(null);
  const reset = useClaudeResetGrants(
    props.file,
    request > 0,
    props.disabled,
    request,
    props.onRefresh
  );
  const { blocked, confirm, readFor } = reset;

  useEffect(() => {
    if (waitingFor === null || readFor !== waitingFor) return;
    setWaitingFor(null);
    if (!blocked) confirm();
  }, [waitingFor, readFor, blocked, confirm]);

  const checking = waitingFor !== null;
  const start = () => {
    // A grant already read (or an unfinished claim to retry) goes straight to the confirmation.
    if (!blocked) {
      confirm();
      return;
    }
    const next = request + 1;
    setRequest(next);
    setWaitingFor(next);
  };
  const empty = !checking && readFor !== undefined && reset.count === 0 && !reset.message;

  return (
    <>
      <Button
        variant="secondary"
        size="sm"
        disabled={props.disabled || reset.busy || checking}
        loading={reset.busy || checking}
        onClick={start}
      >
        {reset.buttonLabel === 'retry' ? t('claude_reset.retry') : t('quota_accounts.bonus_use')}
      </Button>
      {(reset.message || empty) && (
        <p className={styles.bonusNote} role="status">
          {t(`claude_reset.${reset.message || 'empty'}`)}
        </p>
      )}
    </>
  );
}

export function AccountRow(props: AccountRowProps) {
  const { account, file, now, locale, showColumnHeads, canAct, resetting } = props;
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

  return (
    <li
      className={styles.account}
      data-status={account.status}
      id={quotaAccountElementId(account.authIndex)}
    >
      <div className={styles.accountHead}>
        <span className={styles.dot} aria-hidden="true" />
        <span className={styles.email}>{account.label}</span>
        <span className={styles.status}>{status}</span>
        {account.sessions > 0 && (
          <span className={styles.sessions}>
            {t('quota_accounts.sessions', { count: account.sessions })}
          </span>
        )}
      </div>

      {problem && <p className={styles.problem}>{problem}</p>}

      {windows.length > 0 && (
        <table className={styles.limits}>
          <thead className={showColumnHeads ? undefined : styles.srOnly}>
            <tr>
              <th scope="col">{t('quota_accounts.column_limit')}</th>
              <th scope="col">{t('quota_accounts.column_used')}</th>
              <th scope="col">{t('quota_accounts.column_resets')}</th>
            </tr>
          </thead>
          <tbody>
            {windows.map((window) => {
              const used = Math.round(Math.max(0, Math.min(100, window.utilization)));
              const reset = resetCell(t, window, now, locale);
              return (
                <tr key={window.key} data-level={usageLevel(window.utilization)}>
                  <th scope="row">{windowName(t, window)}</th>
                  <td className={styles.usedCell}>
                    <div className={styles.used}>
                      <span className={styles.meter} aria-hidden="true">
                        <span
                          className={styles.meterFill}
                          style={{ '--used': `${used}%` } as CSSProperties}
                        />
                      </span>
                      <span className={styles.percent}>{used}%</span>
                    </div>
                  </td>
                  <td className={styles.resetCell}>
                    <span>{reset.moment}</span>
                    {reset.relative && <span className={styles.relative}>{reset.relative}</span>}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
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
