import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../api/client';
import type { Settings as SettingsData, Status } from '../api/types';
import { formatAge } from '../lib/format';

const FIELDS: { key: keyof SettingsData; label: string; step: string }[] = [
  { key: 'min_margin', label: 'Minimum margin (Rp)', step: '1' },
  { key: 'fee_pct', label: 'Fallback fee (%)', step: 'any' },
  { key: 'balance_alert', label: 'Balance alert (Rp)', step: '1' },
];

const errText = (e: unknown) => (e instanceof Error ? e.message : 'Failed');

export function Settings() {
  const qc = useQueryClient();
  const settings = useQuery({ queryKey: ['settings'], queryFn: () => api.get<SettingsData>('/api/settings') });
  const status = useQuery({ queryKey: ['status'], queryFn: () => api.get<Status>('/api/status') });
  const [form, setForm] = useState<Record<string, string>>({});
  useEffect(() => {
    if (settings.data) setForm(Object.fromEntries(FIELDS.map((f) => [f.key, String(settings.data[f.key])])));
  }, [settings.data]);

  const save = useMutation({
    mutationFn: () => api.send<SettingsData>('PUT', '/api/settings', Object.fromEntries(FIELDS.map((f) => [f.key, Number(form[f.key])]))),
    onSuccess: (d) => qc.setQueryData(['settings'], d),
  });
  const tg = useMutation({ mutationFn: () => api.send('POST', '/api/settings/telegram-test') });
  const logout = useMutation({
    mutationFn: async () => {
      await api.send('POST', '/api/logout');
      // The cookie is gone, so this 401s and App's unauthorized handler shows Login.
      await api.get('/api/status').catch(() => {});
    },
  });

  const now = Math.floor(Date.now() / 1000);
  const when = (t: number) => (t ? `${formatAge(now - t)}${now - t < 60 ? '' : ' ago'}` : 'never');
  const rows = status.data && [
    { label: 'Last callback', at: status.data.lastCallback as number | null, err: '' },
    { label: 'Last poll', at: status.data.lastPoll, err: status.data.lastErrors.itemku },
    { label: 'Last price sync', at: status.data.lastPriceSync, err: status.data.lastErrors.digiflazz },
    ...(status.data.lastErrors.telegram ? [{ label: 'Telegram', at: null, err: status.data.lastErrors.telegram }] : []),
  ];

  return (
    <div className="pb-6">
      <h1 className="px-5 pt-5 pb-3 text-2xl font-semibold tracking-[-0.03em]">Settings</h1>

      <section className="mx-3 mb-3 rounded-[18px] bg-sheet p-4 shadow-card">
        {FIELDS.map((f) => (
          <label key={f.key} className="mb-3 block">
            <span className="text-[12.5px] text-mid">{f.label}</span>
            <input
              type="number"
              inputMode="decimal"
              step={f.step}
              min="0"
              value={form[f.key] ?? ''}
              onChange={(e) => setForm({ ...form, [f.key]: e.target.value })}
              className="tn mt-1 h-11 w-full rounded-xl bg-well px-3.5 text-[14px] outline-none"
            />
          </label>
        ))}
        <p role="alert" className="min-h-5 text-[12.5px] text-stop">
          {save.error ? errText(save.error) : ''}
        </p>
        <button
          type="button"
          disabled={save.isPending || !settings.data}
          onClick={() => save.mutate()}
          className="h-12 w-full rounded-xl bg-hi text-[15px] font-semibold text-white disabled:opacity-40"
        >
          {save.isPending ? 'Saving…' : save.isSuccess ? 'Saved' : 'Save'}
        </button>
      </section>

      <section className="mx-3 mb-3 rounded-[18px] bg-sheet p-4 shadow-card">
        <button
          type="button"
          disabled={tg.isPending}
          onClick={() => tg.mutate()}
          className="h-11 w-full rounded-xl bg-well text-[14px] font-semibold disabled:opacity-40"
        >
          {tg.isPending ? 'Sending…' : 'Send Telegram test'}
        </button>
        <p role="alert" className={`mt-2 min-h-5 text-[12.5px] ${tg.error ? 'text-stop' : 'text-sent'}`}>
          {tg.error ? errText(tg.error) : tg.isSuccess ? 'Test sent' : ''}
        </p>
      </section>

      <section className="mx-3 mb-3 rounded-[18px] bg-sheet px-4 py-2 shadow-card" aria-label="Connection status">
        {rows?.map((r) => (
          <div key={r.label} className="border-t border-line py-2.5 first:border-t-0">
            <div className="flex justify-between text-[13px]">
              <span className="text-mid">{r.label}</span>
              {r.at !== null && <span className="tn font-medium">{when(r.at)}</span>}
            </div>
            {r.err && <p className="mt-0.5 text-[12px] break-words text-stop">{r.err}</p>}
          </div>
        ))}
        {status.error && (
          <p role="alert" className="py-2.5 text-[13px] text-stop">
            Could not load status.
          </p>
        )}
      </section>

      <p className="mx-5 mb-4 rounded-xl bg-well px-3.5 py-3 text-[12.5px] text-mid">
        SKUs used in Auto must have Digiflazz backup sellers turned off
      </p>

      <button
        type="button"
        disabled={logout.isPending}
        onClick={() => logout.mutate()}
        className="mx-auto flex min-h-11 items-center px-4 text-[14px] font-medium text-stop disabled:opacity-40"
      >
        Log out
      </button>
    </div>
  );
}
