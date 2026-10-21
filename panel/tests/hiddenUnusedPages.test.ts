import { describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';

// This deployment routes subscription accounts only. Quick Start (an APIKEY.FUN
// sign-up page) and AI Providers (pay-per-use API keys) stay reachable by URL
// but are not offered in the sidebar or on the dashboard.
const layout = readFileSync('src/components/layout/MainLayout.tsx', 'utf8');
const routes = readFileSync('src/router/MainRoutes.tsx', 'utf8');
const dashboard = readFileSync('src/features/dashboard/DashboardPage.tsx', 'utf8');
const LOCALES = ['en', 'zh-CN', 'zh-TW', 'ru', 'vi', 'ko'];

describe('unused upstream pages', () => {
  test('are not in the sidebar', () => {
    expect(layout).not.toContain("path: '/quick-start'");
    expect(layout).not.toContain("path: '/ai-providers'");
  });

  test('keep their routes', () => {
    expect(routes).toContain("path: '/quick-start'");
    expect(routes).toContain("path: '/ai-providers'");
  });

  test('are not linked from the dashboard, which points at accounts instead', () => {
    expect(dashboard).not.toMatch(/to(=|: )['"]\/(ai-providers|quick-start)['"]/);
    expect(dashboard).toContain('<Link to="/auth-files" className={styles.primaryAction}>');
    expect(dashboard).toContain("t('dashboard.cta_manage_accounts')");
    expect(dashboard).toContain("to: '/oauth'");
    expect(dashboard).toContain("t('dashboard.cta_oauth_desc')");
  });

  test('the new dashboard strings exist in every locale', () => {
    for (const locale of LOCALES) {
      const messages = JSON.parse(readFileSync(`src/i18n/locales/${locale}.json`, 'utf8'));
      expect(typeof messages.dashboard.cta_manage_accounts).toBe('string');
      expect(typeof messages.dashboard.cta_oauth_desc).toBe('string');
    }
  });
});
