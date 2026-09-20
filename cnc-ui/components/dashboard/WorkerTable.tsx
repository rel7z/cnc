"use client";

import { useDashboard } from "@/components/providers/EventProvider";
import { EmptyState } from "@/components/ui/EmptyState";
import { useTick } from "@/lib/utils";

function relativeTime(isoString: string): string {
  const diff = Date.now() - new Date(isoString).getTime();
  const secs = Math.floor(diff / 1000);
  if (secs < 60) return `${secs}s ago`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  return `${Math.floor(mins / 60)}h ago`;
}

export function WorkerTable() {
  useTick(1000);
  const { workers } = useDashboard();
  
  // Filter to only show online workers, sorted by IP address
  const onlineWorkers = Object.values(workers)
    .filter((w) => w.status === "online")
    .sort((a, b) => {
      // Sort by address (IP)
      if (a.address < b.address) return -1;
      if (a.address > b.address) return 1;
      return 0;
    });

  if (onlineWorkers.length === 0) {
    return (
      <EmptyState
        title="No workers online"
        description="Start a worker with: ./cnc-worker --server localhost:9090"
      />
    );
  }

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-sm text-left">
        <thead className="text-xs text-gray-400 uppercase tracking-wider border-b border-gray-800">
          <tr>
            <th className="py-3 px-4 font-medium">IP Address</th>
            <th className="py-3 px-4 font-medium">Load</th>
            <th className="py-3 px-4 font-medium">Last Seen</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-gray-800">
          {onlineWorkers.map((worker) => {
            const pct =
              worker.max_tasks > 0
                ? Math.round((worker.current_load / worker.max_tasks) * 100)
                : 0;
            return (
              <tr
                key={worker.id}
                className="hover:bg-gray-800/50 transition-colors"
              >
                <td className="py-3 px-4 font-mono text-sm text-gray-200">
                  {worker.address || "—"}
                </td>
                <td className="py-3 px-4">
                  <div className="flex items-center gap-2">
                    <div className="w-24 bg-gray-700 rounded-full h-1.5">
                      <div
                        className="bg-blue-500 h-1.5 rounded-full transition-all duration-300"
                        style={{ width: `${pct}%` }}
                      />
                    </div>
                    <span className="text-xs text-gray-400 tabular-nums whitespace-nowrap">
                      {worker.current_load} / {worker.max_tasks}
                    </span>
                  </div>
                </td>
                <td className="py-3 px-4 text-xs text-gray-400 whitespace-nowrap">
                  {relativeTime(worker.last_seen)}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
