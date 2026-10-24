// @vitest-environment jsdom
import { afterAll, afterEach, beforeAll, describe, expect, it, vi } from 'vitest';
import { act, cleanup, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { OrderView, QueueResponse } from '../api/types';
import { Queue } from './Queue';

const get = vi.fn();
const send = vi.fn();
vi.mock('../api/client', () => ({
  api: { get: (p: string) => get(p), send: (...a: unknown[]) => send(...a) },
}));

const order = (id: string, product: string, deadlineAt: number | null): OrderView => ({
  id, source: 'itemku', orderNumber: id, game: 'MLBB', product, quantity: 1, info: {}, customerNo: '1',
  sku: 'A', seller: 'Seller A', cost: 1, income: 2, adsFee: 0, margin: 1, state: 'needs_you', reason: 'auto_failed',
  detail: '', deadlineAt, deadlineClass: 'regular', createdAt: 1, buyerWa: '',
});

const A = order('a', 'Product A', 9_999_999_999);
const B = order('b', 'Product B', 9_000_000_000); // more urgent: sorts first

let queue: QueueResponse;
function setup(initial: QueueResponse) {
  queue = initial;
  get.mockImplementation(async (p: string) => {
    if (p === '/api/queue') return queue;
    const o = [...queue.needs, ...queue.processing].find((x) => p === `/api/orders/${x.id}`);
    return { ...o, attempts: [], options: [] };
  });
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <Queue />
    </QueryClientProvider>,
  );
  return async (next: QueueResponse) => {
    queue = next;
    await act(() => qc.refetchQueries({ queryKey: ['queue'] }));
  };
}

const stats = { balance: null, autoToday: 0, sentToday: 0 };
const focused = () => screen.getByRole('heading', { level: 2, name: /^Product/ }).textContent;

// jsdom lacks pointer capture, which vaul calls on pointerdown. Scoped to this file.
const proto = Element.prototype;
const saved = { set: proto.setPointerCapture, release: proto.releasePointerCapture };
beforeAll(() => {
  proto.setPointerCapture = () => {};
  proto.releasePointerCapture = () => {};
  Element.prototype.scrollIntoView = () => {};
});
afterAll(() => {
  proto.setPointerCapture = saved.set;
  proto.releasePointerCapture = saved.release;
});
afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe('Queue', () => {
  it('focusStaysPinnedWhenANewerOrderSortsFirst', async () => {
    const refetch = setup({ needs: [A], processing: [], stats });
    expect(await screen.findByRole('heading', { level: 2, name: 'Product A' })).toBeTruthy();

    await refetch({ needs: [B, A], processing: [], stats });
    expect(await screen.findByRole('button', { name: /Product B/ })).toBeTruthy(); // B arrived, as a row
    expect(focused()).toBe('Product A');

    // The pinned order leaves needs: the card moves on.
    await refetch({ needs: [B], processing: [], stats });
    await waitFor(() => expect(focused()).toBe('Product B'));
  });

  it('rowTapMovesFocus', async () => {
    const user = userEvent.setup();
    setup({ needs: [B, A], processing: [], stats });
    expect(await screen.findByRole('heading', { level: 2, name: 'Product B' })).toBeTruthy();
    await user.click(screen.getByRole('button', { name: /Product A/ }));
    expect(focused()).toBe('Product A');
  });

  it('unknownBalanceShowsDash', async () => {
    setup({ needs: [], processing: [], stats });
    expect(await screen.findByText('—')).toBeTruthy();
  });
});
