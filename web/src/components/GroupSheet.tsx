import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Drawer } from 'vaul';
import { api } from '../api/client';
import type { GroupModeReq, GroupModeResponse, OrderView, ProductGroup, ProductMode, QueueResponse, TemplateReq } from '../api/types';
import { EmptyError, keys, render } from '../lib/tmpl';

interface Props {
  group: ProductGroup | null;
  onOpenChange: (open: boolean) => void;
}

const ROW = 'flex h-13 w-full items-center justify-between border-b border-line text-left text-[14.5px] font-medium disabled:opacity-40';

export function GroupSheet({ group, onOpenChange }: Props) {
  return (
    <Drawer.Root open={group !== null} onOpenChange={onOpenChange}>
      <Drawer.Portal>
        <Drawer.Overlay className="fixed inset-0 z-40 bg-hi/30" />
        <Drawer.Content className="fixed inset-x-0 bottom-0 z-50 mx-auto flex max-h-[calc(100%-1.5rem)] max-w-3xl flex-col rounded-t-[24px] bg-sheet px-5 pt-3 outline-none">
          <div className="mx-auto mb-4 h-1 w-9 shrink-0 rounded-full bg-dim" />
          {/* Mounted only while a group is open, so closing resets the form. */}
          {group && <GroupBody group={group} onClose={() => onOpenChange(false)} />}
        </Drawer.Content>
      </Drawer.Portal>
    </Drawer.Root>
  );
}

