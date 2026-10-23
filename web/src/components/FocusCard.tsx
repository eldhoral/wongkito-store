import { useEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../api/client';
import type { BuyReq, CancelReq, OrderDetail, OrderView, RejectReq } from '../api/types';
import { formatAge, formatCountdown, formatRp, urgency } from '../lib/format';
import { reasonLabel, reasonTone } from '../lib/reasons';
import { ConfirmSheet } from './ConfirmSheet';

const REJECT_REASONS: [string, string][] = [
  ['NO_STOCK', 'Out of stock'],
  ['WRONG_BUYER_INFORMATION', 'Wrong buyer information'],
  ['SERVER_ERROR', 'Server error'],
  ['PRICE_CHANGED', 'Price changed'],
  ['BUYER_UNREACHABLE', 'Buyer unreachable'],
];
// The server's refusal to reject/cancel while a unit is Sukses or still open.
const SPENT_REFUSAL = /successful or open attempt/;
// Digiflazz rc codes that mean the target number is wrong.
const WRONG_NUMBER_RC = new Set(['52', '54', '57', '59']);

const CHIP = { calm: 'bg-well text-mid', warn: 'bg-hold/10 text-hold', late: 'bg-stop/10 text-stop' };
const PILL = { hold: 'bg-hold/10 text-hold', stop: 'bg-stop/10 text-stop' };

interface Props {
  order: OrderView;
  now: number; // unix seconds
  onEditing: (editing: boolean) => void;
  onDone: () => void;
}

export function FocusCard({ order, now, onEditing, onDone }: Props) {
  const qc = useQueryClient();
  const detail = useQuery({
    queryKey: ['order', order.id],
    queryFn: () => api.get<OrderDetail>(`/api/orders/${order.id}`),
  });
  const options = detail.data?.options ?? [];
  const actionable = order.state === 'needs_you';
  const direct = order.source === 'direct';

  const [customerNo, setCustomerNo] = useState(order.customerNo);
  const [sku, setSku] = useState(order.sku);
  const [noCap, setNoCap] = useState(false);
  const [otherOpen, setOtherOpen] = useState(false);
  const [sheetOpen, setSheetOpen] = useState(false);
  const [picked, setPicked] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [askForce, setAskForce] = useState(false); // server refused: money spent or open
  const input = useRef<HTMLInputElement>(null);

  // Unmounting a focused input fires no blur; release the refetch pause.
  // ponytail: runs once, so onEditing must be stable (a state setter).
  useEffect(() => () => onEditing(false), []);

  const sel = options.find((o) => o.sku === sku);
  const cost = sel?.price ?? order.cost;
  const margin = sel?.margin ?? order.margin;
  const lastRc = [...(detail.data?.attempts ?? [])].sort(
    (a, b) => b.createdAt - a.createdAt || b.attemptNo - a.attemptNo,
  )[0]?.rc;
  const reason = picked ?? (lastRc && WRONG_NUMBER_RC.has(lastRc) ? 'WRONG_BUYER_INFORMATION' : 'NO_STOCK');

  // refused returns true when it handled the error itself.
  async function run(fn: () => Promise<unknown>, refused?: (message: string) => boolean) {
    setBusy(true);
    setError('');
    try {
      await fn();
      await qc.invalidateQueries({ queryKey: ['queue'] });
      onDone();
    } catch (e) {
      const message = e instanceof Error ? e.message : 'Request failed';
      if (!refused?.(message)) setError(message);
    } finally {
      setBusy(false);
    }
  }

  const buy = () =>
    run(() => api.send('POST', `/api/orders/${order.id}/buy`, { sku, customerNo, noCap } satisfies BuyReq));
  // Reject/cancel; a spent-money refusal asks once more and resends with force.
  const close = (force: boolean) =>
    run(
      () =>
        direct
          ? api.send('POST', `/api/orders/${order.id}/cancel`, { force } satisfies CancelReq)
          : api.send('POST', `/api/orders/${order.id}/reject`, { reason, force } satisfies RejectReq),
      (message) => {
        if (force || !SPENT_REFUSAL.test(message)) return false;
        setAskForce(true);
        return true;
      },
    );
  const attempts = detail.data?.attempts ?? [];
  const spentUnits = new Set(attempts.filter((a) => a.status === 'Sukses').map((a) => a.unit)).size;
  const verb = direct ? 'Cancel' : 'Reject';

  const tone = urgency(order.deadlineAt, order.createdAt, now);
  const info = Object.entries(order.info);

  return (
    <section className="mx-3 rounded-[20px] bg-sheet p-5 shadow-card">
      <div className="flex flex-wrap items-center gap-2">
        <span className={`inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-[11.5px] font-semibold ${PILL[reasonTone(order)]}`}>
          <i className="size-1.5 rounded-full bg-current" />
          {reasonLabel(order.reason)}
        </span>
        {order.deadlineAt !== null && (
          <span className={`tn rounded-full px-2.5 py-1 text-[11.5px] font-semibold ${CHIP[tone]}`}>
            {formatCountdown(order.deadlineAt - now)}
          </span>
        )}
      </div>
      <h2 className="mt-3 mb-0.5 text-[22px] leading-tight font-semibold tracking-[-0.025em]">{order.product}</h2>
      <p className="font-mono text-[12.5px] break-all text-lo">
        {order.orderNumber} · {formatAge(now - order.createdAt)}
      </p>

      <div className="my-4 grid gap-1.5">
        {info.length > 0 && (
          <div className="flex justify-between gap-3 text-[12.5px] text-mid">
            <span className="shrink-0">Buyer gave</span>
            <b className="min-w-0 text-right font-mono font-medium break-all text-hi">
              {info.map(([k, v]) => `${k} ${v}`).join(' · ')}
            </b>
          </div>
        )}
        <div className="flex items-center justify-between gap-2 rounded-xl bg-well py-1 pr-1 pl-3.5 shadow-[inset_0_0_0_1px_var(--color-line)]">
          <input
            ref={input}
            aria-label="Customer number"
            value={customerNo}
            readOnly={!actionable}
            onChange={(e) => setCustomerNo(e.target.value)}
            onFocus={() => onEditing(true)}
            onBlur={() => onEditing(false)}
            inputMode="text"
            autoComplete="off"
            spellCheck={false}
            className="h-11 min-w-0 flex-1 bg-transparent font-mono text-xl font-semibold tracking-[-0.01em] outline-none"
          />
          {actionable && (
            <button
              type="button"
              onClick={() => input.current?.focus()}
              className="min-h-11 min-w-11 text-xs font-medium text-act"
            >
              Edit
            </button>
          )}
        </div>
        <div className="flex justify-between gap-3 text-[12.5px] text-mid">
          <span className="min-w-0 truncate">
            {sel?.seller ?? order.seller} · <span className="font-mono">{sku}</span>
          </span>
          <b className={`tn shrink-0 font-medium ${margin < 0 ? 'text-stop' : 'text-hi'}`}>
            {formatRp(cost)} → {margin >= 0 ? '+' : ''}
            {formatRp(margin)}
          </b>
        </div>
      </div>

      {order.detail && <p className="mb-4 text-[12.5px] leading-snug text-stop">{order.detail}</p>}

      {actionable && margin < 0 && (
        <label className="mb-3 flex min-h-11 items-center gap-3 text-[13px] font-medium">
          <input
            type="checkbox"
            checked={noCap}
            onChange={(e) => setNoCap(e.target.checked)}
            className="size-5 accent-stop"
          />
          Buy at a loss
        </label>
      )}

      {error && (
        <p role="alert" className="mb-3 text-[12.5px] text-stop">
          {error}
        </p>
      )}

      {actionable && (
        <>
          <button
            type="button"
            disabled={busy || customerNo.trim() === ''}
            onClick={buy}
            className="h-12 w-full rounded-xl bg-hi text-[15px] font-semibold text-white disabled:opacity-40"
          >
            {busy ? 'Working…' : 'Buy & deliver'}
          </button>
          <div className="mt-2 flex justify-between text-[13px] font-medium">
            <button
              type="button"
              aria-expanded={otherOpen}
              onClick={() => setOtherOpen(!otherOpen)}
              className="min-h-11 pr-3 text-mid"
            >
              Other seller ▾
            </button>
            <button type="button" onClick={() => setSheetOpen(true)} className="min-h-11 pl-3 text-stop">
              {direct ? 'Cancel' : 'Reject & refund'}
            </button>
          </div>
          {otherOpen && (
            <ul aria-label="Other seller" className="mt-1 grid gap-1.5">
              {options.map((o) => (
                <li key={o.sku}>
                  <button
                    type="button"
                    aria-pressed={o.sku === sku}
                    disabled={o.templateError !== null}
                    onClick={() => {
                      setSku(o.sku);
                      setCustomerNo(o.customerNo ?? '');
                      setNoCap(false);
                      setOtherOpen(false);
                    }}
                    className="flex min-h-11 w-full items-center justify-between gap-3 rounded-xl px-3.5 py-2 text-left text-[13px] ring-1 ring-line disabled:opacity-60 aria-pressed:bg-well"
                  >
                    <span className="min-w-0">
                      <b className="font-semibold">{o.seller}</b> <span className="font-mono text-lo">{o.sku}</span>
                      {o.templateError !== null && <span className="block text-xs text-stop">{o.templateError}</span>}
                    </span>
                    <span className="tn shrink-0 text-mid">{formatRp(o.price)}</span>
                  </button>
                </li>
              ))}
              {options.length === 0 && <li className="px-1 text-xs text-lo">No other sellers mapped.</li>}
            </ul>
          )}
          <ConfirmSheet
            open={sheetOpen}
            onOpenChange={(open) => {
              setSheetOpen(open);
              if (!open) setAskForce(false);
            }}
            title={direct ? 'Cancel this order?' : 'Reject and refund?'}
            description={direct ? 'The order is marked cancelled. Nothing is bought.' : 'itemku refunds the buyer. Pick the reason they will see.'}
            confirmLabel={askForce ? `${verb} anyway` : direct ? 'Cancel order' : 'Reject & refund'}
            onConfirm={() => close(askForce)}
            busy={busy}
            error={error}
          >
            {askForce && (
              <p role="alert" className="mb-2 text-[13px] font-medium text-stop">
                {spentUnits > 0
                  ? `Money was already spent on ${spentUnits} unit(s).`
                  : 'A purchase is still open and may still succeed.'}{' '}
                {verb} anyway?
              </p>
            )}
            {!direct && (
              <div role="radiogroup" aria-label="Reject reason" className="grid gap-1">
                {REJECT_REASONS.map(([value, label]) => (
                  <label key={value} className="flex min-h-11 items-center gap-3 text-[14px]">
                    <input
                      type="radio"
                      name="reject-reason"
                      checked={reason === value}
                      onChange={() => setPicked(value)}
                      className="size-5 accent-hi"
                    />
                    {label}
                  </label>
                ))}
              </div>
            )}
          </ConfirmSheet>
        </>
      )}
    </section>
  );
}
