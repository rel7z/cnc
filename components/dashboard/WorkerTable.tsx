"use client";

import { useDashboard } from "@/components/providers/EventProvider";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { EmptyState } from "@/components/ui/EmptyState";

function relativeTime(isoString: string): string {
  const diff = Date.now() - new Date(isoString).getTime();
  const secs = Math.floor(diff / 1000);
  if (secs < 60) return `${secs}s ago`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  return `${Math.floor(hrs / 24)}d ago`;
}

function formatDate(isoString: string): string {
  return new Date(isoString).toLocaleString([], {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

export function WorkerTable() {
  const { workers } = useDashboard();
  const workerList = Object.values(workers).sort(
    (a, b) =>
      new Date(b.registered).getTime() - new Date(a.registered).getTime()
  );

  if (workerList.length === 0) {
    return (
      <EmptyState
        title="No workers connected"
        description="Start a worker with: ./cnc-worker worker start"
      />
    );
  }

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-sm text-left">
        <thead className="text-xs text-gray-400 uppercase tracking-wider border-b border-gray-800">
          <tr>
            <th className="py-3 px-4 font-medium">Worker ID</th>
            <th className="py-3 px-4 font-medium">Address</th>
            <th className="py-3 px-4 font-medium">Status</th>
            <th className="py-3 px-4 font-medium">Load</th>
            <th className="py-3 px-4 font-medium">Registered</th>
            <th className="py-3 px-4 font-medium">Last Seen</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-gray-800">
          {workerList.map((worker) => {
            const pct =
              worker.max_tasks > 0
                ? Math.round((worker.current_load / worker.max_tasks) * 100)
                : 0;
            return (
              <tr
                key={worker.id}
                className="hover:bg-gray-800/50 transition-colors"
              >
                <td className="py-3 px-4 font-mono text-xs text-gray-200 max-w-[200px]">
                  <span className="truncate block" title={worker.id}>
                    {worker.id}
                  </span>
                </td>
                <td className="py-3 px-4 font-mono text-xs text-gray-400">
                  {worker.address || "—"}
                </td>
                <td className="py-3 px-4">
                  <StatusBadge status={worker.status} />
                </td>
                <td className="py-3 px-4">
                  <div className="flex items-center gap-2">
                    <div className="w-20 bg-gray-700 rounded-full h-1.5">
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
                  {formatDate(worker.registered)}
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
