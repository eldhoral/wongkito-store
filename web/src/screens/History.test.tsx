// @vitest-environment jsdom
import { afterAll, afterEach, beforeAll, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { History } from './History';

const get = vi.fn();
vi.mock('../api/client', () => ({
  api: { get: (p: string) => get(p), send: vi.fn() },
}));

// jsdom lacks pointer capture, which vaul calls on pointerdown. Scoped to this file.
const proto = Element.prototype;
const saved = { set: proto.setPointerCapture, release: proto.releasePointerCapture };
beforeAll(() => {
  proto.setPointerCapture = () => {};
  proto.releasePointerCapture = () => {};
});
afterAll(() => {
  proto.setPointerCapture = saved.set;
  proto.releasePointerCapture = saved.release;
});
afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe('History', () => {
  it('searchQueriesHistory', async () => {
    get.mockResolvedValue([]);
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <History />
      </QueryClientProvider>,
    );
    await waitFor(() => expect(get).toHaveBeenCalledWith('/api/history'));
    fireEvent.change(screen.getByLabelText(/search/i), { target: { value: 'OD000045' } });
    // Debounced: nothing is sent before 300 ms.
    expect(get).not.toHaveBeenCalledWith('/api/history?q=OD000045');
    await waitFor(() => expect(get).toHaveBeenCalledWith('/api/history?q=OD000045'), { timeout: 1500 });
  });
});
