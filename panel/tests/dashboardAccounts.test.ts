import { describe, expect, test } from 'bun:test';
import { existsSync, readFileSync } from 'node:fs';

// The quota page is folded into the dashboard: stat cards, then one card per
// account, then traffic. No landing-style hero and no shortcut cards.
const dashboard = readFileSync('src/features/dashboard/DashboardPage.tsx', 'utf8');
const layout = readFileSync('src/components/layout/MainLayout.tsx', 'utf8');
const routes = readFileSync('src/router/MainRoutes.tsx', 'utf8');

describe('dashboard with account usage', () => {
  test('orders stat cards, then accounts, then traffic', () => {
    const stats = dashboard.indexOf('className={styles.statsRow}');
    const accounts = dashboard.indexOf('<AccountsSection');
    const traffic = dashboard.indexOf('<ThroughputChart');
    expect(stats).toBeGreaterThan(-1);
    expect(accounts).toBeGreaterThan(stats);
    expect(traffic).toBeGreaterThan(accounts);
  });

  test('has no landing-style hero or shortcut cards', () => {
    expect(dashboard).not.toContain('styles.hero');
    expect(dashboard).not.toContain('ctaCards');
    expect(dashboard).not.toContain('styles.eyebrow');
  });

  test('shows request volume instead of the always-empty provider-key count', () => {
    expect(dashboard).toContain("key: 'requests'");
    expect(dashboard).not.toContain("key: 'providerKeys'");
  });

  test('the quota page is gone and its address opens the dashboard', () => {
    expect(existsSync('src/features/quota/QuotaPage.tsx')).toBe(false);
    expect(layout).not.toContain("path: '/quota'");
    expect(routes).toContain('{ path: \'/quota\', element: <Navigate to="/" replace /> }');
  });
});
