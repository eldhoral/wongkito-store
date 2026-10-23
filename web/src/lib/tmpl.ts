// Port of api/internal/tmpl/tmpl.go. Keep the two in step; tmpl.test.ts mirrors the Go table.

export class MissingKeyError extends Error {
  constructor(key: string) {
    super(`template: missing key: ${key}`);
    this.name = 'MissingKeyError';
  }
}

export class EmptyError extends Error {
  constructor(key?: string) {
    super(key === undefined ? 'template: empty result' : `template: empty result: ${key}`);
    this.name = 'EmptyError';
  }
}

const PLACEHOLDER = /\{([^{}]*)\}/g;
const VALID = /^([a-zA-Z0-9_ ]+)(?:\|(digits|msisdn))?$/;

const onlyDigits = (s: string) => s.replace(/\D/g, '');

// Replaces {key}, {key|digits} and {key|msisdn} with trimmed values from info.
export function render(template: string, info: Record<string, string>): string {
  const out = template.replace(PLACEHOLDER, (_m, inner: string) => {
    const parts = VALID.exec(inner);
    if (!parts) throw new Error(`template: invalid placeholder "${inner}"`);
    const [, key, filter] = parts;
    if (!Object.hasOwn(info, key)) throw new MissingKeyError(key);
    let value = info[key].trim();
    if (filter === 'digits') value = onlyDigits(value);
    if (filter === 'msisdn') {
      value = onlyDigits(value);
      if (value.startsWith('62')) value = '0' + value.slice(2);
    }
    if (value === '') throw new EmptyError(key);
    return value;
  });
  if (out.trim() === '') throw new EmptyError();
  return out;
}

// Placeholder names in order, without duplicates; filters stripped.
export function keys(template: string): string[] {
  const seen = new Set<string>();
  for (const m of template.matchAll(PLACEHOLDER)) {
    const parts = VALID.exec(m[1]);
    if (parts) seen.add(parts[1]);
  }
  return [...seen];
}
