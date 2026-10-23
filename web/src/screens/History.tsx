import { useEffect, useState } from 'react';
import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { Drawer } from 'vaul';
import { api } from '../api/client';
import type { OrderDetail, OrderView } from '../api/types';
import { formatAge, formatRp } from '../lib/format';

type Source = 'all' | 'itemku' | 'direct';
const SOURCES: { id: Source; label: string }[] = [
  { id: 'all', label: 'All' },
  { id: 'itemku', label: 'itemku' },
  { id: 'direct', label: 'Direct' },
];
const PAGE = 50; // server LIMIT; a full page means there may be more

const STATE_CLS: Record<string, string> = {
  delivered: 'text-sent',
  rejected: 'text-stop',
  cancelled: 'text-stop',
  needs_you: 'text-hold',
};

function historyPath(source: Source, q: string, before?: number): string {
  const p = new URLSearchParams();
  if (source !== 'all') p.set('source', source);
  if (q) p.set('q', q);
  if (before !== undefined) p.set('before', String(before));
  const s = p.toString();
  return s ? `/api/history?${s}` : '/api/history';
}

export function History() {
  const [source, setSource] = useState<Source>('all');
  const [text, setText] = useState('');
  const [q, setQ] = useState('');
  const [openId, setOpenId] = useState<string | null>(null);

  useEffect(() => {
    const t = setTimeout(() => setQ(text.trim()), 300);
    return () => clearTimeout(t);
  }, [text]);

  const list = useInfiniteQuery({
    queryKey: ['history', 'list', source, q],
    initialPageParam: undefined as number | undefined,
    queryFn: ({ pageParam }) => api.get<OrderView[]>(historyPath(source, q, pageParam)),
    getNextPageParam: (last) => (last.length < PAGE ? undefined : last[last.length - 1].createdAt),
  });
  const rows = list.data?.pages.flat() ?? [];
  const now = Math.floor(Date.now() / 1000);

  return (
    <div className="pb-6">
      <h1 className="px-5 pt-5 pb-3 text-2xl font-semibold tracking-[-0.03em]">History</h1>
      <div className="px-5 pb-2">
        <input
          type="search"
          aria-label="Search order or customer number"
          placeholder="Order or customer number"
          value={text}
          onChange={(e) => setText(e.target.value)}
          className="h-11 w-full rounded-xl bg-well px-3.5 text-[14px] outline-none"
        />
      </div>
      <div role="tablist" aria-label="Source" className="flex gap-1.5 px-5 pb-3">
        {SOURCES.map((s) => (
          <button
            key={s.id}
            type="button"
            role="tab"
            aria-selected={source === s.id}
            onClick={() => setSource(s.id)}
            className="-my-1 flex h-11 shrink-0 items-center"
          >
            <span
              className={`flex h-8 items-center rounded-full px-3 text-[12.5px] font-medium ${
                source === s.id ? 'bg-hi text-white' : 'text-mid shadow-[inset_0_0_0_1px_var(--color-line-2)]'
              }`}
            >
              {s.label}
            </span>
          </button>
        ))}
      </div>

      {list.error && (
        <p role="alert" className="px-5 text-[13px] text-stop">
          Could not load history.
        </p>
      )}
      {rows.map((o) => (
        <button
          key={o.id}
          type="button"
          onClick={() => setOpenId(o.id)}
          className="mx-3 mb-2 flex min-h-14 w-[calc(100%-1.5rem)] items-center justify-between gap-3 rounded-[14px] bg-sheet px-3.5 py-3 text-left shadow-[0_0_0_1px_var(--color-line)]"
        >
          <span className="min-w-0">
            <b className="block truncate text-sm font-semibold">{o.product}</b>
            <span className="block truncate font-mono text-xs text-lo">{o.customerNo || o.orderNumber}</span>
          </span>
          <span className="shrink-0 text-right">
            <span className={`block text-[11.5px] font-semibold ${STATE_CLS[o.state] ?? 'text-mid'}`}>{o.state.replace('_', ' ')}</span>
            <span className="block text-xs text-lo">{formatAge(now - o.createdAt)}</span>
          </span>
        </button>
      ))}
      {list.data && rows.length === 0 && <p className="px-5 pt-2 text-[13px] text-mid">No orders.</p>}
      {list.hasNextPage && (
        <button
          type="button"
          disabled={list.isFetchingNextPage}
          onClick={() => void list.fetchNextPage()}
          className="mx-auto mt-1 flex min-h-11 items-center px-4 text-[14px] font-medium text-mid disabled:opacity-40"
        >
          Load more
        </button>
      )}

      <OrderSheet id={openId} onClose={() => setOpenId(null)} />
    </div>
  );
}

