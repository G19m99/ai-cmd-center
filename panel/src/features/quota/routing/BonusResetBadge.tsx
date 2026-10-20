/**
 * `1 reset available · clears 7-day · expires 10/25`, or `resets: unknown`
 * when the server's reset-credit read failed. Nothing when known and zero.
 *
 * Display only: redeeming stays on the card's existing confirm-gated button.
 */

import { useTranslation } from 'react-i18next';
import type { RoutingBonus } from '@/services/api/routingState';
import { describeBonus } from './routingFormat';
import styles from './Routing.module.scss';

export interface BonusResetBadgeProps {
  bonus: RoutingBonus;
  now: number;
}

export function BonusResetBadge({ bonus }: BonusResetBadgeProps) {
  const { t, i18n } = useTranslation();
  const text = describeBonus(t, bonus, i18n.resolvedLanguage);
  if (text === null) return null;
  return (
    <span className={styles.bonusBadge} data-known={bonus.known ? 'true' : 'false'}>
      {text}
    </span>
  );
}
