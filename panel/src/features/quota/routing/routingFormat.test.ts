import { describe, expect, test } from 'bun:test';
import {
  accountDisplayName,
  formatCompactDuration,
  maskAccountName,
  windowLabel,
} from '@/features/quota/routing/routingFormat';
import { DAY_MS, HOUR_MS, MINUTE_MS } from '@/utils/time/durations';

describe('formatCompactDuration', () => {
  test('uses the two largest units', () => {
    expect(formatCompactDuration(14 * HOUR_MS)).toBe('14h');
    expect(formatCompactDuration(DAY_MS + 3 * HOUR_MS)).toBe('1d 3h');
    expect(formatCompactDuration(2 * DAY_MS)).toBe('2d');
    expect(formatCompactDuration(3 * HOUR_MS + 20 * MINUTE_MS)).toBe('3h 20m');
    expect(formatCompactDuration(5 * MINUTE_MS)).toBe('5m');
    expect(formatCompactDuration(0)).toBe('0m');
    expect(formatCompactDuration(-HOUR_MS)).toBe('0m');
  });
});

describe('maskAccountName', () => {
  test('masks the local part of an email and keeps the extension', () => {
    expect(maskAccountName('alice@example.com')).toBe('a•••@example.com');
    expect(maskAccountName('claude-alice@example.com.json')).toBe('c•••@example.com.json');
  });

  test('masks plain file names past the first two characters', () => {
    expect(maskAccountName('codex-work.json')).toBe('co•••.json');
    expect(maskAccountName('ab')).toBe('ab');
    expect(maskAccountName('')).toBe('');
  });
});

describe('windowLabel', () => {
  test('maps known keys and passes others through', () => {
    const t = (key: string) => (key === 'routing.window_seven_day' ? '7-day' : key);
    expect(windowLabel(t, 'seven_day')).toBe('7-day');
    expect(windowLabel(t, 'custom')).toBe('custom');
  });

  test('maps the Codex window keys', () => {
    const t = (key: string) => key;
    expect(windowLabel(t, 'primary_window')).toBe('routing.window_primary_window');
    expect(windowLabel(t, 'secondary_window')).toBe('routing.window_secondary_window');
  });
});

describe('accountDisplayName', () => {
  const file = { name: 'claude-alice@example.com.json' };

  test('masks Claude/Codex names while emails are hidden', () => {
    expect(accountDisplayName(file, 'claude', false)).toBe('c•••@example.com.json');
    expect(accountDisplayName({ name: 'codex-work.json' }, 'codex', false)).toBe('co•••.json');
  });

  test('shows the full name when emails are shown, and never masks other providers', () => {
    expect(accountDisplayName(file, 'claude', true)).toBe('claude-alice@example.com.json');
    expect(accountDisplayName(file, 'kimi', false)).toBe('claude-alice@example.com.json');
  });
});
