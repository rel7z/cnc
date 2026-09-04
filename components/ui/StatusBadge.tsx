import type { JobStatus, TaskStatus, WorkerStatus } from "@/lib/types";

type AnyStatus = WorkerStatus | JobStatus | TaskStatus | string;

const colorMap: Record<string, string> = {
  // green
  online: "bg-emerald-500/15 text-emerald-400 ring-emerald-500/30",
  completed: "bg-emerald-500/15 text-emerald-400 ring-emerald-500/30",
  // blue
  running: "bg-blue-500/15 text-blue-400 ring-blue-500/30",
  busy: "bg-blue-500/15 text-blue-400 ring-blue-500/30",
  assigned: "bg-blue-500/15 text-blue-400 ring-blue-500/30",
  // yellow
  pending: "bg-yellow-500/15 text-yellow-400 ring-yellow-500/30",
  // red
  failed: "bg-red-500/15 text-red-400 ring-red-500/30",
  // gray
  offline: "bg-gray-500/15 text-gray-400 ring-gray-500/30",
  cancelled: "bg-gray-500/15 text-gray-400 ring-gray-500/30",
};

interface StatusBadgeProps {
  status: AnyStatus;
  className?: string;
}

export function StatusBadge({ status, className = "" }: StatusBadgeProps) {
  const colors =
    colorMap[status] ?? "bg-gray-500/15 text-gray-400 ring-gray-500/30";
  return (
    <span
      className={`inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium ring-1 ${colors} ${className}`}
    >
      {status}
    </span>
  );
}
