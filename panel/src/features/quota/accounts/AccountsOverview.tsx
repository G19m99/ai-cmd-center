/**
 * The quota page for the routed providers: one panel each for Claude and
 * Codex, opening with a sentence on which account takes new requests and why,
 * then every account's usage. Reads only the server's routing state, which the
 * poller refreshes every 5 minutes, so it shows exactly what routing acts on.
 */

import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import type { RoutingAccount, RoutingState } from '@/services/api/routingState';
import type { AuthFileItem } from '@/types';
import { getQuotaCacheKey } from '@/utils/quota/identity';
import { AccountRow } from './AccountRow';
import {
  checkedText,
  headlineText,
  oldestFetchMs,
  providerHeadline,
  sortAccounts,
} from './accountView';
import styles from './Accounts.module.scss';

const PROVIDERS = ['claude', 'codex'] as const;

export interface AccountsOverviewProps {
  /** Null until the first read lands. */
  state: RoutingState | null;
  error: string;
  now: number;
  filesByIndex: ReadonlyMap<string, AuthFileItem>;
  canAct: boolean;
  checking: boolean;
  /** Cache key of the Codex credential whose bonus reset is being spent. */
  resettingName: string | null;
  onCheckNow: () => void;
  onRefreshAccount: (authIndex: string) => void;
  onCodexReset: (file: AuthFileItem) => void;
}

function ProviderPanel(
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
  const sorted = sortAccounts(accounts);

  return (
    <section className={styles.provider} aria-labelledby={headingId}>
      <header className={styles.providerHead}>
        <h2 id={headingId} className={styles.providerName}>
          {name}
        </h2>
        {accounts.length > 0 && (
          <span className={styles.count}>
            {t('quota_accounts.accounts_count', { count: accounts.length })}
          </span>
        )}
      </header>
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
          <ul className={styles.accounts}>
            {sorted.map((account, index) => {
              const file = filesByIndex.get(account.authIndex);
              return (
                <AccountRow
                  key={account.authIndex}
                  account={account}
                  file={file}
                  now={now}
                  locale={locale}
                  showColumnHeads={index === 0}
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
  const { state, error, now, canAct, checking, onCheckNow } = props;
  const { t, i18n } = useTranslation();
  const locale = i18n.resolvedLanguage;
  const accounts = state?.accounts ?? [];

  return (
    <div className={styles.overview}>
      <header className={styles.head}>
        <div className={styles.titles}>
          <h1 className={styles.title}>{t('quota_accounts.title')}</h1>
          <p className={styles.subtitle}>{t('quota_accounts.subtitle')}</p>
        </div>
        <div className={styles.headActions}>
          {state && (
            <span className={styles.checked}>{checkedText(t, oldestFetchMs(accounts), now)}</span>
          )}
          <Button
            variant="secondary"
            size="sm"
            onClick={onCheckNow}
            loading={checking}
            disabled={!canAct || checking}
          >
            {checking ? t('quota_accounts.checking') : t('quota_accounts.check_now')}
          </Button>
        </div>
      </header>

      {error && (
        <p className={styles.loadError} role="alert">
          {t('quota_accounts.load_error', { message: error })}
        </p>
      )}

      {state === null
        ? !error && <p className={styles.loading}>{t('quota_accounts.loading')}</p>
        : PROVIDERS.map((provider) => (
            <ProviderPanel
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
