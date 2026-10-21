/**
 * Account usage on the dashboard: the server's routing state for Claude and
 * Codex as one card per account, under a heading that says when usage was
 * last checked. Polls the routing state every 30s while mounted; the server
 * itself re-reads usage every 5 minutes.
 */

import { useCallback, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { useNow } from '@/hooks/useNow';
import type { AuthFileItem } from '@/types';
import { normalizeAuthIndex } from '@/utils/authIndex';
import {
  AccountsOverview,
  type AccountsOverviewProps,
} from '@/features/quota/accounts/AccountsOverview';
import { checkedText, oldestFetchMs } from '@/features/quota/accounts/accountView';
import { useQuotaActions } from '@/features/quota/hooks/useQuotaActions';
import { QUOTA_ADAPTERS } from '@/features/quota/providers';
import { useRoutingState } from '@/features/quota/routing/useRoutingState';
import styles from '../dashboard.module.scss';

export interface AccountsSectionViewProps extends AccountsOverviewProps {
  checking: boolean;
  onCheckNow: () => void;
}

/** The section itself, free of hooks other than translation so it renders from props. */
export function AccountsSectionView(props: AccountsSectionViewProps) {
  const { state, now, canAct, checking, onCheckNow, ...overview } = props;
  const { t } = useTranslation();
  return (
    <section className={styles.section} aria-labelledby="dashboard-accounts-title">
      <header className={styles.sectionHeadRow}>
        <div className={styles.sectionHead}>
          <h2 id="dashboard-accounts-title" className={styles.sectionTitle}>
            {t('quota_accounts.title')}
          </h2>
          <p className={styles.sectionDescription}>{t('quota_accounts.subtitle')}</p>
        </div>
        <div className={styles.sectionActions}>
          {state && (
            <span className={styles.checked}>
              {checkedText(t, oldestFetchMs(state.accounts), now)}
            </span>
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

      <AccountsOverview {...overview} state={state} now={now} canAct={canAct} />
    </section>
  );
}

export interface AccountsSectionProps {
  /** The credential files the dashboard loaded; null until they arrive. */
  authFiles: AuthFileItem[] | null;
  connected: boolean;
}

export function AccountsSection({ authFiles, connected }: AccountsSectionProps) {
  const routing = useRoutingState(connected);
  const { refreshAccount, refreshAll } = routing;
  const now = useNow();
  const canAct = connected && authFiles !== null;

  const filesByIndex = useMemo(() => {
    const map = new Map<string, AuthFileItem>();
    (authFiles ?? []).forEach((file) => {
      const index = normalizeAuthIndex(file.auth_index ?? file.authIndex);
      if (index) map.set(index, file);
    });
    return map;
  }, [authFiles]);

  const { resettingQuotaName, resetQuota } = useQuotaActions(!canAct);
  const handleCodexReset = useCallback(
    (file: AuthFileItem) => resetQuota(file, QUOTA_ADAPTERS.codex),
    [resetQuota]
  );

  const [checking, setChecking] = useState(false);
  const handleCheckNow = useCallback(async () => {
    if (!canAct) return;
    setChecking(true);
    try {
      await refreshAll();
    } finally {
      setChecking(false);
    }
  }, [canAct, refreshAll]);

  // A server without the routing-state endpoint has nothing to show here.
  if (!routing.supported) return null;

  return (
    <AccountsSectionView
      state={routing.state}
      error={routing.error}
      now={now}
      filesByIndex={filesByIndex}
      canAct={canAct}
      checking={checking}
      resettingName={resettingQuotaName}
      onCheckNow={handleCheckNow}
      onRefreshAccount={refreshAccount}
      onCodexReset={handleCodexReset}
    />
  );
}
