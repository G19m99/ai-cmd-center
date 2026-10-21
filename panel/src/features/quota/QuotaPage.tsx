/**
 * Quota page. Claude and Codex accounts come from the server's routing state
 * (AccountsOverview): percent used per window, reset times, which account takes
 * new requests, and bonus resets. Credentials of any other provider keep
 * upstream's cards below it.
 *
 * Behaviour kept from upstream for those cards:
 * - click to load; Devin queries once when first visible, no polling;
 * - cacheGeneration session isolation and request-id dedupe (useQuotaBatchLoader);
 * - each provider's quota cache is pruned when the file list changes;
 * - useHeaderRefresh has one slot and this page owns it: the global refresh
 *   refetches the file list.
 * Against a server without the routing-state endpoint every credential falls
 * back to upstream's cards.
 */

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { authFilesApi } from '@/services/api';
import { useHeaderRefresh } from '@/hooks/useHeaderRefresh';
import { useNow } from '@/hooks/useNow';
import { useAuthStore, useQuotaStore, useThemeStore } from '@/stores';
import type { AuthFileItem, ResolvedTheme } from '@/types';
import { normalizeAuthIndex } from '@/utils/authIndex';
import { getQuotaCacheKey } from '@/utils/quota/identity';
import { AccountsOverview } from './accounts/AccountsOverview';
import { QuotaCard } from './components/QuotaCard';
import { isRoutedQuotaType } from './routing/routingFormat';
import { useRoutingState } from './routing/useRoutingState';
import { QUOTA_TAB_ORDER } from './constants';
import { classifyQuotaFiles, type QuotaFileEntry } from './logic';
import { QUOTA_ADAPTERS, getQuotaSetter, type QuotaCardState } from './providers';
import type { QuotaProviderType } from './providers/types';
import { useDevinQuotaAutoLoad } from './providers/devin/useDevinQuotaAutoLoad';
import { useQuotaActions } from './hooks/useQuotaActions';
import { useQuotaBatchLoader } from './hooks/useQuotaBatchLoader';
import styles from './QuotaPage.module.scss';

const authIndexOf = (file: AuthFileItem): string | null =>
  normalizeAuthIndex(file.auth_index ?? file.authIndex);

