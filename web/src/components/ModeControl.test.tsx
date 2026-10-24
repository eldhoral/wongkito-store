// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ProductView } from '../api/types';
import { ApiError } from '../api/client';
import { ModeControl } from './ModeControl';

const send = vi.fn();
vi.mock('../api/client', async (orig) => ({
  ...(await orig<typeof import('../api/client')>()),
  api: { get: vi.fn(), send: (...a: unknown[]) => send(...a) },
}));

const product = (over: Partial<ProductView> = {}): ProductView => ({
  id: 7, name: '172 Diamonds', mode: 'auto', offReason: '', deadlineClass: 'regular', stockLocked: false,
  cost: 44000, price: 45000, stock: 10, net: 44000, margin: 0, skus: ['A'], ...over,
});

function setup(p: ProductView) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <ModeControl product={p} />
    </QueryClientProvider>,
  );
}

const radio = (name: string) => screen.getByRole('radio', { name }) as HTMLButtonElement;

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe('ModeControl', () => {
  it('autoDisabledWhenUnlinked', () => {
    setup(product({ mode: 'manual', skus: [] }));
    expect(radio('Auto').disabled).toBe(true);
    expect(radio('Manual').disabled).toBe(false);
  });

  it('offDisabledWhenStockLocked', () => {
    setup(product({ stockLocked: true }));
    const off = radio('Off');
    expect(off.disabled).toBe(true);
    expect(off.title).toBe("itemku won't let this item's stock change — turn it off in the itemku dashboard");
  });

  it('sendsPatchAndSelectsOptimistically', async () => {
    send.mockReturnValue(new Promise(() => {})); // never settles: shows the optimistic state
    setup(product());
    await userEvent.setup().click(radio('Manual'));
    expect(send).toHaveBeenCalledWith('PATCH', '/api/products/7', { mode: 'manual' });
    expect(radio('Manual').getAttribute('aria-checked')).toBe('true');
    expect(radio('Auto').getAttribute('aria-checked')).toBe('false');
  });

  it('rollsBackOnError', async () => {
    send.mockRejectedValue(new ApiError(500, 'boom'));
    setup(product());
    await userEvent.setup().click(radio('Manual'));
    expect(await screen.findByRole('alert')).toHaveProperty('textContent', 'boom');
    await waitFor(() => expect(radio('Auto').getAttribute('aria-checked')).toBe('true'));
    expect(radio('Manual').getAttribute('aria-checked')).toBe('false');
  });
});
