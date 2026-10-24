// @vitest-environment jsdom
import { afterAll, afterEach, beforeAll, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ProductGroup } from '../api/types';
import { GroupSheet } from './GroupSheet';

const get = vi.fn();
const send = vi.fn();
vi.mock('../api/client', () => ({
  api: { get: (p: string) => get(p), send: (...a: unknown[]) => send(...a) },
}));

const group: ProductGroup = {
  game: 'DANA',
  seller: 'Seller C',
  template: '',
  items: [
    { id: 1, name: 'Saldo 50.000', mode: 'manual', offReason: '', deadlineClass: 'regular', stockLocked: false, cost: 50120, price: 51500, stock: 10, net: 50000, margin: 0, skus: ['D50'] },
  ],
};

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

describe('GroupSheet', () => {
  it('savesFilteredTemplateWithoutSampleOrder', async () => {
    get.mockImplementation(async (p: string) => (p === '/api/queue' ? { needs: [], processing: [], stats: {} } : []));
    send.mockResolvedValue({ ok: true });
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <GroupSheet group={group} onOpenChange={() => {}} />
      </QueryClientProvider>,
    );
    const user = userEvent.setup();
    await user.click(screen.getByLabelText(/template/i));
    await user.paste('{phone|msisdn}');
    const save = screen.getByRole('button', { name: /save template/i }) as HTMLButtonElement;
    expect(save.disabled).toBe(false);
    expect(screen.getByText(/needs a sample order with phone/i)).toBeTruthy();
    await user.click(save);
    expect(send).toHaveBeenCalledWith('PUT', '/api/templates', { seller: 'Seller C', game: 'DANA', template: '{phone|msisdn}' });
  });
});
