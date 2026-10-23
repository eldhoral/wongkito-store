import { useEffect, useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '../api/client';
import type { QueueResponse } from '../api/types';
import { FocusCard } from '../components/FocusCard';
import { QueueRow } from '../components/QueueRow';
import { formatRp } from '../lib/format';

export function Queue() {
  const [editing, setEditing] = useState(false);
  const [focusId, setFocusId] = useState<string | null>(null);
  // Task 17 renders the new-order sheet from this state.
  const [, setNewOpen] = useState(false);
  const [now, setNow] = useState(() => Math.floor(Date.now() / 1000));
  const focusRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const t = setInterval(() => setNow(Math.floor(Date.now() / 1000)), 1000);
    return () => clearInterval(t);
  }, []);

  const q = useQuery({
    queryKey: ['queue'],
    queryFn: () => api.get<QueueResponse>('/api/queue'),
    refetchInterval: editing ? false : 5000, // don't swap data under a focused input
  });

  if (!q.data) {
    return q.error ? (
      <p role="alert" className="px-5 pt-6 text-[13px] text-stop">
        Could not load the queue.
      </p>
    ) : null;
  }

  const { needs, processing, stats } = q.data;
  const all = [...needs, ...processing];
  const focus = all.find((o) => o.id === focusId) ?? needs[0];
  const rest = all.filter((o) => o !== focus);

  return (
    <div className="pb-6">
      <header className="flex items-end justify-between gap-3 px-5 pt-5 pb-1">
        <h1 className="text-2xl font-semibold tracking-[-0.03em]">{needs.length} to handle</h1>
        <div className="tn text-right text-xs text-mid">
          <b className="block text-sm font-semibold text-hi">{formatRp(stats.balance)}</b>
          saldo · {stats.autoToday} auto today
        </div>
      </header>
      <div className="px-5 pb-3">
        <button
          type="button"
          onClick={() => setNewOpen(true)}
          className="min-h-11 text-[13px] font-medium text-act"
        >
          + New order
        </button>
      </div>

      <div ref={focusRef}>
        {focus ? (
          <FocusCard key={focus.id} order={focus} now={now} onEditing={setEditing} onDone={() => setFocusId(null)} />
        ) : (
          <section className="mx-3 rounded-[20px] bg-sheet p-5 text-center shadow-card">
            <h2 className="text-[22px] font-semibold tracking-[-0.025em]">Nothing needs you</h2>
            <p className="tn mt-1 text-[12.5px] text-mid">
              {stats.autoToday} auto · {stats.sentToday} direct today
            </p>
          </section>
        )}
      </div>

      {rest.length > 0 && (
        <>
          <h2 className="px-5 pt-[18px] pb-1.5 text-[11px] font-semibold tracking-[0.08em] text-lo uppercase">Up next</h2>
          {rest.map((o) => (
            <QueueRow
              key={o.id}
              order={o}
              now={now}
              onSelect={() => {
                setFocusId(o.id);
                focusRef.current?.scrollIntoView({ behavior: 'smooth', block: 'start' });
              }}
            />
          ))}
        </>
      )}
    </div>
  );
}
