"use client";

import { useDashboard } from "@/components/providers/EventProvider";

interface JobProgressProps {
  jobId: string;
}

export function JobProgress({ jobId }: JobProgressProps) {
  const { jobs } = useDashboard();
  const job = jobs[jobId];
  if (!job) return null;

  const total = job.total_tasks;
  if (total === 0) {
    return (
      <p className="text-xs text-gray-500">
        Waiting for input file to be split into chunks…
      </p>
    );
  }

  const running = Math.max(0, total - job.completed - job.failed);
  const completedPct = Math.round((job.completed / total) * 100);
  const failedPct = Math.round((job.failed / total) * 100);
  const runningPct = Math.round((running / total) * 100);

  return (
    <div className="space-y-3">
      {/* Segmented bar */}
      <div
        className="w-full bg-gray-700 rounded-full h-3 overflow-hidden flex"
        role="progressbar"
        aria-valuenow={completedPct}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-label="Job progress"
      >
        <div
          className="bg-emerald-500 h-full transition-all duration-500"
          style={{ width: `${completedPct}%` }}
        />
        <div
          className="bg-blue-500 h-full transition-all duration-500"
          style={{ width: `${runningPct}%` }}
        />
        <div
          className="bg-red-500 h-full transition-all duration-500"
          style={{ width: `${failedPct}%` }}
        />
      </div>

      {/* Legend */}
      <div className="flex flex-wrap gap-4 text-xs">
        <span className="flex items-center gap-1.5 text-emerald-400">
          <span className="h-2 w-2 rounded-full bg-emerald-500" />
          {job.completed} completed
        </span>
        <span className="flex items-center gap-1.5 text-blue-400">
          <span className="h-2 w-2 rounded-full bg-blue-500" />
          {running} running
        </span>
        {job.failed > 0 && (
          <span className="flex items-center gap-1.5 text-red-400">
            <span className="h-2 w-2 rounded-full bg-red-500" />
            {job.failed} failed
          </span>
        )}
        <span className="flex items-center gap-1.5 text-gray-500">
          {total} total tasks
        </span>
      </div>
    </div>
  );
}
