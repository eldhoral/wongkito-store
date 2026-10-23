export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
  }
}

let onUnauthorized: () => void = () => {};
// App registers here so any 401 flips the UI to the Login screen.
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn;
}

// JSON handlers reply {"error": "..."}; auth/hooks reply plain text.
function errorMessage(text: string): string {
  try {
    const e = (JSON.parse(text) as { error?: unknown } | null)?.error;
    if (typeof e === 'string') return e;
  } catch {
    // not JSON
  }
  return text;
}

async function send<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    credentials: 'same-origin',
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) {
    if (res.status === 401) onUnauthorized();
    const text = (await res.text().catch(() => '')).trim();
    throw new ApiError(res.status, errorMessage(text) || res.statusText);
  }
  const text = await res.text();
  return (text ? JSON.parse(text) : undefined) as T;
}

export const api = {
  get: <T>(path: string) => send<T>('GET', path),
  send,
};
