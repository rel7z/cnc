import type { Job, Stats, Task, Worker } from "./types";

// Server-side (SSR/RSC): always hit the Go server directly.
// Client-side: go through the Next.js proxy at /api/*.
const isServer = typeof window === "undefined";
const BASE = isServer
  ? (process.env.GO_API_URL ?? "http://localhost:8080")
  : "";

async function apiFetch<T>(path: string): Promise<T> {
  const url = isServer ? `${BASE}${path}` : path;
  const res = await fetch(url, { cache: "no-store" });
  if (!res.ok) {
    throw new Error(`API ${path} returned ${res.status}`);
  }
  return res.json() as Promise<T>;
}

export function fetchStats(): Promise<Stats> {
  return apiFetch<Stats>("/api/stats");
}

export function fetchWorkers(): Promise<Worker[]> {
  return apiFetch<Worker[]>("/api/workers");
}

export function fetchJobs(): Promise<Job[]> {
  return apiFetch<Job[]>("/api/jobs");
}

export function fetchJob(id: string): Promise<Job> {
  return apiFetch<Job>(`/api/jobs/${id}`);
}

export function fetchTasks(): Promise<Task[]> {
  return apiFetch<Task[]>("/api/tasks");
}

/** Convert an array of items with `.id` to a Record keyed by id */
export function toRecord<T extends { id: string }>(
  items: T[]
): Record<string, T> {
  return items.reduce<Record<string, T>>((acc, item) => {
    acc[item.id] = item;
    return acc;
  }, {});
}
