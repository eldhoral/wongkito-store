import { afterEach, describe, expect, it, vi } from 'vitest';
import { api, ApiError } from './client';

function mockFetch(status: number, body: string) {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(body, { status })));
}

afterEach(() => vi.unstubAllGlobals());

describe('ApiError message', () => {
  it('uses the "error" field of a JSON body', async () => {
    mockFetch(409, '{"error":"already bought"}');
    await expect(api.get('/api/x')).rejects.toMatchObject({ status: 409, message: 'already bought' });
  });

  it('falls back to the raw text for plain-text bodies', async () => {
    mockFetch(401, 'unauthorized\n');
    const err = await api.get('/api/x').catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 401, message: 'unauthorized' });
  });
});