function OrderSheet({ id, onClose }: { id: string | null; onClose: () => void }) {
  const q = useQuery({
    queryKey: ['order', id],
    queryFn: () => api.get<OrderDetail>(`/api/orders/${id}`),
    enabled: id !== null,
  });
  const o = q.data;
  return (
    <Drawer.Root open={id !== null} onOpenChange={(open) => !open && onClose()}>
      <Drawer.Portal>
        <Drawer.Overlay className="fixed inset-0 z-40 bg-hi/30" />
        <Drawer.Content className="fixed inset-x-0 bottom-0 z-50 mx-auto flex max-h-[85dvh] max-w-3xl flex-col rounded-t-[20px] bg-sheet px-5 pt-3 pb-[max(1.5rem,env(safe-area-inset-bottom))] outline-none">
          <div className="mx-auto mb-4 h-1 w-9 shrink-0 rounded-full bg-dim" />
          <Drawer.Title className="text-lg font-semibold tracking-tight text-hi">{o?.product ?? 'Order'}</Drawer.Title>
          <Drawer.Description className="mt-1 font-mono text-[12.5px] text-mid">
            {o ? `${o.orderNumber} · ${o.customerNo}` : 'Loading…'}
          </Drawer.Description>
          {q.error && (
            <p role="alert" className="mt-3 text-[13px] text-stop">
              Could not load order.
            </p>
          )}
          {o && (
            <div className="mt-3 overflow-y-auto">
              {o.attempts.length === 0 ? (
                <p className="text-[13px] text-mid">No attempts.</p>
              ) : (
                <table className="w-full text-left text-[12px]">
                  <thead className="text-lo">
                    <tr>
                      <th className="py-1 pr-2 font-medium">ref_id</th>
                      <th className="py-1 pr-2 font-medium">SKU</th>
                      <th className="py-1 pr-2 font-medium">Status</th>
                      <th className="py-1 pr-2 font-medium">rc</th>
                      <th className="py-1 pr-2 font-medium">sn</th>
                      <th className="py-1 text-right font-medium">Price</th>
                    </tr>
                  </thead>
                  <tbody>
                    {o.attempts.map((a) => (
                      <tr key={a.refId} className="border-t border-line align-top">
                        <td className="py-1.5 pr-2 font-mono break-all">{a.refId}</td>
                        <td className="py-1.5 pr-2 font-mono">{a.sku}</td>
                        <td className="py-1.5 pr-2">{a.status}</td>
                        <td className="py-1.5 pr-2 font-mono">{a.rc}</td>
                        <td className="py-1.5 pr-2 font-mono break-all">{a.sn}</td>
                        <td className="tn py-1.5 text-right">{formatRp(a.price)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
              {o.source === 'direct' && o.state === 'delivered' && o.whatsappUrl && (
                <a
                  href={o.whatsappUrl}
                  target="_blank"
                  rel="noreferrer"
                  className="mt-4 flex h-12 w-full items-center justify-center rounded-xl bg-hi text-[15px] font-semibold text-white"
                >
                  Send to WhatsApp
                </a>
              )}
            </div>
          )}
          <button type="button" onClick={onClose} className="mt-1 min-h-11 w-full text-[14px] font-medium text-mid">
            Close
          </button>
        </Drawer.Content>
      </Drawer.Portal>
    </Drawer.Root>
  );
}
