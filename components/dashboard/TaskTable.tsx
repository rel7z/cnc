"use client";

import { useDashboard } from "@/components/providers/EventProvider";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { EmptyState } from "@/components/ui/EmptyState";

interface TaskTableProps {
  jobId: string;
}

function duration(start: string | undefined, end: string | undefined): string {
  if (!start) return "—";
  const endTime = end ? new Date(end).getTime() : Date.now();
  const ms = endTime - new Date(start).getTime();
  const secs = Math.floor(ms / 1000);
  if (secs < 60) return `${secs}s`;
  const mins = Math.floor(secs / 60);
  return `${mins}m ${secs % 60}s`;
}

export function TaskTable({ jobId }: TaskTableProps) {
  const { tasks } = useDashboard();
  const taskList = Object.values(tasks)
    .filter((t) => t.job_id === jobId)
    .sort((a, b) => a.id.localeCompare(b.id));

  if (taskList.length === 0) {
    return (
      <EmptyState
        title="No tasks yet"
        description="Tasks appear here once the server finishes splitting the file"
      />
    );
  }

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-sm text-left">
        <thead className="text-xs text-gray-400 uppercase tracking-wider border-b border-gray-800">
          <tr>
            <th className="py-3 px-4 font-medium">Part</th>
            <th className="py-3 px-4 font-medium">Worker receives</th>
            <th className="py-3 px-4 font-medium">Assigned to</th>
            <th className="py-3 px-4 font-medium">Status</th>
            <th className="py-3 px-4 font-medium">Duration</th>
            <th className="py-3 px-4 font-medium">Retries</th>
            <th className="py-3 px-4 font-medium">Error</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-gray-800">
          {taskList.map((task) => {
            const partNum = task.id.split("_").slice(-1)[0] ?? "?";
            const destName = task.payload?.dest_name;
            return (
              <tr key={task.id} className="hover:bg-gray-800/50 transition-colors">
                <td className="py-3 px-4 font-mono text-xs text-gray-400 tabular-nums whitespace-nowrap">
                  part {partNum}
                </td>
                <td className="py-3 px-4 font-mono text-xs text-emerald-400 whitespace-nowrap">
                  {destName ? `~/${destName}` : "—"}
                </td>
                <td className="py-3 px-4 font-mono text-xs text-gray-400 max-w-[160px]">
                  {task.assigned_to ? (
                    <span className="truncate block" title={task.assigned_to}>
                      {task.assigned_to}
                    </span>
                  ) : (
                    <span className="text-gray-600">—</span>
                  )}
                </td>
                <td className="py-3 px-4">
                  <StatusBadge status={task.status} />
                </td>
                <td className="py-3 px-4 text-xs text-gray-400 tabular-nums whitespace-nowrap">
                  {duration(task.started_at, task.completed_at)}
                </td>
                <td className="py-3 px-4 text-xs text-gray-400 tabular-nums">
                  {task.retry_count > 0 ? (
                    <span className="text-yellow-400">{task.retry_count}</span>
                  ) : "0"}
                </td>
                <td className="py-3 px-4 text-xs text-red-400 max-w-[200px]">
                  {task.error ? (
                    <span className="truncate block cursor-help" title={task.error}>
                      {task.error.length > 40 ? task.error.slice(0, 40) + "…" : task.error}
                    </span>
                  ) : (
                    <span className="text-gray-600">—</span>
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
