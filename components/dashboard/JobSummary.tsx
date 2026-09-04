"use client";

import Link from "next/link";
import { useDashboard } from "@/components/providers/EventProvider";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { EmptyState } from "@/components/ui/EmptyState";
import type { Job } from "@/lib/types";

function ProgressBar({ job }: { job: Job }) {
  const total = job.total_tasks;
  if (total === 0) return <span className="text-xs text-gray-500">—</span>;
  const completedPct = Math.round((job.completed / total) * 100);
  const failedPct = Math.round((job.failed / total) * 100);
  return (
    <div className="flex items-center gap-2">
      <div className="w-24 bg-gray-700 rounded-full h-1.5 overflow-hidden flex">
        <div
          className="bg-emerald-500 h-full transition-all duration-300"
          style={{ width: `${completedPct}%` }}
        />
        <div
          className="bg-red-500 h-full transition-all duration-300"
          style={{ width: `${failedPct}%` }}
        />
      </div>
      <span className="text-xs text-gray-400 tabular-nums">
        {job.completed}/{total}
      </span>
    </div>
  );
}

export function JobSummary() {
  const { jobs } = useDashboard();
  const jobList = Object.values(jobs)
    .sort(
      (a, b) =>
        new Date(b.created_at).getTime() - new Date(a.created_at).getTime()
    )
    .slice(0, 5);

  return (
    <div className="bg-gray-900 border border-gray-800 rounded-xl">
      <div className="flex items-center justify-between px-5 py-3 border-b border-gray-800">
        <h2 className="text-sm font-semibold text-white">Recent Jobs</h2>
        <Link
          href="/dashboard/jobs"
          className="text-xs text-blue-400 hover:text-blue-300 transition-colors"
        >
          View all →
        </Link>
      </div>

      {jobList.length === 0 ? (
        <EmptyState
          title="No jobs yet"
          description="Submit a job via the CLI to get started"
        />
      ) : (
        <table className="w-full text-sm">
          <thead>
            <tr className="text-xs text-gray-400 uppercase tracking-wider border-b border-gray-800">
              <th className="text-left py-2 px-5 font-medium">Name</th>
              <th className="text-left py-2 px-4 font-medium">Status</th>
              <th className="text-left py-2 px-4 font-medium">Progress</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-800">
            {jobList.map((job) => (
              <tr
                key={job.id}
                className="hover:bg-gray-800/50 transition-colors"
              >
                <td className="py-2.5 px-5">
                  <Link
                    href={`/dashboard/jobs/${job.id}`}
                    className="text-gray-200 hover:text-white transition-colors text-sm"
                  >
                    {job.name || job.id}
                  </Link>
                </td>
                <td className="py-2.5 px-4">
                  <StatusBadge status={job.status} />
                </td>
                <td className="py-2.5 px-4">
                  <ProgressBar job={job} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
