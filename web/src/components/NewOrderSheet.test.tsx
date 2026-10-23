// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { DirectReq, OrderDetail, ProductGroup } from '../api/types';
import { NewOrderSheet } from './NewOrderSheet';

const get = vi.fn();
const send = vi.fn();
vi.mock('../api/client', () => ({
  api: { get: (p: string) => get(p), send: (...a: unknown[]) => send(...a) },
}));

const groups: ProductGroup[] = [
  {
    game: 'Mobile Legends',
    seller: 'Seller A',
    template: '{player_id}|{zone_id}',
    items: [
      { id: 7, name: '172 Diamonds', mode: 'auto', offReason: '', deadlineClass: 'regular', stockLocked: false, cost: 44000, price: 45000, net: 44000, margin: 0, skus: ['A'] },
    ],
  },
];

const delivered = (over: Partial<OrderDetail> = {}): OrderDetail => ({
  id: 'dir-1', source: 'direct', orderNumber: 'dir-1', game: 'Mobile Legends', product: '172 Diamonds', quantity: 1,
  info: {}, customerNo: '12345678|1234', sku: 'A', seller: 'Seller A', cost: 44000, income: 0, margin: 0,
  state: 'delivered', reason: '', detail: '', deadlineAt: null, deadlineClass: 'regular', createdAt: 1, buyerWa: '',
  attempts: [{ refId: 'r', unit: 0, attemptNo: 1, sku: 'A', customerNo: '12345678|1234', status: 'Sukses', rc: '00', message: '', sn: 'SN-777', price: 44000, createdAt: 1 }],
  options: [],
  ...over,
});

function setup(order: OrderDetail = delivered()) {
  get.mockImplementation(async (p: string) => (p === '/api/products' ? groups : order));
  send.mockResolvedValue({ id: 'dir-1' });
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <NewOrderSheet open onOpenChange={() => {}} />
    </QueryClientProvider>,
  );
}

async function pickProduct(user: ReturnType<typeof userEvent.setup>) {
  await user.click(await screen.findByRole('button', { name: /172 Diamonds/ }));
}

// jsdom lacks pointer capture, which vaul calls on pointerdown.
Element.prototype.setPointerCapture = () => {};
Element.prototype.releasePointerCapture = () => {};

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe('NewOrderSheet', () => {
  it('rendersLiveCustomerNo', async () => {
    const user = userEvent.setup();
    setup();
    await pickProduct(user);
    await user.type(screen.getByLabelText('player_id'), ' 12345678 ');
    await user.type(screen.getByLabelText('zone_id'), '1234');
    expect(screen.getByText('12345678|1234')).toBeTruthy();
  });

  it('submitSendsDirectReq', async () => {
    const user = userEvent.setup();
    setup();
    await pickProduct(user);
    await user.type(screen.getByLabelText('player_id'), '12345678');
    await user.type(screen.getByLabelText('zone_id'), '1234');
    await user.type(screen.getByLabelText(/buyer name/i), 'Budi');
    await user.type(screen.getByLabelText(/buyer whatsapp/i), '0812 3456');
    await user.type(screen.getByLabelText(/price paid/i), '50000');
    await user.click(screen.getByRole('button', { name: /buy & deliver/i }));
    const want: DirectReq = {
      productId: 7,
      fields: { player_id: '12345678', zone_id: '1234' },
      quantity: 1,
      buyerName: 'Budi',
      buyerWa: '0812 3456',
      paidPrice: 50000,
    };
    expect(send).toHaveBeenCalledWith('POST', '/api/orders/direct', want);
  });

  it('rawSkuSendsSkuAndCustomerNo', async () => {
    const user = userEvent.setup();
    setup();
    get.mockImplementation(async (p: string) =>
      p.startsWith('/api/pricelist')
        ? [{ sku: 'TSEL5', productName: 'Telkomsel 5K', brand: 'TELKOMSEL', category: 'Pulsa', type: 'Umum', seller: 'Seller X', price: 5200, stock: 1, unlimitedStock: true, multi: false, buyerProductStatus: true, sellerProductStatus: true }]
        : delivered(),
    );
    await user.click(screen.getByRole('tab', { name: /any digiflazz sku/i }));
    await user.click(await screen.findByRole('button', { name: /Telkomsel 5K/ }));
    await user.type(screen.getByLabelText(/customer number/i), '0812999');
    await user.click(screen.getByRole('button', { name: /buy & deliver/i }));
    expect(send).toHaveBeenCalledWith('POST', '/api/orders/direct', { sku: 'TSEL5', customerNo: '0812999', quantity: 1 });
  });

  it('deliveredWithNumberOffersWhatsapp', async () => {
    const user = userEvent.setup();
    setup(delivered({ buyerWa: '0812', whatsappUrl: 'https://wa.me/62812?text=x' }));
    await pickProduct(user);
    await user.type(screen.getByLabelText('player_id'), '1');
    await user.type(screen.getByLabelText('zone_id'), '2');
    await user.click(screen.getByRole('button', { name: /buy & deliver/i }));
    expect(await screen.findByText('SN-777')).toBeTruthy();
    expect(screen.getByRole('link', { name: /send to whatsapp/i }).getAttribute('href')).toBe('https://wa.me/62812?text=x');
  });

  it('deliveredWithoutNumberCopiesMessage', async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    setup();
    await pickProduct(user);
    await user.type(screen.getByLabelText('player_id'), '1');
    await user.type(screen.getByLabelText('zone_id'), '2');
    await user.click(screen.getByRole('button', { name: /buy & deliver/i }));
    await user.click(await screen.findByRole('button', { name: /copy message/i }));
    expect(writeText).toHaveBeenCalledWith('172 Diamonds\nID: 12345678|1234\nSN: SN-777');
  });
});
