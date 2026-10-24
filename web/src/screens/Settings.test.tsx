// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { Settings } from './Settings';

const get = vi.fn();
const send = vi.fn();
vi.mock('../api/client', () => ({
  api: { get: (p: string) => get(p), send: (...a: unknown[]) => send(...a) },
}));

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

async function setup() {
  get.mockImplementation(async (p: string) =>
    p === '/api/settings'
      ? { min_margin: 0, fee_pct: 5, balance_alert: 100000 }
      : { lastCallback: 0, lastPoll: 0, lastPriceSync: 0, lastErrors: { itemku: '', digiflazz: '', telegram: '' } },
  );
  send.mockResolvedValue({ min_margin: 0, fee_pct: 5, balance_alert: 100000 });
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <Settings />
    </QueryClientProvider>,
  );
  await waitFor(() => expect((screen.getByLabelText(/balance alert/i) as HTMLInputElement).value).toBe('100000'));
}

const save = () => fireEvent.click(screen.getByRole('button', { name: /^save$/i }));
const setField = (label: RegExp, value: string) => fireEvent.change(screen.getByLabelText(label), { target: { value } });

describe('Settings', () => {
  it('refreshPriceListShowsDigiflazzError', async () => {
    await setup();
    send.mockRejectedValueOnce(new Error('digiflazz: price list: limit (rc 83)'));
    fireEvent.click(screen.getByRole('button', { name: /refresh price list/i }));
    expect(await screen.findByText(/rc 83/)).toBeTruthy();
    expect(send).toHaveBeenCalledWith('POST', '/api/pricelist/refresh');
  });

  it('blankFieldShowsErrorWithoutSaving', async () => {
    await setup();
    setField(/balance alert/i, '');
    save();
    expect(await screen.findByText(/balance alert is required/i)).toBeTruthy();
    expect(send).not.toHaveBeenCalled();
  });

  it('decimalBalanceAlertShowsErrorWithoutSaving', async () => {
    await setup();
    setField(/balance alert/i, '1.5');
    save();
    expect((await screen.findByText(/balance alert must be a whole number/i)).textContent).toBeTruthy();
    expect(send).not.toHaveBeenCalled();
  });

  it('validValuesSendOnePutWithNumbers', async () => {
    await setup();
    setField(/minimum margin/i, '500');
    setField(/fallback fee/i, '2.5');
    save();
    await waitFor(() => expect(send).toHaveBeenCalledTimes(1));
    expect(send).toHaveBeenCalledWith('PUT', '/api/settings', { min_margin: 500, fee_pct: 2.5, balance_alert: 100000 });
  });
});
