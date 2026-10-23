import { useEffect, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Drawer } from 'vaul';
import { api } from '../api/client';
import type { DirectReq, DirectResult, OrderDetail, OrderState, PricelistItem, ProductGroup, ProductView } from '../api/types';
import { formatRp } from '../lib/format';
import { reasonLabel } from '../lib/reasons';
import { keys, render } from '../lib/tmpl';

interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

type ProductPick = { kind: 'product'; item: ProductView; game: string; seller: string; template: string };
type Picked = ProductPick | { kind: 'sku'; item: PricelistItem };

const FINAL: OrderState[] = ['delivered', 'rejected', 'cancelled', 'needs_you'];
const FIELD = 'h-11 w-full rounded-xl bg-well px-3.5 text-[14px] shadow-[inset_0_0_0_1px_var(--color-line)] outline-none';
const ROW = 'flex min-h-11 w-full items-center justify-between gap-3 rounded-xl px-3.5 py-2 text-left text-[13px] ring-1 ring-line';

// Same text the server puts in the wa.me link (relay.DeliveryText).
function deliveryText(o: OrderDetail, sns: string[]) {
  return [o.product, `ID: ${o.customerNo}`, ...sns.map((sn) => `SN: ${sn}`)].join('\n');
}

// The form's idempotency key. randomUUID needs a secure context; fall back so
// plain-http dev still works.
function newClientId(): string {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID();
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), (b) => b.toString(16).padStart(2, '0')).join('');
}

export function NewOrderSheet({ open, onOpenChange }: Props) {
  // While a submit is in flight the sheet cannot close: closing would unmount
  // the form and lose the idempotency key and the order id.
  const [busy, setBusy] = useState(false);
  return (
    <Drawer.Root open={open} dismissible={!busy} onOpenChange={(o) => {
        if (o || !busy) onOpenChange(o);
      }}
    >
      <Drawer.Portal>
        <Drawer.Overlay className="fixed inset-0 z-40 bg-hi/30" />
        <Drawer.Content className="fixed inset-x-0 bottom-0 z-50 mx-auto flex h-[calc(100%-1.5rem)] max-w-3xl flex-col rounded-t-[20px] bg-sheet px-5 pt-3 outline-none">
          <div className="mx-auto mb-3 h-1 w-9 shrink-0 rounded-full bg-dim" />
          <Drawer.Title className="text-lg font-semibold tracking-tight text-hi">New order</Drawer.Title>
          <Drawer.Description className="sr-only">Buy a Digiflazz top-up for a WhatsApp buyer.</Drawer.Description>
          {/* Mounted only while open, so closing resets the form. */}
          <NewOrderForm onClose={() => onOpenChange(false)} onBusy={setBusy} />
        </Drawer.Content>
      </Drawer.Portal>
    </Drawer.Root>
  );
}

