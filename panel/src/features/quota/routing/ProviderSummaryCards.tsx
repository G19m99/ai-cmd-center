/**
 * Theo-style provider summary: per provider, account count, pooled weekly
 * headroom ("409% of 500%") and the next weekly reset. Hosts the "Show emails"
 * toggle that unmasks account names on the rows below.
 */

import { useTranslation } from 'react-i18next';
import { IconEye, IconEyeOff } from '@/components/ui/icons';
import type { RoutingState } from '@/services/api/routingState';
import { buildResetDisplay } from '@/utils/quota';
import { QuotaResetLabel } from '../components/QuotaResetLabel';
import type { QuotaClassMap } from '../types';
import { summarizeProvider } from './routingSummary';
import styles from './Routing.module.scss';

const SUMMARY_PROVIDERS = ['claude', 'codex'] as const;

export interface ProviderSummaryCardsProps {
  state: RoutingState | null;
  /** Credentials known to the panel per provider (routing may lag or be absent). */
  counts: Partial<Record<(typeof SUMMARY_PROVIDERS)[number], number>>;
  now: number;
  classes: QuotaClassMap;
  showEmails: boolean;
  onToggleEmails: (value: boolean) => void;
}

export function ProviderSummaryCards(props: ProviderSummaryCardsProps) {
  const { state, counts, now, classes, showEmails, onToggleEmails } = props;
  const { t, i18n } = useTranslation();
  const visible = SUMMARY_PROVIDERS.filter((provider) => (counts[provider] ?? 0) > 0);
  if (visible.length === 0) return null;

  return (
    <section className={styles.summary} aria-label={t('routing.summary_label')}>
      <div className={styles.summaryCards}>
        {visible.map((provider) => {
          const summary = summarizeProvider(state?.accounts ?? [], provider, now);
          const known = state !== null && summary.accounts > 0;
          const reset = known
            ? buildResetDisplay(null, summary.nextWeeklyResetMs, now, i18n.resolvedLanguage)
            : null;
          return (
            <article key={provider} className={styles.summaryCard} data-provider={provider}>
              <div className={styles.summaryHead}>
                <span className={styles.summaryProvider}>{t(`routing.provider_${provider}`)}</span>
                <span className={styles.summaryCount}>
                  {t('routing.summary_accounts', { count: counts[provider] ?? 0 })}
                </span>
              </div>
              <div className={styles.summaryHeadroom}>
                {known
                  ? t('routing.summary_headroom', {
                      total: summary.headroomTotal,
                      max: summary.headroomMax,
                    })
                  : '--'}
              </div>
              <div className={styles.summaryReset}>
                <span className={styles.summaryResetLabel}>{t('routing.summary_next_reset')}</span>
                {reset ? (
                  <span className={classes.quotaMeta}>
                    <QuotaResetLabel display={reset} classes={classes} />
                  </span>
                ) : (
                  <span className={classes.quotaReset}>--</span>
                )}
              </div>
            </article>
          );
        })}
      </div>
      <div className={styles.summaryAside}>
        {state && (
          <span className={styles.summaryStrategy}>
            {t('routing.summary_strategy', { strategy: state.strategy })}
          </span>
        )}
        <button
          type="button"
          className={styles.toggleEmails}
          aria-pressed={showEmails}
          onClick={() => onToggleEmails(!showEmails)}
        >
          {showEmails ? (
            <IconEyeOff size={13} aria-hidden="true" />
          ) : (
            <IconEye size={13} aria-hidden="true" />
          )}
          {t(showEmails ? 'routing.hide_emails' : 'routing.show_emails')}
        </button>
      </div>
    </section>
  );
}
