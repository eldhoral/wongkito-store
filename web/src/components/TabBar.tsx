import type { ReactNode } from 'react';

export type Tab = 'queue' | 'products' | 'history' | 'settings';

const svg = (children: ReactNode) => (
  <svg width="20" height="20" viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.6" aria-hidden="true">
    {children}
  </svg>
);

const TABS: { id: Tab; label: string; icon: ReactNode }[] = [
  { id: 'queue', label: 'Queue', icon: svg(<path d="M3 5h14M3 10h14M3 15h9" />) },
  {
    id: 'products',
    label: 'Products',
    icon: svg(
      <>
        <rect x="3" y="3" width="6" height="6" rx="1.5" />
        <rect x="11" y="3" width="6" height="6" rx="1.5" />
        <rect x="3" y="11" width="6" height="6" rx="1.5" />
        <rect x="11" y="11" width="6" height="6" rx="1.5" />
      </>,
    ),
  },
  {
    id: 'history',
    label: 'History',
    icon: svg(
      <>
        <circle cx="10" cy="10" r="7" />
        <path d="M10 6v4l3 2" />
      </>,
    ),
  },
  {
    id: 'settings',
    label: 'Settings',
    icon: svg(
      <>
        <circle cx="10" cy="10" r="2.5" />
        <path d="M10 2.5v2M10 15.5v2M2.5 10h2M15.5 10h2M4.7 4.7l1.4 1.4M13.9 13.9l1.4 1.4M4.7 15.3l1.4-1.4M13.9 6.1l1.4-1.4" />
      </>,
    ),
  },
];

export function TabBar({ active }: { active: Tab }) {
  return (
    <nav className="flex border-t border-line bg-desk px-2 pt-2.5 pb-[max(1.5rem,env(safe-area-inset-bottom))]">
      {TABS.map((t) => (
        <a
          key={t.id}
          href={`#/${t.id}`}
          aria-current={t.id === active ? 'page' : undefined}
          className={`flex min-h-11 flex-1 flex-col items-center gap-1 text-[10.5px] font-medium ${
            t.id === active ? 'text-hi' : 'text-lo'
          }`}
        >
          {t.icon}
          {t.label}
        </a>
      ))}
    </nav>
  );
}
