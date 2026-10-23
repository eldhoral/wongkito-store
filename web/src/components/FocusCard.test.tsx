// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { Attempt, OrderDetail, OrderView } from '../api/types';
import { FocusCard } from './FocusCard';

const get = vi.fn();
const send = vi.fn();
vi.mock('../api/client', () => ({
  api: { get: (p: string) => get(p), send: (...a: unknown[]) => send(...a) },
}));

const order: OrderView = {
  id: 'o1',
  source: 'itemku',
  orderNumber: 'OD0000454634',
  game: 'Mobile Legends',
  product: 'Mobile Legends · 172 Diamonds',
  quantity: 1,
  info: { player: '12345678', zone: '1234' },
  customerNo: '12345678|1234',
  sku: 'A',
  seller: 'Seller A',
  cost: 44000,
  income: 45000,
  margin: 1000,
  state: 'needs_you',
  reason: 'auto_failed',
  detail: 'Seller A rejected: rc 44.',
  deadlineAt: null,
  deadlineClass: 'regular',
  createdAt: 1000,
  buyerWa: '',
};

const opt = (sku: string, seller: string, customerNo: string | null, margin: number | null, templateError: string | null = null) => ({
  sku, seller, price: 44000, customerNo, templateError, margin,
});

function setup(
  view: OrderView,
  options = [opt('A', 'Seller A', '12345678|1234', 1000), opt('B', 'Seller B', '123456781234', -200)],
  attempts: Attempt[] = [],
) {
  const detail: OrderDetail = { ...view, attempts, options };
  get.mockResolvedValue(detail);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <FocusCard order={view} now={2000} onEditing={() => {}} onDone={() => {}} />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe('FocusCard', () => {
  it('pickingOtherSellerRewritesNumber', async () => {
    const user = userEvent.setup();
    setup(order);
    await user.click(await screen.findByRole('button', { name: /other seller/i }));
    await user.click(await screen.findByRole('button', { name: /Seller B/ }));
    expect((screen.getByLabelText(/customer number/i) as HTMLInputElement).value).toBe('123456781234');
  });

  it('lossCheckboxOnlyWhenNegativeMargin', async () => {
    const user = userEvent.setup();
    setup(order);
    expect(screen.queryByLabelText(/buy at a loss/i)).toBeNull();
    await user.click(await screen.findByRole('button', { name: /other seller/i }));
    await user.click(await screen.findByRole('button', { name: /Seller B/ }));
    await user.click(screen.getByLabelText(/buy at a loss/i));
    send.mockResolvedValue({ ok: true });
    await user.click(screen.getByRole('button', { name: /buy & deliver/i }));
    expect(send).toHaveBeenCalledWith('POST', '/api/orders/o1/buy', { sku: 'B', customerNo: '123456781234', noCap: true });
  });

  it('directOrderShowsCancelNotRefund', async () => {
    setup({ ...order, source: 'direct' });
    expect(await screen.findByRole('button', { name: /^cancel$/i })).toBeTruthy();
    expect(screen.queryByRole('button', { name: /reject & refund/i })).toBeNull();
  });

  it('templateErrorOptionIsDisabledWithError', async () => {
    const user = userEvent.setup();
    setup(order, [opt('A', 'Seller A', '1', 1), opt('C', 'Seller C', null, null, 'missing zone')]);
    await user.click(await screen.findByRole('button', { name: /other seller/i }));
    const c = await screen.findByRole('button', { name: /Seller C/ });
    expect((c as HTMLButtonElement).disabled).toBe(true);
    expect(c.textContent).toContain('missing zone');
  });

  it('rejectDefaultsFollowLastRc', async () => {
    const user = userEvent.setup();
    const att = (rc: string): Attempt => ({
      refId: 'r', unit: 0, attemptNo: 1, sku: 'A', customerNo: 'x', status: 'Gagal', rc, message: '', sn: '', price: 0, createdAt: 1,
    });
    setup(order, undefined, [att('54')]);
    await user.click(await screen.findByRole('button', { name: /reject & refund/i }));
    // attempts arrive with the detail query, possibly after the sheet opens
    await waitFor(() =>
      expect((screen.getByRole('radio', { name: /wrong buyer information/i }) as HTMLInputElement).checked).toBe(true),
    );
  });
});
