import { useState, type FormEvent } from 'react';
import { api, ApiError } from '../api/client';

export function Login({ onSuccess }: { onSuccess: () => void }) {
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError('');
    try {
      await api.send('POST', '/api/login', { password });
      onSuccess();
    } catch (err) {
      setError(
        err instanceof ApiError && err.status === 429
          ? 'Too many attempts. Wait a bit.'
          : err instanceof ApiError && err.status === 401
            ? 'Wrong password.'
            : 'Could not sign in.',
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="mx-auto flex min-h-dvh max-w-sm flex-col justify-center px-5">
      <form onSubmit={submit} className="rounded-[20px] bg-sheet p-6 shadow-card">
        <h1 className="text-xl font-semibold tracking-tight text-hi">wongkito</h1>
        <label htmlFor="password" className="mt-6 block text-xs font-medium text-mid">
          Password
        </label>
        <input
          id="password"
          type="password"
          autoComplete="current-password"
          autoFocus
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          className="mt-1.5 h-12 w-full rounded-xl bg-well px-3.5 text-base text-hi ring-1 ring-line outline-none focus-visible:ring-2 focus-visible:ring-hi"
        />
        <p role="alert" className="mt-2 min-h-5 text-[12.5px] text-stop">
          {error}
        </p>
        <button
          type="submit"
          disabled={busy || password === ''}
          className="mt-2 h-12 w-full rounded-xl bg-hi text-[15px] font-semibold text-white disabled:opacity-40"
        >
          {busy ? 'Signing in…' : 'Sign in'}
        </button>
      </form>
    </main>
  );
}
