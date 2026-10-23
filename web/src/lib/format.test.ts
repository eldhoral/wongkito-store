import { describe, expect, it } from 'vitest';
import { formatAge, formatCountdown, formatRp, urgency } from './format';

describe('formatRp', () => {
  it('groups with dots and an ASCII space', () => {
    expect(formatRp(1284500)).toBe('Rp 1.284.500');
    expect(formatRp(0)).toBe('Rp 0');
    expect(formatRp(-1000)).toBe('-Rp 1.000');
  });
});

describe('formatCountdown', () => {
  it('formats m:ss under 5 min, minutes under 1 h, h m beyond, late at 0', () => {
    expect(formatCountdown(102)).toBe('1:42');
    expect(formatCountdown(299)).toBe('4:59');
    expect(formatCountdown(540)).toBe('9m');
    expect(formatCountdown(3599)).toBe('59m');
    expect(formatCountdown(3 * 3600 + 12 * 60)).toBe('3h 12m');
    expect(formatCountdown(0)).toBe('late');
    expect(formatCountdown(-5)).toBe('late');
  });
});

describe('urgency', () => {
  const now = 1_000_000;
  it('warns under 25% of the window left', () => {
    expect(urgency(now + 50, now - 550, now)).toBe('warn');
  });
  it('is calm with plenty left', () => {
    expect(urgency(now + 500, now - 100, now)).toBe('calm');
  });
  it('is late past the deadline', () => {
    expect(urgency(now - 1, now - 600, now)).toBe('late');
  });
});

describe('formatAge', () => {
  it('picks the coarsest unit', () => {
    expect([30, 120, 7200, 172800, -5].map(formatAge)).toEqual(['now', '2m', '2h', '2d', 'now']);
  });
});
