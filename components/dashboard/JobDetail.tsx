"use client";

import { useDashboard } from "@/components/providers/EventProvider";
import { StatusBadge } from "@/components/ui/StatusBadge";
import type { Job, Task } from "@/lib/types";

// ── Helpers ───────────────────────────────────────────────────────────────────

function formatDate(iso: string | undefined): string {
  if (!iso) return "—";
  return new Date(iso).toLocaleString([], {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

function duration(start?: string, end?: string): string {
  if (!start) return "—";
  const ms = (end ? new Date(end) : new Date()).getTime() - new Date(start).getTime();
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${s % 60}s`;
  return `${Math.floor(m / 60)}h ${m % 60}m`;
}

function basename(p: string): string {
  return p.split("/").filter(Boolean).pop() ?? p;
}

// ── Progress bar ──────────────────────────────────────────────────────────────

function JobProgressBar({ job }: { job: Job }) {
  const total = job.total_tasks;
  if (total === 0) return <p className="text-xs text-gray-500">No tasks yet — splitting file…</p>;

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
        <div className="bg-emerald-500 h-full transition-all duration-500" style={{ width: `${completedPct}%` }} />
        <div className="bg-red-500 h-full transition-all duration-500" style={{ width: `${failedPct}%` }} />
        <div className="bg-gray-600 h-full transition-all duration-500" style={{ width: `${remaining}%` }} />
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

// ── Task table ────────────────────────────────────────────────────────────────

function TaskTable({ tasks }: { tasks: Task[] }) {
  if (tasks.length === 0) {
    return (
      <p className="text-sm text-gray-500 text-center py-8">
        No tasks dispatched yet.
      </p>
    );
  }

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-sm text-left">
        <thead className="text-xs text-gray-400 uppercase tracking-wider border-b border-gray-800">
          <tr>
            <th className="py-2.5 px-4 font-medium">Part</th>
            <th className="py-2.5 px-4 font-medium">Worker receives</th>
            <th className="py-2.5 px-4 font-medium">Worker</th>
            <th className="py-2.5 px-4 font-medium">Status</th>
            <th className="py-2.5 px-4 font-medium">Duration</th>
            <th className="py-2.5 px-4 font-medium">Retries</th>
            <th className="py-2.5 px-4 font-medium">Error</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-gray-800">
          {tasks.map((task) => {
            const partNum = task.id.split("_").slice(-1)[0] ?? "?";
            const destName = task.payload?.dest_name;
            return (
              <tr key={task.id} className="hover:bg-gray-800/40 transition-colors">
                <td className="py-2.5 px-4 font-mono text-xs text-gray-400 tabular-nums">
                  #{partNum}
                </td>
                <td className="py-2.5 px-4 font-mono text-xs text-emerald-400">
                  {destName ? `~/${destName}` : "—"}
                </td>
                <td className="py-2.5 px-4 font-mono text-xs text-gray-400 max-w-[160px]">
                  <span className="truncate block" title={task.assigned_to}>
                    {task.assigned_to ?? "—"}
                  </span>
                </td>
                <td className="py-2.5 px-4">
                  <StatusBadge status={task.status} />
                </td>
                <td className="py-2.5 px-4 text-xs text-gray-400 whitespace-nowrap">
                  {duration(task.started_at, task.completed_at)}
                </td>
                <td className="py-2.5 px-4 text-xs text-gray-400 tabular-nums">
                  {task.retry_count > 0 ? (
                    <span className="text-yellow-400">{task.retry_count}</span>
                  ) : "0"}
                </td>
                <td className="py-2.5 px-4 text-xs text-gray-400">
                  {task.error ? (
                    <span className="text-red-400 truncate block max-w-[200px]" title={task.error}>
                      {task.error}
                    </span>
                  ) : task.result?.message ? (
                    <span className="text-emerald-400">{task.result.message}</span>
                  ) : "—"}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

// ── Main component ────────────────────────────────────────────────────────────

interface JobDetailProps {
  jobId: string;
}

export function JobDetail({ jobId }: JobDetailProps) {
  const { jobs, tasks } = useDashboard();
  const job = jobs[jobId];

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

  const jobTasks = Object.values(tasks)
    .filter((t) => t.job_id === jobId)
    .sort((a, b) => new Date(a.created_at).getTime() - new Date(b.created_at).getTime());

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
          <StatusBadge status={job.status} className="shrink-0" />
        </div>
        <JobProgressBar job={job} />
      </div>

      {/* Details */}
      <div className="bg-gray-900 border border-gray-800 rounded-xl px-5 py-1">
        <dl>
          <InfoRow label="Command">{job.command || "—"}</InfoRow>
          <InfoRow label="Source file">{job.input_file || "—"}</InfoRow>
          <InfoRow label="Workers receive">
            {destFilename ? (
              <span className="text-emerald-400">~/{destFilename}</span>
            ) : "—"}
          </InfoRow>
          <InfoRow label="Parts">{job.workers > 0 ? String(job.workers) : "auto"}</InfoRow>
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

      {/* Task list */}
      <div className="bg-gray-900 border border-gray-800 rounded-xl overflow-hidden">
        <div className="px-5 py-3 border-b border-gray-800 flex items-center justify-between">
          <h3 className="text-sm font-semibold text-white">Distribution</h3>
          <span className="text-xs text-gray-500">
            {jobTasks.length} of {job.total_tasks} parts
          </span>
        </div>
        <TaskTable tasks={jobTasks} />
      </div>
    </div>
  );
}
