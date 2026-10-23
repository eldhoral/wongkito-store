import { describe, expect, it } from 'vitest';
import { EmptyError, MissingKeyError, keys, render } from './tmpl';

// Mirrors api/internal/tmpl/tmpl_test.go case for case.
describe('render', () => {
  const ok: [string, string, Record<string, string>, string][] = [
    ['pipe separator trims values', '{player_id}|{zone_id}', { player_id: ' 12345678 ', zone_id: '1234' }, '12345678|1234'],
    ['adjacent placeholders', '{player_id}{zone_id}', { player_id: ' 12345678 ', zone_id: '1234' }, '123456781234'],
    ['digits filter', '{phone|digits}', { phone: '0812-3456-7890' }, '081234567890'],
    ['msisdn rewrites 62 to 0', '{phone|msisdn}', { phone: '+62 812 3456 7890' }, '081234567890'],
    ['msisdn leaves local number', '{phone|msisdn}', { phone: '081234567890' }, '081234567890'],
  ];
  it.each(ok)('%s', (_n, template, info, want) => {
    expect(render(template, info)).toBe(want);
  });

  it('missing key', () => {
    expect(() => render('{player_id}|{zone_id}', { player_id: '12345678' })).toThrow(MissingKeyError);
  });
  it('digits filter empty', () => {
    expect(() => render('{x|digits}', { x: 'abc' })).toThrow(EmptyError);
  });
  it('unknown filter', () => {
    expect(() => render('{x|upper}', { x: 'abc' })).toThrow();
  });
  it('empty template', () => {
    expect(() => render('  ', {})).toThrow(EmptyError);
  });
});

describe('keys', () => {
  it('dedupes and strips filters', () => {
    expect(keys('{a}|{b|digits}{a}')).toEqual(['a', 'b']);
  });
});