function GroupBody({ group, onClose }: { group: ProductGroup; onClose: () => void }) {
  const qc = useQueryClient();
  const [template, setTemplate] = useState(group.template);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [failures, setFailures] = useState<string[]>([]);
  const [confirmOff, setConfirmOff] = useState(false);

  // Example values come from the newest order of this game that has buyer info.
  const queue = useQuery({ queryKey: ['queue'], queryFn: () => api.get<QueueResponse>('/api/queue') });
  const history = useQuery({ queryKey: ['history', 'examples'], queryFn: () => api.get<OrderView[]>('/api/history') });
  const sample = [...(queue.data?.needs ?? []), ...(queue.data?.processing ?? []), ...(history.data ?? [])].find(
    (o) => o.game === group.game && Object.keys(o.info).length > 0,
  );

  const names = keys(template);
  const info = Object.fromEntries(names.map((k) => [k, sample?.info[k]?.trim() || k]));
  let example = '';
  let exampleError = ''; // blocks Save: the template itself is invalid
  let exampleNote = ''; // preview only: echoed names have no digits for {key|digits}
  if (names.length > 0) {
    try {
      example = `${names.map((k) => `${k}=${info[k]}`).join(', ')} → ${render(template, info)}`;
    } catch (e) {
      if (e instanceof EmptyError) exampleNote = `Example needs a sample order with ${names.join(', ')}`;
      else exampleError = e instanceof Error ? e.message : 'Invalid template';
    }
  }

  const count = (m: ProductMode) => group.items.filter((i) => i.mode === m).length;
  const canSave = group.seller !== '' && names.length > 0 && !exampleError && template !== group.template;

  async function run(fn: () => Promise<void>) {
    setBusy(true);
    setError('');
    setFailures([]);
    try {
      await fn();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Request failed');
    } finally {
      setBusy(false);
    }
  }

  const saveTemplate = () =>
    run(async () => {
      const body: TemplateReq = { seller: group.seller, game: group.game, template };
      await api.send('PUT', '/api/templates', body);
      await qc.invalidateQueries({ queryKey: ['products'] });
      onClose();
    });

  const setAll = (mode: ProductMode) =>
    run(async () => {
      setConfirmOff(false);
      const body: GroupModeReq = { game: group.game, seller: group.seller, mode };
      const { results } = await api.send<GroupModeResponse>('POST', '/api/groups/mode', body);
      await qc.invalidateQueries({ queryKey: ['products'] });
      // 200 with per-item errors: name the ones that did not switch.
      const failed = results.filter((r) => r.error).map((r) => `${group.items.find((i) => i.id === r.id)?.name ?? `#${r.id}`}: ${r.error}`);
      if (failed.length === 0) onClose();
      else setFailures(failed);
    });

  return (
    <div data-vaul-no-drag className="-mx-5 min-h-0 overflow-y-auto px-5 pb-[max(1.75rem,env(safe-area-inset-bottom))]">
      <Drawer.Title className="text-lg font-semibold tracking-tight text-hi">
        {group.game}
        {group.seller && ` · ${group.seller}`}
      </Drawer.Title>
      <Drawer.Description className="mt-0.5 text-[12.5px] text-lo">
        {group.items.length} items · {count('auto')} Auto · {count('manual')} Manual · {count('off')} Off
      </Drawer.Description>

      {group.seller !== '' && (
        <div className="mt-3.5 rounded-xl bg-well px-3.5 py-3 shadow-[inset_0_0_0_1px_var(--color-line)]">
          <label htmlFor="tpl" className="text-[11px] font-semibold tracking-[0.08em] text-lo uppercase">
            Customer no. template
          </label>
          {group.sellerDesc && (
            <p className="mt-1 mb-2 max-h-40 overflow-y-auto text-[12px] leading-snug whitespace-pre-line text-mid">
              {group.sellerDesc}
            </p>
          )}
          <input
            id="tpl"
            value={template}
            onChange={(e) => setTemplate(e.target.value)}
            autoComplete="off"
            spellCheck={false}
            placeholder="{player_id}|{zone_id}"
            className="mt-1 h-11 w-full rounded-lg bg-sheet px-3 font-mono text-[15px] font-semibold outline-none ring-1 ring-line"
          />
          <p className={`mt-1.5 min-h-4 font-mono text-xs break-all ${exampleError ? 'text-stop' : 'text-mid'}`}>
            {exampleError || exampleNote || example}
          </p>
          <button
            type="button"
            disabled={busy || !canSave}
            onClick={saveTemplate}
            className="mt-2 h-11 w-full rounded-xl bg-hi text-[14px] font-semibold text-white disabled:opacity-40"
          >
            Save template
          </button>
        </div>
      )}

      <div className="mt-2">
        <button type="button" disabled={busy} onClick={() => setAll('auto')} className={ROW}>
          Set all to Auto<span className="text-[13px] font-normal text-mid">{group.items.length} items</span>
        </button>
        <button type="button" disabled={busy} onClick={() => setAll('manual')} className={ROW}>
          Set all to Manual<span className="text-[13px] font-normal text-mid">{group.items.length} items</span>
        </button>
        {confirmOff ? (
          <div className="border-b border-line py-3">
            <p className="text-[13px] text-mid">Sets stock to 0 on itemku for all {group.items.length} items, so buyers cannot order them.</p>
            <button
              type="button"
              disabled={busy}
              onClick={() => setAll('off')}
              className="mt-2 h-12 w-full rounded-xl bg-stop text-[15px] font-semibold text-white disabled:opacity-40"
            >
              {busy ? 'Working…' : 'Turn all off'}
            </button>
            <button type="button" onClick={() => setConfirmOff(false)} className="mt-1 min-h-11 w-full text-[14px] font-medium text-mid">
              Back
            </button>
          </div>
        ) : (
          <button type="button" disabled={busy} onClick={() => setConfirmOff(true)} className={`${ROW} text-stop`}>
            Turn all off on itemku<span className="text-[13px] font-normal text-mid">stock → 0</span>
          </button>
        )}
      </div>

      {(error || failures.length > 0) && (
        <div role="alert" className="mt-2 text-[12.5px] text-stop">
          {error}
          {failures.map((f) => (
            <p key={f}>{f}</p>
          ))}
        </div>
      )}
    </div>
  );
}
