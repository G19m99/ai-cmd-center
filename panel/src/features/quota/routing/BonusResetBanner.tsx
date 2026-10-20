/**
 * Page-top banner listing every account with a bonus reset available.
 *
 * It only points at the row (`#quota-account-<authIndex>`), where the existing
 * redeem button lives behind its confirmation. This component has no redeem
 * action of its own and never triggers one.
 */

import type { MouseEvent } from 'react';
import { useTranslation } from 'react-i18next';
import type { RoutingAccount } from '@/services/api/routingState';
import { describeBonus, quotaAccountElementId } from './routingFormat';
import styles from './Routing.module.scss';

export interface BonusResetBannerProps {
  accounts: readonly RoutingAccount[];
  now: number;
  /** Display name for an account (masked or not); defaults to the server label. */
  labelFor?: (account: RoutingAccount) => string;
  /** Bring the account's row into view (switch tab/page first if needed). */
  onJump?: (account: RoutingAccount) => void;
}

export function BonusResetBanner({ accounts, labelFor, onJump }: BonusResetBannerProps) {
  const { t, i18n } = useTranslation();
  const withResets = accounts.filter(
    (account) => account.bonus.known && account.bonus.available > 0
  );
  if (withResets.length === 0) return null;

  const handleClick = (event: MouseEvent<HTMLAnchorElement>, account: RoutingAccount) => {
    // The app uses hash routing; a plain anchor jump would change the route.
    event.preventDefault();
    if (onJump) {
      onJump(account);
      return;
    }
    document.getElementById(quotaAccountElementId(account.authIndex))?.scrollIntoView({
      block: 'center',
      behavior: 'smooth',
    });
  };

  return (
    <section className={styles.banner} role="status" aria-live="polite">
      <div className={styles.bannerHead}>
        <span className={styles.bannerTitle}>{t('routing.banner_title')}</span>
        <span className={styles.bannerHint}>{t('routing.banner_hint')}</span>
      </div>
      <ul className={styles.bannerList}>
        {withResets.map((account) => (
          <li key={account.authIndex} className={styles.bannerItem}>
            <a
              className={styles.bannerLink}
              href={`#${quotaAccountElementId(account.authIndex)}`}
              onClick={(event) => handleClick(event, account)}
            >
              {labelFor ? labelFor(account) : account.label}
            </a>
            <span className={styles.bannerDetail}>
              {describeBonus(t, account.bonus, i18n.resolvedLanguage)}
            </span>
          </li>
        ))}
      </ul>
    </section>
  );
}
