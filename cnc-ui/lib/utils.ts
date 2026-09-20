import { useEffect, useState } from "react";

/**
 * Format an ISO date string into a short human-readable date+time.
 * Returns "—" if the string is undefined or empty.
 */
export function formatDate(iso: string | undefined): string {
  if (!iso) return "—";
  return new Date(iso).toLocaleString([], {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

/**
 * Compute the duration between `start` and `end` (or now if `end` is absent).
 * Returns "—" if `start` is absent.
 * Formats: "Xs", "Xm Ys", "Xh Ym"
 */
export function duration(start?: string, end?: string): string {
  if (!start) return "—";
  const ms =
    (end ? new Date(end) : new Date()).getTime() - new Date(start).getTime();
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${s % 60}s`;
  return `${Math.floor(m / 60)}h ${m % 60}m`;
}

/**
 * Compute a human-readable relative time from `iso` to now.
 * e.g. "5s ago", "3m ago", "2h ago", "1d ago"
 */
export function relativeTime(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime();
  const secs = Math.floor(diff / 1000);
  if (secs < 60) return `${secs}s ago`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  return `${Math.floor(hrs / 24)}d ago`;
}

/**
 * Return the last path segment of a unix-style path string.
 * e.g. "/home/user/targets.txt" → "targets.txt"
 */
export function basename(p: string): string {
  return p.split("/").filter(Boolean).pop() ?? p;
}

/**
 * React hook that forces a re-render every `ms` milliseconds.
 * Useful for components that display live-updating relative times or durations.
 */
export function useTick(ms: number): void {
  const [, setTick] = useState(0);
  useEffect(() => {
    const id = setInterval(() => setTick((n) => n + 1), ms);
    return () => clearInterval(id);
  }, [ms]);
}
