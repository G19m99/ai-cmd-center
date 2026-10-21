/**
 * Account usage for the routed providers, Claude then Codex: a sentence on
 * which account takes new requests and why, then one card per account. Reads
 * only the server's routing state, which the poller refreshes every 5 minutes,
 * so it shows exactly what routing acts on. The dashboard supplies the heading.
 */

import { useTranslation } from 'react-i18next';
import type { RoutingAccount, RoutingState } from '@/services/api/routingState';
import type { AuthFileItem } from '@/types';
import { getQuotaCacheKey } from '@/utils/quota/identity';
import { AccountCard } from './AccountCard';
import { headlineText, providerHeadline, sortAccounts } from './accountView';
import styles from './Accounts.module.scss';

const PROVIDERS = ['claude', 'codex'] as const;

export interface AccountsOverviewProps {
  /** Null until the first read lands. */
  state: RoutingState | null;
  error: string;
  now: number;
  filesByIndex: ReadonlyMap<string, AuthFileItem>;
  canAct: boolean;
  /** Cache key of the Codex credential whose bonus reset is being spent. */
  resettingName: string | null;
  onRefreshAccount: (authIndex: string) => void;
  onCodexReset: (file: AuthFileItem) => void;
}

function ProviderGroup(
  props: AccountsOverviewProps & {
    provider: (typeof PROVIDERS)[number];
    accounts: RoutingAccount[];
    locale: string | undefined;
  }
) {
  const { provider, accounts, state, now, locale, filesByIndex } = props;
  const { t } = useTranslation();
  const name = t(`routing.provider_${provider}`);
  const headingId = `quota-provider-${provider}`;

  return (
    <section className={styles.provider} aria-labelledby={headingId}>
      <div className={styles.providerHead}>
        <h3 id={headingId} className={styles.providerName}>
          {name}
        </h3>
        {accounts.length > 0 && (
          <span className={styles.count}>
            {t('quota_accounts.accounts_count', { count: accounts.length })}
          </span>
        )}
      </div>
      {accounts.length === 0 ? (
        <p className={styles.empty}>
          {t(`quota_accounts.empty_${provider}`)}{' '}
          <a className={styles.emptyLink} href="#/oauth">
            {t('quota_accounts.empty_action')}
          </a>
        </p>
      ) : (
        <>
          <p className={styles.headline}>
            {headlineText(
              t,
              providerHeadline(accounts, state?.providers[provider]),
              name,
              now,
              locale
            )}
          </p>
          <ul className={styles.cards}>
            {sortAccounts(accounts).map((account) => {
              const file = filesByIndex.get(account.authIndex);
              return (
                <AccountCard
                  key={account.authIndex}
                  account={account}
                  file={file}
                  now={now}
                  locale={locale}
                  canAct={props.canAct}
                  resetting={file !== undefined && props.resettingName === getQuotaCacheKey(file)}
                  onRefresh={() => props.onRefreshAccount(account.authIndex)}
                  onCodexReset={props.onCodexReset}
                />
              );
            })}
          </ul>
        </>
      )}
    </section>
  );
}

export function AccountsOverview(props: AccountsOverviewProps) {
  const { state, error } = props;
  const { t, i18n } = useTranslation();
  const locale = i18n.resolvedLanguage;
  const accounts = state?.accounts ?? [];

  return (
    <div className={styles.overview}>
      {error && (
        <p className={styles.loadError} role="alert">
          {t('quota_accounts.load_error', { message: error })}
        </p>
      )}

      {state === null
        ? !error && <p className={styles.loading}>{t('quota_accounts.loading')}</p>
        : PROVIDERS.map((provider) => (
            <ProviderGroup
              key={provider}
              {...props}
              provider={provider}
              accounts={accounts.filter((account) => account.provider === provider)}
              locale={locale}
            />
          ))}
    </div>
  );
}
