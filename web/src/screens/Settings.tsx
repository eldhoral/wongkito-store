import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../api/client';
import type { Settings as SettingsData, Status } from '../api/types';
import { formatAge } from '../lib/format';

const FIELDS: { key: keyof SettingsData; label: string; step: string; whole: boolean }[] = [
  { key: 'min_margin', label: 'Minimum margin (Rp)', step: '1', whole: true },
  { key: 'fee_pct', label: 'Fallback fee (%)', step: 'any', whole: false },
  { key: 'balance_alert', label: 'Balance alert (Rp)', step: '1', whole: true },
];

// Mirrors the server: min_margin/balance_alert are int64 (>= 0), fee_pct is 0..100.
function validate(form: Record<string, string>): { error: string } | { body: Record<string, number> } {
  const body: Record<string, number> = {};
  for (const f of FIELDS) {
    const name = f.label.replace(/ \(.*\)$/, '');
    const raw = (form[f.key] ?? '').trim();
    if (raw === '') return { error: `${name} is required` };
    const n = Number(raw);
    if (!Number.isFinite(n)) return { error: `${name} must be a number` };
    if (f.whole && !Number.isSafeInteger(n)) return { error: `${name} must be a whole number` };
    if (n < 0 || (f.key === 'fee_pct' && n > 100)) return { error: `${name} must be ${f.key === 'fee_pct' ? 'between 0 and 100' : '0 or more'}` };
    body[f.key] = n;
  }
  return { body };
}

const errText = (e: unknown) => (e instanceof Error ? e.message : 'Failed');

export function Settings() {
  const qc = useQueryClient();
  const settings = useQuery({ queryKey: ['settings'], queryFn: () => api.get<SettingsData>('/api/settings') });
  const status = useQuery({ queryKey: ['status'], queryFn: () => api.get<Status>('/api/status') });
  const [form, setForm] = useState<Record<string, string>>({});
  useEffect(() => {
    if (settings.data) setForm(Object.fromEntries(FIELDS.map((f) => [f.key, String(settings.data[f.key])])));
  }, [settings.data]);

  const [invalid, setInvalid] = useState('');
  const save = useMutation({
    mutationFn: (body: Record<string, number>) => api.send<SettingsData>('PUT', '/api/settings', body),
    onSuccess: (d) => qc.setQueryData(['settings'], d),
  });
  const tg = useMutation({ mutationFn: () => api.send('POST', '/api/settings/telegram-test') });
  const refresh = useMutation({
    mutationFn: () => api.send('POST', '/api/pricelist/refresh'),
    // Errors (e.g. Digiflazz's rc 83 limit) also land in the status list.
    onSettled: () => qc.invalidateQueries({ predicate: (q) => ['status', 'products', 'pricelist'].includes(q.queryKey[0] as string) }),
  });
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
              onChange={(e) => {
                setInvalid('');
                setForm({ ...form, [f.key]: e.target.value });
              }}
              className="tn mt-1 h-11 w-full rounded-xl bg-well px-3.5 text-[14px] outline-none"
            />
          </label>
        ))}
        <p role="alert" className="min-h-5 text-[12.5px] text-stop">
          {invalid || (save.error ? errText(save.error) : '')}
        </p>
        <button
          type="button"
          disabled={save.isPending || !settings.data}
          onClick={() => {
            const v = validate(form);
            setInvalid('error' in v ? v.error : '');
            if ('body' in v) save.mutate(v.body);
          }}
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
        <button
          type="button"
          disabled={refresh.isPending}
          onClick={() => refresh.mutate()}
          className="mt-1 h-11 w-full rounded-xl bg-well text-[14px] font-semibold disabled:opacity-40"
        >
          {refresh.isPending ? 'Refreshing…' : 'Refresh price list'}
        </button>
        <p role="alert" className={`mt-2 min-h-5 text-[12.5px] ${refresh.error ? 'text-stop' : 'text-sent'}`}>
          {refresh.error ? errText(refresh.error) : refresh.isSuccess ? 'Price list updated' : ''}
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
