"use client";

import { useEffect, useCallback } from "react";
import { useDashboard } from "@/components/providers/EventProvider";
import { StatCard } from "./StatCard";

export function StatsGrid() {
  const { stats, connected, dispatch } = useDashboard();

  // Poll /api/stats every 5 s when SSE is disconnected so numbers don't go stale.
  const poll = useCallback(async () => {
    try {
      const res = await fetch("/api/stats", { cache: "no-store" });
      if (!res.ok) return;
      const data = await res.json();
      dispatch({ type: "STATS_UPDATE", payload: data });
    } catch {
      // network error — silently ignore
    }
  }, [dispatch]);

  useEffect(() => {
    if (connected) return; // SSE is live — no need to poll
    poll(); // immediate fetch on disconnect
    const id = setInterval(poll, 5000);
    return () => clearInterval(id);
  }, [connected, poll]);

  return (
    <div className="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-4 gap-4">
      <StatCard
        label="Workers Online"
        value={stats?.workers_online ?? null}
        color="green"
        description={
          stats ? `${stats.workers_online} of ${stats.workers_total} registered` : undefined
        }
      />
      <StatCard
        label="Jobs Running"
        value={stats?.jobs_running ?? null}
        color="blue"
        description={
          stats ? `${stats.jobs_total} total` : undefined
        }
      />
      <StatCard
        label="Tasks Completed"
        value={stats?.tasks_completed ?? null}
        color="green"
        description={
          stats ? `${stats.tasks_total} total tasks` : undefined
        }
      />
      <StatCard
        label="Tasks Failed"
        value={stats?.tasks_failed ?? null}
        color={stats && stats.tasks_failed > 0 ? "red" : "default"}
        description={
          stats
            ? `${stats.tasks_pending} pending · ${stats.tasks_running} running`
            : undefined
        }
      />
    </div>
  );
}
