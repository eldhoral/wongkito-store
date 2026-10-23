import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { api } from '../api/client';
import type { ProductMode, ProductPatch, ProductView } from '../api/types';

export const STOCK_LOCKED_TEXT = "itemku won't let this item's stock change — turn it off in the itemku dashboard";

const OPTIONS: { mode: ProductMode; label: string; text: string }[] = [
  { mode: 'off', label: 'Off', text: 'Off' },
  { mode: 'manual', label: 'Manual', text: 'Man' },
  { mode: 'auto', label: 'Auto', text: 'Auto' },
];

const SELECTED: Record<ProductMode, string> = {
  off: 'bg-sheet text-stop shadow-card',
  manual: 'bg-sheet text-hi shadow-card',
  auto: 'bg-hi text-white',
};

// Off · Man · Auto. 32 px visual inside 44 px hit areas.
export function ModeControl({ product }: { product: ProductView }) {
  const qc = useQueryClient();
  // Optimistic: shown until the PATCH settles, then the refetched value (or the old one after an error) takes over.
  const [pending, setPending] = useState<ProductMode | null>(null);
  const [error, setError] = useState('');
  const shown = pending ?? product.mode;

  async function choose(mode: ProductMode) {
    if (pending || mode === shown) return;
    setError('');
    setPending(mode);
    try {
      await api.send('PATCH', `/api/products/${product.id}`, { mode } satisfies ProductPatch);
      await qc.invalidateQueries({ queryKey: ['products'] });
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Request failed');
    } finally {
      setPending(null);
    }
  }

  return (
    <div className="flex flex-col items-end">
      <div role="radiogroup" aria-label={`Mode for ${product.name}`} className="flex rounded-[10px] bg-well p-0.5 shadow-[inset_0_0_0_1px_var(--color-line)]">
        {OPTIONS.map((o) => {
          const disabled = (o.mode === 'auto' && product.skus.length === 0) || (o.mode === 'off' && product.stockLocked);
          const on = shown === o.mode;
          return (
            <button
              key={o.mode}
              type="button"
              role="radio"
              aria-checked={on}
              aria-label={o.label}
              disabled={disabled}
              title={o.mode === 'off' && product.stockLocked ? STOCK_LOCKED_TEXT : undefined}
              onClick={() => choose(o.mode)}
              className="-my-1.5 flex h-11 items-center"
            >
              <span
                className={`flex h-8 min-w-11 items-center justify-center rounded-lg px-2 text-xs font-semibold ${
                  on ? SELECTED[o.mode] : disabled ? 'text-dim' : 'text-lo'
                }`}
              >
                {o.text}
              </span>
            </button>
          );
        })}
      </div>
      {error && (
        <p role="alert" className="mt-1 max-w-48 text-right text-[11.5px] text-stop">
          {error}
        </p>
      )}
    </div>
  );
}