function NewOrderForm({ onClose, onBusy }: { onClose: () => void; onBusy: (busy: boolean) => void }) {
  const qc = useQueryClient();
  // One key per form: a retry after a network failure reuses it, so the server
  // returns the order it may already have bought instead of buying again.
  const [clientId] = useState(newClientId);
  const [tab, setTab] = useState<'product' | 'sku'>('product');
  const [search, setSearch] = useState('');
  const [picked, setPicked] = useState<Picked | null>(null);
  const [fields, setFields] = useState<Record<string, string>>({});
  const [customerNo, setCustomerNo] = useState('');
  const [quantity, setQuantity] = useState('1');
  const [buyerName, setBuyerName] = useState('');
  const [buyerWa, setBuyerWa] = useState('');
  const [paid, setPaid] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [orderId, setOrderId] = useState<string | null>(null);
  const [note, setNote] = useState('');

  const q = search.trim();
  const products = useQuery({ queryKey: ['products'], queryFn: () => api.get<ProductGroup[]>('/api/products') });
  // ponytail: one request per keystroke; the server caps at 30 rows. Debounce if it hurts.
  const pricelist = useQuery({
    queryKey: ['pricelist', q],
    queryFn: () => api.get<PricelistItem[]>(`/api/pricelist?q=${encodeURIComponent(q)}`),
    enabled: tab === 'sku',
  });

  const order = useQuery({
    queryKey: ['order', orderId],
    queryFn: () => api.get<OrderDetail>(`/api/orders/${orderId}`),
    enabled: orderId !== null,
    refetchInterval: (query) => (query.state.data && FINAL.includes(query.state.data.state) ? false : 2000),
  });
  const state = order.data?.state;
  useEffect(() => {
    if (state && FINAL.includes(state)) void qc.invalidateQueries({ queryKey: ['queue'] });
  }, [state, qc]);

  const matches = (products.data ?? []).flatMap((g) =>
    g.items
      .filter((i) => `${g.game} ${i.name}`.toLowerCase().includes(q.toLowerCase()))
      .map((item): ProductPick => ({ kind: 'product', item, game: g.game, seller: g.seller, template: g.template })),
  );

  let rendered = '';
  if (picked?.kind === 'product') {
    try {
      rendered = render(picked.template, fields);
    } catch {
      // incomplete fields: nothing to show yet
    }
  }
  const number = picked?.kind === 'product' ? rendered : customerNo.trim();
  const qty = Number(quantity);
  const paidPrice = Number(paid);
  const valid = picked !== null && number !== '' && Number.isInteger(qty) && qty >= 1;

  function pick(p: Picked) {
    setPicked(p);
    setFields(p.kind === 'product' ? Object.fromEntries(keys(p.template).map((k) => [k, ''])) : {});
    setCustomerNo('');
    setError('');
  }

  async function submit() {
    if (!picked || !valid) return;
    const req: DirectReq = picked.kind === 'product' ? { productId: picked.item.id, fields } : { sku: picked.item.sku, customerNo: number };
    req.quantity = qty;
    if (buyerName.trim()) req.buyerName = buyerName.trim();
    if (buyerWa.trim()) req.buyerWa = buyerWa.trim();
    if (paidPrice > 0) req.paidPrice = paidPrice;
    req.clientId = clientId;
    setBusy(true);
    onBusy(true);
    setError('');
    try {
      const { id, error: buyError } = await api.send<DirectResult>('POST', '/api/orders/direct', req);
      // The order exists even if the buy errored; follow it so no second submit can double-buy.
      setNote(buyError ?? '');
      setOrderId(id);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Request failed');
    } finally {
      setBusy(false);
      onBusy(false);
    }
  }

  if (orderId !== null) return <Result order={order.data} note={note} failed={order.isError} onClose={onClose} />;

  return (
    <>
      <div data-vaul-no-drag className="-mx-5 mt-3 min-h-0 flex-1 overflow-y-auto px-5 pb-4">
        <div role="tablist" className="mb-3 grid grid-cols-2 gap-1 rounded-xl bg-well p-1">
          {(['product', 'sku'] as const).map((t) => (
            <button
              key={t}
              type="button"
              role="tab"
              aria-selected={tab === t}
              onClick={() => {
                setTab(t);
                setPicked(null);
              }}
              className="min-h-11 rounded-lg text-[13px] font-medium text-mid aria-selected:bg-sheet aria-selected:text-hi aria-selected:shadow-card"
            >
              {t === 'product' ? 'Mapped product' : 'Any Digiflazz SKU'}
            </button>
          ))}
        </div>
        <input
          aria-label="Search"
          type="search"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder={tab === 'product' ? 'Search products' : 'Search SKU, product or brand'}
          className={FIELD}
        />

        {picked === null ? (
          <ul className="mt-3 grid gap-1.5">
            {tab === 'product'
              ? matches.map((m) => (
                  <li key={m.item.id}>
                    <button type="button" onClick={() => pick(m)} className={ROW}>
                      <span className="min-w-0">
                        <b className="block font-semibold">{m.item.name}</b>
                        <span className="text-xs text-lo">
                          {m.game} · {m.seller}
                        </span>
                      </span>
                      <span className="tn shrink-0 text-mid">{formatRp(m.item.cost)}</span>
                    </button>
                  </li>
                ))
              : (pricelist.data ?? []).map((p) => (
                  <li key={p.sku}>
                    <button type="button" onClick={() => pick({ kind: 'sku', item: p })} className={ROW}>
                      <span className="min-w-0">
                        <b className="block font-semibold">{p.productName}</b>
                        <span className="text-xs text-lo">
                          {p.seller} · <span className="font-mono">{p.sku}</span>
                        </span>
                      </span>
                      <span className="tn shrink-0 text-mid">{formatRp(p.price)}</span>
                    </button>
                  </li>
                ))}
          </ul>
        ) : (
          <div className="mt-3 grid gap-3">
            <div className="flex items-center justify-between gap-3">
              <b className="min-w-0 text-[15px] font-semibold">
                {picked.kind === 'product' ? picked.item.name : picked.item.productName}
              </b>
              <button type="button" onClick={() => setPicked(null)} className="min-h-11 shrink-0 text-[13px] font-medium text-act">
                Change
              </button>
            </div>
            {picked.kind === 'product' ? (
              <>
                {Object.keys(fields).map((k) => (
                  <input
                    key={k}
                    aria-label={k}
                    placeholder={k}
                    value={fields[k]}
                    onChange={(e) => setFields({ ...fields, [k]: e.target.value })}
                    autoComplete="off"
                    spellCheck={false}
                    className={FIELD}
                  />
                ))}
                <p className="text-[12.5px] text-mid">
                  Customer number{' '}
                  <b className="block min-h-7 font-mono text-xl font-semibold break-all text-hi">{rendered || '—'}</b>
                </p>
              </>
            ) : (
              <input
                aria-label="Customer number"
                placeholder="Customer number"
                value={customerNo}
                onChange={(e) => setCustomerNo(e.target.value)}
                autoComplete="off"
                spellCheck={false}
                className={`${FIELD} font-mono`}
              />
            )}
            <input
              aria-label="Quantity"
              type="number"
              inputMode="numeric"
              min={1}
              value={quantity}
              onChange={(e) => setQuantity(e.target.value)}
              className={FIELD}
            />
            <input aria-label="Buyer name" placeholder="Buyer name (optional)" value={buyerName} onChange={(e) => setBuyerName(e.target.value)} className={FIELD} />
            <input
              aria-label="Buyer WhatsApp"
              placeholder="Buyer WhatsApp (optional)"
              type="tel"
              value={buyerWa}
              onChange={(e) => setBuyerWa(e.target.value)}
              className={FIELD}
            />
            <input
              aria-label="Price paid"
              placeholder="Price paid (optional)"
              inputMode="numeric"
              value={paid}
              onChange={(e) => setPaid(e.target.value.replace(/\D/g, ''))}
              className={FIELD}
            />
          </div>
        )}
      </div>
      <div className="pt-2 pb-[max(1.5rem,env(safe-area-inset-bottom))]">
        <p role="alert" className="min-h-5 text-[12.5px] text-stop">
          {error}
        </p>
        <button
          type="button"
          disabled={busy || !valid}
          onClick={submit}
          className="h-12 w-full rounded-xl bg-hi text-[15px] font-semibold text-white disabled:opacity-40"
        >
          {busy ? 'Working…' : 'Buy & deliver'}
        </button>
      </div>
    </>
  );
}

