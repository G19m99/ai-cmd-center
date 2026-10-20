/**
 * Routing status for one account: `● Draining · #1`, `Standby · #2`,
 * `Limited until 3:40 PM (5-hour)`, `Quota unknown`, or, when the drain is
 * racing its weekly reset, `⚡ Urgent: resets in 14h, 38% left`. The session
 * count sits beside the pill.
 *
 * `now` is a prop so the parent's minute clock drives the countdown and tests
 * can pin it.
 */

import { useTranslation } from 'react-i18next';
import type { RoutingAccount } from '@/services/api/routingState';
import { parseIsoToMs } from '@/utils/quota';
import { formatClockTime, formatCompactDuration, limitedByLabel } from './routingFormat';
import styles from './Routing.module.scss';

export interface RoutingStatusPillProps {
  account: RoutingAccount;
  now: number;
}

export function RoutingStatusPill({ account, now }: RoutingStatusPillProps) {
  const { t, i18n } = useTranslation();
  const locale = i18n.resolvedLanguage;
  const ranked = account.rank > 0;

  let label: string;
  if (account.status === 'limited') {
    const freeAtMs = parseIsoToMs(account.freeAt);
    const by = account.limitedBy ? limitedByLabel(t, account.limitedBy) : '';
    if (freeAtMs !== null) {
      const time = formatClockTime(freeAtMs, now, locale);
      label = by
        ? t('routing.pill_limited_until_by', { time, by })
        : t('routing.pill_limited_until', { time });
    } else {
      label = by ? t('routing.pill_limited_by', { by }) : t('routing.pill_limited');
    }
  } else if (account.status === 'unknown') {
    label = t('routing.pill_unknown');
  } else if (account.status === 'draining' && account.urgent) {
    const resetMs = parseIsoToMs(account.weeklyResetsAt);
    label = t('routing.pill_urgent', {
      duration: resetMs === null ? '--' : formatCompactDuration(resetMs - now),
      headroom: Math.round(account.headroom),
    });
  } else if (account.status === 'draining') {
    label = ranked
      ? t('routing.pill_draining', { rank: account.rank })
      : t('routing.pill_draining_unranked');
  } else {
    label = ranked
      ? t('routing.pill_standby', { rank: account.rank })
      : t('routing.pill_standby_unranked');
  }

  return (
    <span className={styles.statusLine}>
      <span
        className={styles.pill}
        data-status={account.status}
        data-urgent={account.urgent ? 'true' : undefined}
      >
        {label}
      </span>
      <span className={styles.sessions}>{t('routing.sessions', { count: account.sessions })}</span>
    </span>
  );
}
