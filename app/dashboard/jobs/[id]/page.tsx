import Link from "next/link";
import { notFound } from "next/navigation";
import { fetchJob } from "@/lib/api";
import { Header } from "@/components/dashboard/Header";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { TaskTable } from "@/components/dashboard/TaskTable";
import { JobProgress } from "@/components/dashboard/JobProgress";

function formatDate(isoString: string | undefined): string {
  if (!isoString) return "—";
  return new Date(isoString).toLocaleString([], {
    month: "short",
    day: "numeric",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function basename(p: string): string {
  return p.split("/").filter(Boolean).pop() ?? p;
}

interface JobDetailPageProps {
  params: Promise<{ id: string }>;
}

export default async function JobDetailPage({ params }: JobDetailPageProps) {
  const { id } = await params;
  let job;
  try {
    job = await fetchJob(id);
  } catch {
    notFound();
  }

  const destFilename = job.input_file ? basename(job.input_file) : null;

  return (
    <>
      <Header title="Job Detail" />
      <div className="p-6 space-y-6">
        {/* Breadcrumb */}
        <nav className="flex items-center gap-1.5 text-xs text-gray-500" aria-label="Breadcrumb">
          <Link href="/dashboard" className="hover:text-gray-300 transition-colors">Dashboard</Link>
          <span>/</span>
          <Link href="/dashboard/jobs" className="hover:text-gray-300 transition-colors">Jobs</Link>
          <span>/</span>
          <span className="text-gray-300">{job.name || job.id}</span>
        </nav>

        {/* Job header */}
        <div className="bg-gray-900 border border-gray-800 rounded-xl p-5 space-y-4">
          <div className="flex items-start justify-between gap-4">
            <div>
              <h2 className="text-base font-semibold text-white">{job.name || "Unnamed Job"}</h2>
              <p className="text-xs font-mono text-gray-500 mt-0.5">{job.id}</p>
            </div>
            <StatusBadge status={job.status} />
          </div>

          {/* Metadata grid */}
          <div className="grid grid-cols-1 md:grid-cols-2 gap-x-8 gap-y-3 text-sm">
            <div className="space-y-3">
              <div>
                <span className="text-xs text-gray-500 block mb-1">Command</span>
                <code className="block bg-gray-800 text-gray-200 text-xs rounded px-3 py-2 font-mono break-all">
                  {job.command}
                </code>
              </div>
              <div>
                <span className="text-xs text-gray-500 block mb-1">Source file (server)</span>
                <code className="block bg-gray-800 text-gray-400 text-xs rounded px-3 py-2 font-mono break-all">
                  {job.input_file || "—"}
                </code>
              </div>
              <div>
                <span className="text-xs text-gray-500 block mb-1">Workers receive</span>
                <code className="block bg-gray-800 text-emerald-400 text-xs rounded px-3 py-2 font-mono">
                  {destFilename ? `~/${destFilename}` : "—"}
                </code>
              </div>
            </div>
            <div className="space-y-3">
              <div className="flex justify-between">
                <span className="text-xs text-gray-500">Parts</span>
                <span className="text-xs text-gray-300">{job.workers > 0 ? job.workers : "auto"}</span>
              </div>
              <div className="flex justify-between">
                <span className="text-xs text-gray-500">Timeout</span>
                <span className="text-xs text-gray-300">
                  {job.timeout_seconds === -1 ? "none" : `${job.timeout_seconds}s`}
                </span>
              </div>
              <div className="flex justify-between">
                <span className="text-xs text-gray-500">Created</span>
                <span className="text-xs text-gray-300">{formatDate(job.created_at)}</span>
              </div>
              {job.started_at && (
                <div className="flex justify-between">
                  <span className="text-xs text-gray-500">Started</span>
                  <span className="text-xs text-gray-300">{formatDate(job.started_at)}</span>
                </div>
              )}
              {job.completed_at && (
                <div className="flex justify-between">
                  <span className="text-xs text-gray-500">Completed</span>
                  <span className="text-xs text-gray-300">{formatDate(job.completed_at)}</span>
                </div>
              )}
            </div>
          </div>

          {/* Live progress */}
          <div>
            <span className="text-xs text-gray-500 block mb-2">Progress</span>
            <JobProgress jobId={job.id} />
          </div>
        </div>

        {/* Distribution table */}
        <div className="bg-gray-900 border border-gray-800 rounded-xl overflow-hidden">
          <div className="px-5 py-3 border-b border-gray-800">
            <h3 className="text-sm font-semibold text-white">Distribution</h3>
            <p className="text-xs text-gray-500 mt-0.5">
              Each part is downloaded to{" "}
              <code className="text-emerald-400">~/{destFilename ?? "…"}</code>{" "}
              on the assigned worker, then the command runs.
            </p>
          </div>
          <TaskTable jobId={job.id} />
        </div>
      </div>
    </>
  );
}
