import { useEffect, useState, useSyncExternalStore } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { api, setUnauthorizedHandler } from './api/client';
import { TabBar, type Tab } from './components/TabBar';
import { History } from './screens/History';
import { Login } from './screens/Login';
import { Products } from './screens/Products';
import { Queue } from './screens/Queue';
import { Settings } from './screens/Settings';

const TABS: Tab[] = ['queue', 'products', 'history', 'settings'];

function subscribe(cb: () => void) {
  window.addEventListener('hashchange', cb);
  return () => window.removeEventListener('hashchange', cb);
}

function useTab(): Tab {
  const hash = useSyncExternalStore(subscribe, () => window.location.hash);
  const name = hash.replace(/^#\//, '');
  return (TABS as string[]).includes(name) ? (name as Tab) : 'queue';
}

const SCREENS: Record<Tab, () => React.ReactNode> = {
  queue: () => <Queue />,
  products: () => <Products />,
  history: () => <History />,
  settings: () => <Settings />,
};

export default function App() {
  const qc = useQueryClient();
  // ponytail: null = still probing the session; a 401 anywhere flips to 'out'.
  const [authed, setAuthed] = useState<boolean | null>(null);
  const tab = useTab();
  const Screen = SCREENS[tab];

  useEffect(() => {
    setUnauthorizedHandler(() => {
      qc.clear();
      setAuthed(false);
    });
    api.get('/api/status').then(
      () => setAuthed(true),
      () => setAuthed(false), // 401 or server down: Login shows its own error on submit
    );
  }, [qc]);

  if (authed === null) return null;
  if (!authed) return <Login onSuccess={() => setAuthed(true)} />;

  return (
    <div className="mx-auto flex h-dvh max-w-3xl flex-col">
      <main className="flex-1 overflow-y-auto"><Screen /></main>
      <TabBar active={tab} />
    </div>
  );
}
