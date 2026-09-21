"use client";

import { useState } from "react";

import { useDashboard } from "@/components/providers/EventProvider";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { TaskTable } from "@/components/dashboard/TaskTable";
import { BroadcastResultTable } from "@/components/dashboard/BroadcastResultTable";
import type { Job, Task } from "@/lib/types";
import { formatDate, duration, basename } from "@/lib/utils";

// ── Progress bar ──────────────────────────────────────────────────────────────

function JobProgressBar({ job }: { job: Job }) {
  const total = job.total_tasks;
  if (total === 0)
    return (
      <p className="text-xs text-gray-500">
        {job.mode === "server"
          ? "Executing on server host…"
          : job.mode === "broadcast"
          ? "Waiting for workers…"
          : "No tasks yet — splitting file…"}
      </p>
    );

  const completedPct = Math.round((job.completed / total) * 100);
  const failedPct = Math.round((job.failed / total) * 100);
  const remaining = 100 - completedPct - failedPct;

  return (
    <div className="space-y-2">
      <div
        className="h-2.5 rounded-full bg-gray-700 overflow-hidden flex"
        role="progressbar"
        aria-valuenow={completedPct}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-label={`${completedPct}% complete`}
      >
        <div
          className="bg-emerald-500 h-full transition-all duration-500"
          style={{ width: `${completedPct}%` }}
        />
        <div
          className="bg-red-500 h-full transition-all duration-500"
          style={{ width: `${failedPct}%` }}
        />
        <div
          className="bg-gray-600 h-full transition-all duration-500"
          style={{ width: `${remaining}%` }}
        />
      </div>
      <div className="flex items-center gap-4 text-xs text-gray-400">
        <span className="flex items-center gap-1.5">
          <span className="h-2 w-2 rounded-full bg-emerald-500 inline-block" />
          {job.completed} completed
        </span>
        <span className="flex items-center gap-1.5">
          <span className="h-2 w-2 rounded-full bg-red-500 inline-block" />
          {job.failed} failed
        </span>
        <span className="flex items-center gap-1.5">
          <span className="h-2 w-2 rounded-full bg-gray-600 inline-block" />
          {total - job.completed - job.failed} remaining
        </span>
        <span className="ml-auto font-medium text-white">{completedPct}% complete</span>
      </div>
    </div>
  );
}

// ── Info row ──────────────────────────────────────────────────────────────────

function InfoRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-start gap-3 py-2.5 border-b border-gray-800 last:border-0">
      <dt className="w-36 shrink-0 text-xs text-gray-500 pt-px">{label}</dt>
      <dd className="text-sm text-gray-200 font-mono break-all">{children}</dd>
    </div>
  );
}

// ── Main component ────────────────────────────────────────────────────────────

interface JobDetailProps {
  jobId: string;
}

export function JobDetail({ jobId }: JobDetailProps) {
  const { jobs, tasks } = useDashboard();
  const [isCancelling, setIsCancelling] = useState(false);
  const job = jobs[jobId];

  const handleCancel = async () => {
    if (!confirm("Are you sure you want to forcefully cancel this job? All running tasks will be killed.")) return;
    setIsCancelling(true);
    try {
      const res = await fetch(`/api/jobs/${jobId}/cancel`, { method: "POST" });
      if (!res.ok) throw new Error("Failed to cancel job");
    } catch (err) {
      console.error(err);
      alert("Error cancelling job");
    } finally {
      setIsCancelling(false);
    }
  };

  if (!job) {
    return (
      <div className="p-6">
        <div className="bg-gray-900 border border-gray-800 rounded-xl p-8 text-center">
          <p className="text-gray-400 text-sm">
            Job <span className="font-mono text-gray-300">{jobId}</span> not found.
          </p>
          <p className="text-gray-600 text-xs mt-1">
            The live connection will update this page automatically.
          </p>
        </div>
      </div>
    );
  }

  const jobTasks: Task[] = Object.values(tasks)
    .filter((t) => t.job_id === jobId)
    .sort((a, b) => new Date(a.created_at).getTime() - new Date(b.created_at).getTime());

  const isSpread = job.mode === "spread";
  const destFilename = job.input_file ? basename(job.input_file) : null;

  return (
    <div className="p-6 space-y-6">
      {/* Header card */}
      <div className="bg-gray-900 border border-gray-800 rounded-xl p-5 space-y-4">
        <div className="flex items-start justify-between gap-4">
          <div>
            <h2 className="text-base font-semibold text-white">{job.name || job.id}</h2>
            <p className="font-mono text-xs text-gray-500 mt-0.5">{job.id}</p>
          </div>
          <div className="flex items-center gap-3">
            <StatusBadge status={job.status} className="shrink-0" />
            {(job.status === "running" || job.status === "pending") && (
              <button
                onClick={handleCancel}
                disabled={isCancelling}
                className="px-3 py-1 bg-red-500/10 text-red-400 hover:bg-red-500/20 border border-red-500/20 rounded-md text-xs font-medium transition-colors disabled:opacity-50"
              >
                {isCancelling ? "Cancelling..." : "Cancel Job"}
              </button>
            )}
          </div>
        </div>
        <JobProgressBar job={job} />
      </div>

      {/* Details */}
      <div className="bg-gray-900 border border-gray-800 rounded-xl px-5 py-1">
        <dl>
          <InfoRow label="Mode">
            <span
              className={
                job.mode === "spread"
                  ? "text-blue-400"
                  : job.mode === "server"
                  ? "text-amber-400 font-semibold"
                  : "text-purple-400"
              }
            >
              {job.mode}
            </span>
          </InfoRow>
          <InfoRow label="Command">{job.command || "—"}</InfoRow>
          {isSpread && (
            <InfoRow label="Source file">{job.input_file || "—"}</InfoRow>
          )}
          {isSpread && destFilename && (
            <InfoRow label="Workers receive">
              <span className="text-emerald-400">~/{destFilename}</span>
            </InfoRow>
          )}
          {job.mode === "server" ? (
            <InfoRow label="Target">
              <span className="text-purple-300">Server host (local execution)</span>
            </InfoRow>
          ) : isSpread ? (
            <InfoRow label="Parts">{job.workers > 0 ? String(job.workers) : "auto"}</InfoRow>
          ) : (
            <InfoRow label="Workers">{job.workers > 0 ? String(job.workers) : "—"}</InfoRow>
          )}
          <InfoRow label="Timeout">
            {job.timeout_seconds === -1 ? (
              <span className="text-gray-400">none</span>
            ) : `${job.timeout_seconds}s`}
          </InfoRow>
          <InfoRow label="Created">{formatDate(job.created_at)}</InfoRow>
          <InfoRow label="Started">{formatDate(job.started_at)}</InfoRow>
          <InfoRow label="Completed">{formatDate(job.completed_at)}</InfoRow>
          <InfoRow label="Duration">{duration(job.started_at, job.completed_at)}</InfoRow>
        </dl>
      </div>

      {/* Task list — branched by mode */}
      <div className="bg-gray-900 border border-gray-800 rounded-xl overflow-hidden">
        <div className="px-5 py-3 border-b border-gray-800 flex items-center justify-between">
          <h3 className="text-sm font-semibold text-white">
            {isSpread ? "Distribution" : "Results"}
          </h3>
          <span className="text-xs text-gray-500">
            {jobTasks.length} of {job.total_tasks}{" "}
            {isSpread ? "parts" : "workers"}
          </span>
        </div>
        {isSpread ? (
          <TaskTable jobId={jobId} />
        ) : (
          <BroadcastResultTable tasks={jobTasks} />
        )}
      </div>
    </div>
  );
}
