"use client";

import Link from "next/link";
import { useDashboard } from "@/components/providers/EventProvider";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { EmptyState } from "@/components/ui/EmptyState";
import type { Job } from "@/lib/types";

function ProgressBar({ job }: { job: Job }) {
  const total = job.total_tasks;
  if (total === 0) {
    return <span className="text-xs text-gray-500">—</span>;
  }
  const completedPct = Math.round((job.completed / total) * 100);
  const failedPct = Math.round((job.failed / total) * 100);
  const remaining = 100 - completedPct - failedPct;
  return (
    <div className="flex items-center gap-2">
      <div
        className="w-28 bg-gray-700 rounded-full h-1.5 overflow-hidden flex"
        title={`${job.completed} completed, ${job.failed} failed, ${total - job.completed - job.failed} remaining`}
        role="progressbar"
        aria-valuenow={completedPct}
        aria-valuemin={0}
        aria-valuemax={100}
      >
        <div
          className="bg-emerald-500 h-full transition-all duration-300"
          style={{ width: `${completedPct}%` }}
        />
        <div
          className="bg-red-500 h-full transition-all duration-300"
          style={{ width: `${failedPct}%` }}
        />
        <div
          className="bg-gray-600 h-full transition-all duration-300"
          style={{ width: `${remaining}%` }}
        />
      </div>
      <span className="text-xs text-gray-400 tabular-nums whitespace-nowrap">
        {job.completed}/{total}
      </span>
    </div>
  );
}

function formatDate(isoString: string): string {
  return new Date(isoString).toLocaleString([], {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

export function JobTable() {
  const { jobs } = useDashboard();
  const jobList = Object.values(jobs).sort(
    (a, b) =>
      new Date(b.created_at).getTime() - new Date(a.created_at).getTime()
  );

  if (jobList.length === 0) {
    return (
      <EmptyState
        title="No jobs submitted yet"
        description="Submit a job with: ./cnc job submit"
      />
    );
  }

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-sm text-left">
        <thead className="text-xs text-gray-400 uppercase tracking-wider border-b border-gray-800">
          <tr>
            <th className="py-3 px-4 font-medium">Job ID</th>
            <th className="py-3 px-4 font-medium">Name</th>
            <th className="py-3 px-4 font-medium">Workers</th>
            <th className="py-3 px-4 font-medium">Status</th>
            <th className="py-3 px-4 font-medium">Progress</th>
            <th className="py-3 px-4 font-medium">Created</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-gray-800">
          {jobList.map((job) => (
            <tr
              key={job.id}
              className="hover:bg-gray-800/50 transition-colors cursor-pointer"
            >
              <td className="py-3 px-4 font-mono text-xs text-gray-400 max-w-[160px]">
                <span className="truncate block" title={job.id}>
                  {job.id}
                </span>
              </td>
              <td className="py-3 px-4">
                <Link
                  href={`/dashboard/jobs/${job.id}`}
                  className="text-gray-200 hover:text-white transition-colors font-medium"
                >
                  {job.name || "—"}
                </Link>
              </td>
              <td className="py-3 px-4 text-xs text-gray-400 tabular-nums">
                {job.workers > 0 ? job.workers : "—"}
              </td>
              <td className="py-3 px-4">
                <StatusBadge status={job.status} />
              </td>
              <td className="py-3 px-4">
                <ProgressBar job={job} />
              </td>
              <td className="py-3 px-4 text-xs text-gray-400 whitespace-nowrap">
                {formatDate(job.created_at)}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
