import type { ReactNode } from 'react';
import { Drawer } from 'vaul';

interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description: string;
  confirmLabel: string;
  onConfirm: () => void;
  busy?: boolean;
  error?: string;
  children?: ReactNode;
}

export function ConfirmSheet({ open, onOpenChange, title, description, confirmLabel, onConfirm, busy, error, children }: Props) {
  return (
    <Drawer.Root open={open} onOpenChange={onOpenChange}>
      <Drawer.Portal>
        <Drawer.Overlay className="fixed inset-0 z-40 bg-hi/30" />
        <Drawer.Content className="fixed inset-x-0 bottom-0 z-50 mx-auto flex max-w-3xl flex-col rounded-t-[20px] bg-sheet px-5 pt-3 pb-[max(1.5rem,env(safe-area-inset-bottom))] outline-none">
          <div className="mx-auto mb-4 h-1 w-9 rounded-full bg-dim" />
          <Drawer.Title className="text-lg font-semibold tracking-tight text-hi">{title}</Drawer.Title>
          <Drawer.Description className="mt-1 text-[12.5px] text-mid">{description}</Drawer.Description>
          <div className="mt-3">{children}</div>
          <p role="alert" className="mt-2 min-h-5 text-[12.5px] text-stop">
            {error}
          </p>
          <button
            type="button"
            disabled={busy}
            onClick={onConfirm}
            className="mt-2 h-12 w-full rounded-xl bg-stop text-[15px] font-semibold text-white disabled:opacity-40"
          >
            {busy ? 'Working…' : confirmLabel}
          </button>
          <button
            type="button"
            onClick={() => onOpenChange(false)}
            className="mt-1 min-h-11 w-full text-[14px] font-medium text-mid"
          >
            Back
          </button>
        </Drawer.Content>
      </Drawer.Portal>
    </Drawer.Root>
  );
}
