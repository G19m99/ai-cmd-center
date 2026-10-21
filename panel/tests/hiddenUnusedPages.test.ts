import { describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';

// This deployment routes subscription accounts only. Quick Start (an APIKEY.FUN
// sign-up page) and AI Providers (pay-per-use API keys) stay reachable by URL
// but are not offered in the sidebar or on the dashboard.
const layout = readFileSync('src/components/layout/MainLayout.tsx', 'utf8');
const routes = readFileSync('src/router/MainRoutes.tsx', 'utf8');
const dashboard = readFileSync('src/features/dashboard/DashboardPage.tsx', 'utf8');

describe('unused upstream pages', () => {
  test('are not in the sidebar', () => {
    expect(layout).not.toContain("path: '/quick-start'");
    expect(layout).not.toContain("path: '/ai-providers'");
  });

  test('keep their routes', () => {
    expect(routes).toContain("path: '/quick-start'");
    expect(routes).toContain("path: '/ai-providers'");
  });

  test('are not linked from the dashboard', () => {
    expect(dashboard).not.toMatch(/to(=|: )['"]\/(ai-providers|quick-start)['"]/);
  });
});
