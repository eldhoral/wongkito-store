import type { OrderView } from '../api/types';
import { formatAge } from '../lib/format';
import { reasonLabel } from '../lib/reasons';

function tag(o: OrderView): { text: string; cls: string } {
  if (o.state !== 'needs_you') return { text: 'Pending', cls: 'text-mid' };
  return o.reason === 'manual_product' ? { text: 'Manual', cls: 'text-hold' } : { text: 'Failed', cls: 'text-stop' };
}

export function QueueRow({ order, now, onSelect }: { order: OrderView; now: number; onSelect: () => void }) {
  const t = tag(order);
  const why = order.state === 'needs_you' ? reasonLabel(order.reason).toLowerCase() : order.state;
  return (
    <button
      type="button"
      onClick={onSelect}
      className="mx-3 mb-2 flex min-h-14 w-[calc(100%-1.5rem)] items-center justify-between gap-3 rounded-[14px] bg-sheet px-3.5 py-3 text-left shadow-[0_0_0_1px_var(--color-line)]"
    >
      <span className="min-w-0">
        <b className="block truncate text-sm font-semibold">{order.product}</b>
        <span className="block truncate text-xs text-lo">
          {why} · {formatAge(now - order.createdAt)}
        </span>
      </span>
      <span className={`shrink-0 text-[11.5px] font-semibold ${t.cls}`}>{t.text}</span>
    </button>
  );
}
