import type { Job, Stats, Task, Worker } from "./types";

const BASE = process.env.NEXT_PUBLIC_API_BASE ?? "";

async function apiFetch<T>(path: string): Promise<T> {
  const res = await fetch(`${BASE}${path}`, { cache: "no-store" });
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