const BTN = 'flex h-12 w-full items-center justify-center rounded-xl bg-hi text-[15px] font-semibold text-white';

function Result({ order, note, failed, onClose }: { order?: OrderDetail; note: string; failed: boolean; onClose: () => void }) {
  const [copied, setCopied] = useState(false);
  const [manual, setManual] = useState('');
  const sns = (order?.attempts ?? []).filter((a) => a.status === 'Sukses' && a.sn).map((a) => a.sn);
  const waiting = !order || !FINAL.includes(order.state);

  return (
    <div className="mt-3 flex min-h-0 flex-1 flex-col pb-[max(1.5rem,env(safe-area-inset-bottom))]">
      <div className="flex-1 overflow-y-auto">
        {failed && !order && (
          <p role="alert" className="text-[13px] text-stop">
            Could not load the order. Check the Queue.
          </p>
        )}
        {note && (
          <p role="alert" className="mb-3 text-[12.5px] text-stop">
            {note}
          </p>
        )}
        {waiting && !failed && <p role="status" className="text-[14px] text-mid">Buying…</p>}
        {order && (
          <>
            <h2 className="text-[22px] leading-tight font-semibold tracking-[-0.025em]">{order.product}</h2>
            <p className="font-mono text-[15px] break-all text-mid">{order.customerNo}</p>
          </>
        )}
        {order?.state === 'delivered' && (
          <ul aria-label="Serial numbers" className="mt-4 grid gap-1.5">
            {sns.map((sn) => (
              <li key={sn} className="rounded-xl bg-well px-3.5 py-2.5 font-mono text-[15px] font-semibold break-all">
                {sn}
              </li>
            ))}
          </ul>
        )}
        {order?.state === 'needs_you' && (
          <p role="alert" className="mt-4 text-[13px] text-stop">
            <b>{reasonLabel(order.reason)}.</b> {order.detail} Handle it from the Queue.
          </p>
        )}
        {manual && (
          <textarea
            readOnly
            aria-label="Message to copy"
            value={manual}
            rows={4}
            onFocus={(e) => e.currentTarget.select()}
            className={`${FIELD} mt-4 h-auto py-2.5 font-mono`}
          />
        )}
        {(order?.state === 'rejected' || order?.state === 'cancelled') && (
          <p className="mt-4 text-[13px] text-stop">
            {order.state === 'rejected' ? 'Rejected' : 'Cancelled'}. {order.detail}
          </p>
        )}
      </div>
      {order?.state === 'delivered' &&
        (order.whatsappUrl ? (
          <a href={order.whatsappUrl} target="_blank" rel="noreferrer" className={`${BTN} mb-1`}>
            Send to WhatsApp
          </a>
        ) : (
          <button
            type="button"
            onClick={async () => {
              const text = deliveryText(order, sns);
              try {
                await navigator.clipboard.writeText(text);
                setCopied(true);
              } catch {
                // no clipboard (non-HTTPS) or denied: show the text to copy by hand
                setManual(text);
              }
            }}
            className={`${BTN} mb-1`}
          >
            {copied ? 'Copied' : 'Copy message'}
          </button>
        ))}
      <button type="button" onClick={onClose} className="min-h-11 w-full text-[14px] font-medium text-mid">
        {waiting ? 'Close' : 'Done'}
      </button>
    </div>
  );
}