export function QuotaPage() {
  const { t } = useTranslation();
  const connectionStatus = useAuthStore((state) => state.connectionStatus);
  const resolvedTheme: ResolvedTheme = useThemeStore((state) => state.resolvedTheme);

  const [files, setFiles] = useState<AuthFileItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const disableControls = connectionStatus !== 'connected';

  /* ---------- File list ---------- */

  const sessionGeneration = useQuotaStore((state) => state.cacheGeneration);
  const [filesGeneration, setFilesGeneration] = useState<number | null>(null);
  const listRequestRef = useRef(0);
  const loadFiles = useCallback(async () => {
    const requestId = ++listRequestRef.current;
    if (connectionStatus !== 'connected') {
      setFiles([]);
      setFilesGeneration(null);
      setLoading(false);
      return;
    }
    const isCurrent = () =>
      requestId === listRequestRef.current &&
      sessionGeneration === useQuotaStore.getState().cacheGeneration;
    setLoading(true);
    setError('');
    try {
      const data = await authFilesApi.list();
      if (!isCurrent()) return;
      setFiles(data?.files || []);
      setFilesGeneration(sessionGeneration);
    } catch (err: unknown) {
      if (!isCurrent()) return;
      const message = err instanceof Error ? err.message : t('notification.refresh_failed');
      setError(message);
    } finally {
      if (isCurrent()) setLoading(false);
    }
  }, [connectionStatus, sessionGeneration, t]);

  useHeaderRefresh(loadFiles);

  useEffect(() => {
    void loadFiles();
    return () => {
      listRequestRef.current += 1;
    };
  }, [loadFiles]);

  /* ---------- Quota caches (upstream cards) ---------- */

  const antigravityQuota = useQuotaStore((state) => state.antigravityQuota);
  const claudeQuota = useQuotaStore((state) => state.claudeQuota);
  const codexQuota = useQuotaStore((state) => state.codexQuota);
  const devinQuota = useQuotaStore((state) => state.devinQuota);
  const kimiQuota = useQuotaStore((state) => state.kimiQuota);
  const metaQuota = useQuotaStore((state) => state.metaQuota);
  const pluginQuota = useQuotaStore((state) => state.pluginQuota);
  const xaiQuota = useQuotaStore((state) => state.xaiQuota);

  const quotaByType = useMemo<Record<QuotaProviderType, Record<string, QuotaCardState>>>(
    () =>
      ({
        antigravity: antigravityQuota,
        claude: claudeQuota,
        codex: codexQuota,
        devin: devinQuota,
        kimi: kimiQuota,
        meta: metaQuota,
        plugin: pluginQuota,
        xai: xaiQuota,
      }) as unknown as Record<QuotaProviderType, Record<string, QuotaCardState>>,
    [
      antigravityQuota,
      claudeQuota,
      codexQuota,
      devinQuota,
      kimiQuota,
      metaQuota,
      pluginQuota,
      xaiQuota,
    ]
  );

  const getQuota = useCallback(
    (entry: QuotaFileEntry): QuotaCardState | undefined =>
      quotaByType[entry.type][getQuotaCacheKey(entry.file)],
    [quotaByType]
  );

  const entries = useMemo(() => classifyQuotaFiles(files), [files]);

  // Prune: once the file list settles, each provider cache keeps only live credentials.
  useEffect(() => {
    if (loading || error || filesGeneration !== sessionGeneration) return;
    const survivorsByType = new Map<QuotaProviderType, Set<string>>(
      QUOTA_TAB_ORDER.map((type) => [type, new Set<string>()])
    );
    entries.forEach((entry) => survivorsByType.get(entry.type)?.add(getQuotaCacheKey(entry.file)));

    QUOTA_TAB_ORDER.forEach((type) => {
      const survivors = survivorsByType.get(type) ?? new Set<string>();
      const setQuota = getQuotaSetter(QUOTA_ADAPTERS[type]);
      setQuota((prev) => {
        const staleKeys = Object.keys(prev).filter((name) => !survivors.has(name));
        if (staleKeys.length === 0) return prev;
        const next = { ...prev };
        staleKeys.forEach((name) => delete next[name]);
        return next;
      });
    });
  }, [entries, error, filesGeneration, loading, sessionGeneration]);

  /* ---------- Routing state (fork) ----------
   * Polled every 30s while visible; the server re-reads usage every 5 minutes.
   * A 404 from an older server turns `supported` off. */

  const routing = useRoutingState(connectionStatus === 'connected');
  const { refreshAccount: refreshRoutingAccount, refreshAll: refreshAllRouting } = routing;
  const now = useNow();

  const filesByIndex = useMemo(() => {
    const map = new Map<string, AuthFileItem>();
    entries.forEach((entry) => {
      const index = authIndexOf(entry.file);
      if (index) map.set(index, entry.file);
    });
    return map;
  }, [entries]);

  const [checking, setChecking] = useState(false);
  const handleCheckNow = useCallback(async () => {
    if (disableControls) return;
    setChecking(true);
    try {
      await refreshAllRouting();
    } finally {
      setChecking(false);
    }
  }, [disableControls, refreshAllRouting]);

  /* ---------- Other providers ---------- */

  const otherEntries = useMemo(
    () => (routing.supported ? entries.filter((entry) => !isRoutedQuotaType(entry.type)) : entries),
    [entries, routing.supported]
  );

  const { batchLoading, loadQuota } = useQuotaBatchLoader();
  const { resettingQuotaName, refreshQuota, resetQuota } = useQuotaActions(disableControls);

  const handleRefreshCard = useCallback(
    (entry: QuotaFileEntry) => {
      void refreshQuota(entry.file, QUOTA_ADAPTERS[entry.type]);
      if (isRoutedQuotaType(entry.type)) refreshRoutingAccount(authIndexOf(entry.file));
    },
    [refreshQuota, refreshRoutingAccount]
  );

  const handleCodexReset = useCallback(
    (file: AuthFileItem) => resetQuota(file, QUOTA_ADAPTERS.codex),
    [resetQuota]
  );

  useDevinQuotaAutoLoad(
    otherEntries,
    disableControls ||
      loading ||
      batchLoading ||
      Boolean(error) ||
      filesGeneration !== sessionGeneration,
    loadQuota
  );

  const canUseActions = !disableControls && !loading && filesGeneration === sessionGeneration;

  /* ---------- Render ---------- */

  return (
    <div className={styles.page}>
      {routing.supported ? (
        <AccountsOverview
          state={routing.state}
          error={routing.error}
          now={now}
          filesByIndex={filesByIndex}
          canAct={canUseActions}
          checking={checking}
          resettingName={resettingQuotaName}
          onCheckNow={handleCheckNow}
          onRefreshAccount={refreshRoutingAccount}
          onCodexReset={handleCodexReset}
        />
      ) : (
        <h1 className={styles.fallbackTitle}>{t('quota_accounts.title')}</h1>
      )}

      {error && (
        <div className={styles.errorBanner} role="alert">
          {error}
        </div>
      )}

      {otherEntries.length > 0 && (
        <section
          className={styles.others}
          aria-labelledby={routing.supported ? 'quota-other-providers' : undefined}
        >
          {routing.supported && (
            <h2 id="quota-other-providers" className={styles.othersTitle}>
              {t('quota_accounts.other_providers')}
            </h2>
          )}
          <div className={styles.grid}>
            {otherEntries.map((entry) => (
              <QuotaCard
                key={`${entry.type}:${getQuotaCacheKey(entry.file)}`}
                entry={entry}
                quota={getQuota(entry)}
                resolvedTheme={resolvedTheme}
                canRefresh={canUseActions && !entry.file.disabled}
                resetting={resettingQuotaName === getQuotaCacheKey(entry.file)}
                onRefresh={() => handleRefreshCard(entry)}
                onReset={() => resetQuota(entry.file, QUOTA_ADAPTERS[entry.type])}
              />
            ))}
          </div>
        </section>
      )}
    </div>
  );
}
