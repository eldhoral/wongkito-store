export type Urgency = 'calm' | 'warn' | 'late';

// "Rp 1.284.500" with an ASCII space (Intl currency emits U+00A0).
export function formatRp(n: number): string {
  const digits = new Intl.NumberFormat('id-ID', { maximumFractionDigits: 0 }).format(Math.abs(n));
  return `${n < 0 ? '-' : ''}Rp ${digits}`;
}

// ponytail: m:ss under 5 min so fast classes tick by the second; coarser beyond.
export function formatCountdown(secondsLeft: number): string {
  const s = Math.floor(secondsLeft);
  if (s <= 0) return 'late';
  if (s < 300) return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`;
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`;
}

// All arguments are unix seconds. No deadline means calm.
export function urgency(deadlineAt: number | null, createdAt: number, now: number): Urgency {
  if (deadlineAt === null) return 'calm';
  const left = deadlineAt - now;
  if (left <= 0) return 'late';
  return left < (deadlineAt - createdAt) * 0.25 ? 'warn' : 'calm';
}

// Seconds since an event as "now", "5m", "3h", "2d".
export function formatAge(seconds: number): string {
  const s = Math.max(0, Math.floor(seconds));
  if (s < 60) return 'now';
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h`;
  return `${Math.floor(s / 86400)}d`;
}
