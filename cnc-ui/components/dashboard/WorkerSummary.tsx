"use client";

import Link from "next/link";
import { useDashboard } from "@/components/providers/EventProvider";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { EmptyState } from "@/components/ui/EmptyState";

function relativeTime(isoString: string): string {
  const diff = Date.now() - new Date(isoString).getTime();
  const secs = Math.floor(diff / 1000);
  if (secs < 60) return `${secs}s ago`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  return `${Math.floor(mins / 60)}h ago`;
}

export function WorkerSummary() {
  const { workers } = useDashboard();
  const workerList = Object.values(workers).slice(0, 5);

  return (
    <div className="bg-gray-900 border border-gray-800 rounded-xl">
      <div className="flex items-center justify-between px-5 py-3 border-b border-gray-800">
        <h2 className="text-sm font-semibold text-white">Workers</h2>
        <Link
          href="/dashboard/workers"
          className="text-xs text-blue-400 hover:text-blue-300 transition-colors"
        >
          View all →
        </Link>
      </div>

      {workerList.length === 0 ? (
        <EmptyState
          title="No workers connected"
          description="Start a worker agent to begin processing tasks"
        />
      ) : (
        <table className="w-full text-sm">
          <thead>
            <tr className="text-xs text-gray-400 uppercase tracking-wider border-b border-gray-800">
              <th className="text-left py-2 px-5 font-medium">ID</th>
              <th className="text-left py-2 px-4 font-medium">Status</th>
              <th className="text-left py-2 px-4 font-medium">Load</th>
              <th className="text-left py-2 px-4 font-medium">Last Seen</th>
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
                  <td className="py-2.5 px-5 font-mono text-xs text-gray-300 truncate max-w-[140px]">
                    {worker.id}
                  </td>
                  <td className="py-2.5 px-4">
                    <StatusBadge status={worker.status} />
                  </td>
                  <td className="py-2.5 px-4">
                    <div className="flex items-center gap-2">
                      <div className="w-16 bg-gray-700 rounded-full h-1.5">
                        <div
                          className="bg-blue-500 h-1.5 rounded-full transition-all duration-300"
                          style={{ width: `${pct}%` }}
                        />
                      </div>
                      <span className="text-xs text-gray-400 tabular-nums">
                        {worker.current_load}/{worker.max_tasks}
                      </span>
                    </div>
                  </td>
                  <td className="py-2.5 px-4 text-xs text-gray-400">
                    {relativeTime(worker.last_seen)}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
    </div>
  );
}
