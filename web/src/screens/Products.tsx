import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '../api/client';
import type { ProductGroup, ProductView } from '../api/types';
import { GroupSheet } from '../components/GroupSheet';
import { ModeControl, STOCK_LOCKED_TEXT } from '../components/ModeControl';
import { ProductSheet } from '../components/ProductSheet';
import { formatRp } from '../lib/format';

type Filter = 'all' | 'auto' | 'off' | 'unlinked';

const FILTERS: { id: Filter; label: string; test: (p: ProductView) => boolean }[] = [
  { id: 'all', label: 'All', test: () => true },
  { id: 'auto', label: 'Auto', test: (p) => p.mode === 'auto' },
  { id: 'off', label: 'Off', test: (p) => p.mode === 'off' },
  { id: 'unlinked', label: 'Unlinked', test: (p) => p.skus.length === 0 },
];

const sign = (n: number) => (n < 0 ? '−' : '+') + formatRp(Math.abs(n)).slice(3);

export function Products() {
  const [filter, setFilter] = useState<Filter>('all');
  // Sheets hold ids and read fresh data, so a refetch updates what they show.
  const [groupKey, setGroupKey] = useState<[string, string] | null>(null);
  const [itemId, setItemId] = useState<number | null>(null);
  const q = useQuery({ queryKey: ['products'], queryFn: () => api.get<ProductGroup[]>('/api/products') });

  if (!q.data) {
    return q.error ? (
      <p role="alert" className="px-5 pt-6 text-[13px] text-stop">
        Could not load products.
      </p>
    ) : null;
  }

  const groups = q.data;
  const all = groups.flatMap((g) => g.items);
  const test = FILTERS.find((f) => f.id === filter)!.test;
  const shown = groups.map((g) => ({ ...g, items: g.items.filter(test) })).filter((g) => g.items.length > 0);
  const openGroup = groupKey && (groups.find((g) => g.game === groupKey[0] && g.seller === groupKey[1]) ?? null);
  const openGroupOf = groups.find((g) => g.items.some((i) => i.id === itemId));
  const openItem = openGroupOf?.items.find((i) => i.id === itemId) ?? null;

  return (
    <div className="pb-6">
      <h1 className="px-5 pt-5 pb-3 text-2xl font-semibold tracking-[-0.03em]">Products</h1>
      <div role="tablist" aria-label="Filter" className="flex gap-1.5 overflow-x-auto px-5 pb-3">
        {FILTERS.map((f) => (
          <button
            key={f.id}
            type="button"
            role="tab"
            aria-selected={filter === f.id}
            onClick={() => setFilter(f.id)}
            className="-my-1 flex h-11 shrink-0 items-center"
          >
            <span
              className={`flex h-8 items-center gap-1.5 rounded-full px-3 text-[12.5px] font-medium ${
                filter === f.id
                  ? 'bg-hi text-white'
                  : `text-mid shadow-[inset_0_0_0_1px_var(--color-line-2)] ${f.id === 'unlinked' ? '[&>b]:text-hold' : ''}`
              }`}
            >
              {f.label} <b className="tn font-semibold">{all.filter(f.test).length}</b>
            </span>
          </button>
        ))}
      </div>

      {shown.map((g) => (
        <section key={`${g.game}\0${g.seller}`} className="mx-3 mb-2.5 rounded-[18px] bg-sheet shadow-card">
          <header className="flex items-center justify-between py-2.5 pr-2 pl-4">
            <div className="min-w-0">
              <b className="block text-[15px] font-semibold tracking-[-0.01em]">{g.game}</b>
              <div className="mt-0.5 text-[11.5px] text-lo">
                {g.seller ? (
                  <>
                    {g.seller} · <span className="font-mono text-mid">{g.template || 'no template'}</span>
                  </>
                ) : (
                  'no Digiflazz SKU linked'
                )}
              </div>
            </div>
            <button
              type="button"
              aria-label={`${g.game} group options`}
              onClick={() => setGroupKey([g.game, g.seller])}
              className="flex h-11 w-11 shrink-0 items-center justify-center"
            >
              <span className="flex h-9 w-9 items-center justify-center rounded-[10px] text-mid">•••</span>
            </button>
          </header>
          {g.items.map((p) => (
            <div key={p.id} className="flex items-center justify-between gap-2.5 border-t border-line py-2.5 pr-3.5 pl-4">
              <button type="button" onClick={() => setItemId(p.id)} className="min-h-11 min-w-0 flex-1 text-left">
                <div className={`text-sm font-medium ${p.mode === 'off' ? 'text-lo' : ''}`}>{p.name}</div>
                {p.skus.length > 0 && (
                  <div className="tn mt-0.5 text-[11.5px] text-lo">
                    {formatRp(p.cost)} → {formatRp(p.price)}{' '}
                    <span className={`font-medium ${p.margin < 0 ? 'text-stop' : 'text-sent'}`}>{sign(p.margin)}</span>
                  </div>
                )}
                {p.skus.length === 0 && <div className="mt-0.5 text-[11.5px] text-hold">Link a SKU to enable Auto</div>}
                {p.offReason.startsWith('auto:') && <div className="mt-0.5 text-[11.5px] text-hold">{p.offReason}</div>}
                {p.stockLocked && <div className="mt-0.5 text-[11.5px] text-hold">{STOCK_LOCKED_TEXT}</div>}
              </button>
              <ModeControl product={p} />
            </div>
          ))}
        </section>
      ))}
      {shown.length === 0 && <p className="px-5 pt-4 text-[13px] text-mid">Nothing here.</p>}

      <GroupSheet group={openGroup || null} onOpenChange={(o) => !o && setGroupKey(null)} />
      <ProductSheet product={openItem} game={openGroupOf?.game ?? ''} onOpenChange={(o) => !o && setItemId(null)} />
    </div>
  );
}
