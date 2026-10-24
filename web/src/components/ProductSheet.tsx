import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Drawer } from 'vaul';
import { api } from '../api/client';
import type { DeadlineClass, PricelistItem, ProductPatch, ProductView } from '../api/types';
import { formatRp } from '../lib/format';

interface Props {
  product: ProductView | null;
  game: string;
  onOpenChange: (open: boolean) => void;
}

const CLASSES: { value: DeadlineClass; label: string }[] = [
  { value: 'regular', label: 'Regular 24 h' },
  { value: 'ten_min', label: '10 min' },
  { value: 'instant', label: '2 min' },
];
const FIELD = 'h-11 w-full rounded-xl bg-well px-3.5 text-[14px] shadow-[inset_0_0_0_1px_var(--color-line)] outline-none';
const ICON = 'flex h-11 w-11 shrink-0 items-center justify-center rounded-lg text-[15px] text-mid disabled:opacity-30';

export function ProductSheet({ product, game, onOpenChange }: Props) {
  return (
    <Drawer.Root open={product !== null} onOpenChange={onOpenChange}>
      <Drawer.Portal>
        <Drawer.Overlay className="fixed inset-0 z-40 bg-hi/30" />
        <Drawer.Content className="fixed inset-x-0 bottom-0 z-50 mx-auto flex h-[calc(100%-1.5rem)] max-w-3xl flex-col rounded-t-[20px] bg-sheet px-5 pt-3 outline-none">
          <div className="mx-auto mb-3 h-1 w-9 shrink-0 rounded-full bg-dim" />
          {/* Mounted only while open, so closing resets the form. */}
          {product && <ProductBody product={product} game={game} onClose={() => onOpenChange(false)} />}
        </Drawer.Content>
      </Drawer.Portal>
    </Drawer.Root>
  );
}

function ProductBody({ product, game, onClose }: { product: ProductView; game: string; onClose: () => void }) {
  const qc = useQueryClient();
  const [deadline, setDeadline] = useState(product.deadlineClass);
  const [skus, setSkus] = useState(product.skus);
  const [search, setSearch] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const q = search.trim();
  // ponytail: one request per keystroke; the server caps at 30 rows. Debounce if it hurts.
  const pricelist = useQuery({
    queryKey: ['pricelist', q],
    queryFn: () => api.get<PricelistItem[]>(`/api/pricelist?q=${encodeURIComponent(q)}`),
    enabled: q !== '',
  });
  const results = (pricelist.data ?? []).filter((p) => !skus.includes(p.sku));
  const dirty = skus.join('\n') !== product.skus.join('\n');

  async function patch(body: ProductPatch) {
    await api.send('PATCH', `/api/products/${product.id}`, body);
    await qc.invalidateQueries({ queryKey: ['products'] });
  }

  async function pickDeadline(value: DeadlineClass) {
    const prev = deadline;
    setDeadline(value);
    setError('');
    try {
      await patch({ deadlineClass: value });
    } catch (e) {
      setDeadline(prev);
      setError(e instanceof Error ? e.message : 'Request failed');
    }
  }

  async function saveSkus() {
    setBusy(true);
    setError('');
    try {
      await patch({ skus });
      onClose();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Request failed');
    } finally {
      setBusy(false);
    }
  }

  function move(i: number, d: -1 | 1) {
    const next = [...skus];
    [next[i], next[i + d]] = [next[i + d], next[i]];
    setSkus(next);
  }

  return (
    <>
      <Drawer.Title className="text-lg font-semibold tracking-tight text-hi">{product.name}</Drawer.Title>
      <Drawer.Description className="text-[12.5px] text-lo">{game}</Drawer.Description>
      <div data-vaul-no-drag className="-mx-5 mt-3 min-h-0 flex-1 overflow-y-auto px-5 pb-4">
        <div role="radiogroup" aria-label="Deadline class" className="grid grid-cols-3 gap-1 rounded-xl bg-well p-1">
          {CLASSES.map((c) => (
            <button
              key={c.value}
              type="button"
              role="radio"
              aria-checked={deadline === c.value}
              onClick={() => pickDeadline(c.value)}
              className="min-h-11 rounded-lg text-[13px] font-medium text-mid aria-checked:bg-sheet aria-checked:text-hi aria-checked:shadow-card"
            >
              {c.label}
            </button>
          ))}
        </div>

        <h3 className="mt-5 text-[11px] font-semibold tracking-[0.08em] text-lo uppercase">Digiflazz SKUs</h3>
        <ul className="mt-1.5 grid gap-1.5">
          {skus.map((sku, i) => (
            <li key={sku} className="flex items-center gap-1 rounded-xl pl-3.5 ring-1 ring-line">
              <span className="min-w-0 flex-1">
                <b className="block font-mono text-[13px] font-semibold break-all">{sku}</b>
                {i === 0 && <span className="text-xs text-lo">Primary</span>}
              </span>
              <button type="button" aria-label={`Move ${sku} up`} disabled={i === 0} onClick={() => move(i, -1)} className={ICON}>
                ↑
              </button>
              <button type="button" aria-label={`Move ${sku} down`} disabled={i === skus.length - 1} onClick={() => move(i, 1)} className={ICON}>
                ↓
              </button>
              <button type="button" aria-label={`Remove ${sku}`} onClick={() => setSkus(skus.filter((s) => s !== sku))} className={`${ICON} text-stop`}>
                ✕
              </button>
            </li>
          ))}
          {skus.length === 0 && <li className="text-[12.5px] text-hold">No SKU linked. Auto needs one.</li>}
        </ul>

        <input
          aria-label="Search price list"
          type="search"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search SKU, product or brand"
          className={`${FIELD} mt-3`}
        />
        <ul className="mt-2 grid gap-1.5">
          {results.map((p) => (
            <li key={p.sku}>
              <button
                type="button"
                onClick={() => setSkus([...skus, p.sku])}
                className="flex min-h-11 w-full items-center justify-between gap-3 rounded-xl px-3.5 py-2 text-left text-[13px] ring-1 ring-line"
              >
                <span className="min-w-0">
                  <b className="block font-semibold">{p.productName}</b>
                  <span className="text-xs text-lo">
                    {p.seller} · <span className="font-mono">{p.sku}</span>
                  </span>
                  {p.desc && (
                    <span className="mt-0.5 line-clamp-3 block text-[11.5px] leading-snug whitespace-pre-line text-mid">
                      {p.desc}
                    </span>
                  )}
                </span>
                <span className="tn shrink-0 text-mid">{formatRp(p.price)}</span>
              </button>
            </li>
          ))}
        </ul>
      </div>
      <div className="pt-2 pb-[max(1.5rem,env(safe-area-inset-bottom))]">
        <p role="alert" className="min-h-5 text-[12.5px] text-stop">
          {error}
        </p>
        <button
          type="button"
          disabled={busy || !dirty}
          onClick={saveSkus}
          className="h-12 w-full rounded-xl bg-hi text-[15px] font-semibold text-white disabled:opacity-40"
        >
          {busy ? 'Working…' : 'Save SKUs'}
        </button>
      </div>
    </>
  );
}
