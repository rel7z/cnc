"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { ProcessorEditor, ProcessorResults } from "./ProcessorEditor";
import { WorkerSSH } from "./WorkerSSH";
import { ToolPriorities } from "./ToolPriorities";

type Health = { enabled: boolean; counts: Record<string, number>; configured_programs?: number; configured_routes?: number; blocked_reason?: string; watch_dir?: string };
type Worker = { address?: string; metadata?: Record<string,string>; id: string; status: string; current_load: number; max_tasks: number; resources?: { cpu_percent: number; available_memory_mb: number } };
type Cluster = { workers: Worker[]; error?: string };
type Run = { ID: string; ProgramID: string; Status: string; WorkerID?: string; AttemptID?: string; Phase?: string; BlockedReason?: string; Error?: string; PublishedLines?: number; PendingLines?: number; OutputFilePath?: string };
type Section = "overview" | "programs" | "results";
const secondary = "rounded-lg border border-gray-700 px-3 py-2 text-xs font-medium text-gray-300 hover:bg-gray-800 disabled:opacity-40";
const primary = "rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-500 disabled:opacity-40";
function programName(id: string) { return id.replaceAll("--", " / ").replaceAll("_", " "); }
function statusName(run: Run) {
  if (run.Phase === "uploading" && run.Status === "running") return "Saving results";
  return ({ queued: "Waiting", running: "Processing", completed: "Completed", failed: "Failed", interrupted: "Stopped" } as Record<string,string>)[run.Status] || run.Status;
}
function statusTone(status: string) {
  if (["completed", "online"].includes(status)) return "bg-emerald-500/10 text-emerald-300";
  if (["failed", "interrupted"].includes(status)) return "bg-amber-500/10 text-amber-300";
  if (status === "running") return "bg-blue-500/10 text-blue-300";
  return "bg-gray-800 text-gray-400";
}
async function readAPI(path: string) {
  const response = await fetch(`/api/processor/${path}`, { cache: "no-store" });
  if (!response.ok) throw new Error(path === "health" ? "Cannot connect to the processor. Check the service and try again." : `Could not refresh ${path}.`);
  return response.json();
}

export function ProcessorSettings({ compact = false }: { compact?: boolean }) {
  const [section, setSection] = useState<Section>("overview");
  const [visited, setVisited] = useState<Section[]>(["overview"]);
  const [health, setHealth] = useState<Health | null>(null);
  const [cluster, setCluster] = useState<Cluster | null>(null);
  const [runs, setRuns] = useState<Run[]>([]);
  const [loading, setLoading] = useState(true);
  const [connected, setConnected] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const [filter, setFilter] = useState("all");
  const [retry, setRetry] = useState<Run | null>(null);
  const [cancel, setCancel] = useState<Run | null>(null);
  const [resetPending, setResetPending] = useState(false);
  const [advanced, setAdvanced] = useState(false);
  const refreshing = useRef(false);
  function navigate(next: Section) {
    setSection(next);
    setVisited(items => items.includes(next) ? items : [...items, next]);
  }
  const refresh = useCallback(async () => {
    if (refreshing.current) return;
    refreshing.current = true;
    try {
      const [state, workers, history] = await Promise.allSettled([
        readAPI("health"), compact ? Promise.resolve(null) : readAPI("cluster"), compact ? Promise.resolve(null) : readAPI("runs"),
      ]);
      setConnected(state.status === "fulfilled");
      if (state.status === "fulfilled") setHealth(state.value);
      if (workers.status === "fulfilled" && workers.value) setCluster({ ...workers.value, workers: workers.value.workers ?? [] });
      if (history.status === "fulfilled" && history.value) setRuns(history.value);
      const failed = [state, workers, history].find(result => result.status === "rejected");
      setError(failed?.status === "rejected" ? failed.reason.message : "");
    } finally { refreshing.current = false; setLoading(false); }
  }, [compact]);
  useEffect(() => {
    void refresh();
    const timer = setInterval(() => void refresh(), 5000);
    return () => clearInterval(timer);
  }, [refresh]);
  async function mutate(path: string, method: string, body?: unknown) {
    const response = await fetch(`/api/processor/${path}`, { method, headers: { "Content-Type": "application/json" }, ...(body === undefined ? {} : { body: JSON.stringify(body) }) });
    const value = await response.json().catch(() => null);
    if (!response.ok) throw new Error(value?.error || "The change could not be confirmed. Refresh and try again.");
    return value;
  }
  async function action(work: () => Promise<void>) {
    setBusy(true); setError(""); setNotice("");
    try { await work(); await refresh(); }
    catch (e) { setError(e instanceof Error ? e.message : "The request failed. Try again."); }
    finally { setBusy(false); }
  }
  const counts = health?.counts ?? {};
  const attention = (counts.failed ?? 0) + (counts.interrupted ?? 0);
  const online = cluster?.workers.filter(w => w.status === "online" || w.status === "busy").length ?? 0;
  const canChange = connected && !!health && !busy;
  const resetDisabled = !canChange || !!health?.enabled || (counts.running ?? 0) > 0 || (counts.queued ?? 0) > 0;
  const filtered = runs.filter(run => filter === "all" || filter === "attention" && ["failed", "interrupted"].includes(run.Status) || run.Status === filter);
  const stateLabel = !connected ? loading ? "Connecting…" : "Offline" : health?.blocked_reason ? "Setup required" : health?.enabled ? "Processing enabled" : "Paused";
  return <section className="space-y-5">
    <div className="flex flex-wrap items-center justify-between gap-4 rounded-2xl border border-gray-800 bg-gray-900 p-5">
      <div>
        <p className="flex items-center gap-2 text-sm font-medium text-white"><span className={`h-2 w-2 rounded-full ${connected && health?.enabled && !health.blocked_reason ? "bg-emerald-400" : "bg-amber-400"}`} />{stateLabel}</p>
        <p className="mt-1.5 max-w-xl text-sm text-gray-400">{!connected ? "Connect to the processor to see current activity." : health?.blocked_reason ? "Add a program and choose its input file to get started." : health?.enabled ? "New input is picked up automatically. Results are saved as work finishes." : "New work is paused. Programs already running can finish and save their results."}</p>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        {compact && <Link className={secondary} href="/dashboard/processor">Open processor</Link>}
        {!compact && <button type="button" className={secondary} disabled={loading || busy} onClick={() => void refresh()}>Refresh</button>}
        <button type="button" className={primary} disabled={!canChange || !!health?.blocked_reason && !health.enabled} onClick={() => void action(async () => {
          await mutate("processing-state", "PUT", { enabled: !health!.enabled });
          setNotice(health!.enabled ? "Processing paused. Running programs can finish." : "Processing started. Watching for input files.");
        })}>{busy ? "Updating…" : health?.enabled ? "Pause processing" : "Start processing"}</button>
      </div>
    </div>
    {error && <div role="alert" className="rounded-xl border border-amber-500/20 bg-amber-500/10 p-4 text-sm text-amber-200">{error}{health && <span className="block pt-1 text-xs">Showing the last available status.</span>}</div>}
    {notice && <p role="status" className="rounded-xl bg-emerald-500/10 px-4 py-3 text-sm text-emerald-300">{notice}</p>}
    {loading && <p role="status" className="text-sm text-gray-400">Loading processor status…</p>}
    {health && (compact || section === "overview") && <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
      {[["Waiting", counts.queued ?? 0, "Ready when workers are available"], ["Processing", counts.running ?? 0, compact ? "Workers assigned automatically" : `${online} workers connected`], ["Completed", counts.completed ?? 0, "Finished processing"], ["Needs attention", attention, "Failed or stopped runs"]].map(([label, count, description]) => <div key={label} className="rounded-xl border border-gray-800 bg-gray-900/50 p-4"><p className="text-xs text-gray-400">{label}</p><p className="mt-2 text-2xl font-semibold tracking-tight text-white">{count}</p><p className="mt-1 text-xs text-gray-500">{description}</p></div>)}
    </div>}
    {!compact && <>
      <nav aria-label="Processor sections" className="flex gap-1 border-b border-gray-800">
        {([['overview','Overview'],['programs','Programs'],['results','Results']] as const).map(([id,label]) => <button key={id} type="button" aria-pressed={section === id} aria-controls={`processor-${id}`} onClick={() => navigate(id)} className={`border-b-2 px-5 py-3 text-sm font-medium ${section === id ? "border-blue-400 text-blue-300" : "border-transparent text-gray-400 hover:text-white"}`}>{label}</button>)}
      </nav>
      {visited.includes("programs") && <div id="processor-programs" hidden={section !== "programs"}><ProcessorEditor /></div>}
      {visited.includes("results") && <div id="processor-results" hidden={section !== "results"}><ProcessorResults /></div>}
      <div id="processor-overview" hidden={section !== "overview"} className="space-y-5">
        {health?.blocked_reason && <div className="rounded-xl border border-blue-500/20 bg-blue-500/5 p-5"><h3 className="font-medium text-white">Set up your first program</h3><p className="mt-1 text-sm text-gray-400">Choose an input file, add the program that reads it, and select the output to save.</p><button type="button" className={primary + " mt-4"} onClick={() => navigate("programs")}>Set up a program</button></div>}
        {!health?.blocked_reason && health && <div className="flex flex-wrap items-center gap-x-4 gap-y-2 rounded-xl border border-gray-800 px-4 py-3 text-xs text-gray-400"><span className="text-gray-200">How it works</span><span>1. Input file arrives</span><span aria-hidden="true">→</span><span>2. Workers process it</span><span aria-hidden="true">→</span><span>3. Results are saved</span></div>}
        <div className="rounded-xl border border-gray-800 bg-gray-900/50">
          <div className="flex flex-wrap items-center justify-between gap-3 p-4"><div><h3 className="text-sm font-medium text-white">Recent activity</h3><p className="mt-1 text-xs text-gray-500">The latest 100 runs. Open a run for logs and technical details.</p></div><select aria-label="Filter processor runs" value={filter} onChange={e => setFilter(e.target.value)} className="rounded-lg border border-gray-700 bg-gray-950 p-2 text-xs text-gray-300">{[["all","All runs"],["running","Processing"],["queued","Waiting"],["attention","Needs attention"],["completed","Completed"]].map(([value,label]) => <option key={value} value={value}>{label}</option>)}</select></div>
          {filtered.length === 0 && <div className="border-t border-gray-800 px-5 py-12 text-center"><p className="text-sm text-gray-300">{loading ? "Loading activity…" : runs.length ? "No runs match this filter." : "No activity yet"}</p><p className="mt-2 text-xs text-gray-500">{runs.length ? "Choose another filter to see more runs." : "Add a program, start processing, and matching input files will appear here."}</p></div>}
          <div className="divide-y divide-gray-800">{filtered.map(run => <details key={run.ID} className="group px-4 py-3">
            <summary className="flex cursor-pointer list-none flex-wrap items-center justify-between gap-3 rounded-lg focus-visible:outline-2 focus-visible:outline-blue-400">
              <div className="min-w-0 flex-1"><p className="break-words text-sm font-medium text-gray-200">{programName(run.ProgramID)}</p><p className="mt-1 text-xs text-gray-500">{run.BlockedReason || run.Error || (run.Status === "queued" ? "Waiting for an available worker" : `${(run.PublishedLines ?? 0).toLocaleString()} result lines saved${run.PendingLines ? ` · ${run.PendingLines} waiting to sync` : ""}`)}</p></div>
              <span className={`rounded-full px-2.5 py-1 text-xs ${statusTone(run.Status)}`}>{statusName(run)}</span><span className="text-xs text-gray-500 group-open:hidden">Details ↓</span><span className="hidden text-xs text-gray-500 group-open:inline">Close ↑</span>
            </summary>
            <div className="mt-4 space-y-3 rounded-lg bg-gray-950/70 p-4 text-xs text-gray-400">
              <dl className="grid gap-3 sm:grid-cols-2"><div><dt className="text-gray-500">Run</dt><dd className="mt-1 break-all font-mono">{run.ID}</dd></div><div><dt className="text-gray-500">Worker</dt><dd className="mt-1">{run.WorkerID || "Not assigned yet"}</dd></div></dl>
              <p>{run.PublishedLines ?? 0} result lines saved · {run.PendingLines ?? 0} waiting to sync</p>
              {run.OutputFilePath && <p className="break-all">Files: {run.OutputFilePath.split("\n").map(path => path.split("/").pop()).join(", ")}</p>}
              <div className="flex flex-wrap gap-3"><a href={`/api/processor/runs/${encodeURIComponent(run.ID)}/log`} target="_blank" rel="noreferrer" className="text-blue-300">Open log ↗</a><button type="button" className="text-blue-300" onClick={() => navigate("results")}>View results</button>{["queued","running"].includes(run.Status) && <button type="button" disabled={!canChange} onClick={() => { setRetry(null); setCancel(run); }} className="text-amber-300 disabled:opacity-40">Cancel run</button>}{["failed","interrupted"].includes(run.Status) && <button type="button" disabled={!canChange} onClick={() => { setCancel(null); setRetry(run); }} className="text-blue-300 disabled:opacity-40">Retry run</button>}</div>
        {(retry || cancel)?.ID === run.ID && <div role="alert" className="space-y-3 rounded-xl border border-amber-500/30 bg-amber-500/5 p-4 text-sm text-amber-200"><p>{retry ? `Retry ${programName(retry.ProgramID)}? Previous operations may be repeated. Existing results are kept.` : `Cancel ${programName(cancel!.ProgramID)}? A running program will stop when its worker checks in. Results already saved are kept.`}</p><div className="flex gap-3"><button type="button" disabled={!canChange} className={secondary} onClick={() => void action(async () => { const run = retry || cancel!; await mutate(`runs/${encodeURIComponent(run.ID)}/${retry ? "retry" : "cancel"}`, "POST"); setNotice(retry ? "Run queued for another attempt." : "Cancellation requested. Saved results are kept."); setRetry(null); setCancel(null); })}>{retry ? "Confirm retry" : "Confirm cancellation"}</button><button type="button" disabled={busy} onClick={() => { setRetry(null); setCancel(null); }}>Keep as is</button></div></div>}
            </div>
          </details>)}</div>
        </div>
        <details className="rounded-xl border border-gray-800 p-4" onToggle={e => { if (e.currentTarget.open) setAdvanced(true); }}><summary className="cursor-pointer text-sm text-gray-400">Advanced settings <span className="ml-2 text-xs text-gray-600">Workers, priorities, and reprocessing</span></summary>{advanced && <div className="mt-5 space-y-5">
          <div><h3 className="text-sm font-medium text-white">Workers</h3><p className="mt-1 text-xs text-gray-500">Work is assigned automatically. Unstarted shares move to another worker after a one-minute outage.</p>{cluster?.error && <p role="alert" className="mt-2 text-xs text-amber-300">{cluster.error}</p>}<div className="mt-3 grid gap-3 md:grid-cols-2">{cluster?.workers.map(worker => <div key={worker.id} className="flex flex-wrap items-center justify-between gap-3 rounded-lg bg-gray-900 p-3 text-xs"><div><p className="text-gray-200">{worker.id} · {worker.status}</p><p className="mt-1 text-gray-500">{worker.current_load}/{worker.max_tasks} slots{worker.resources ? ` · CPU ${worker.resources.cpu_percent.toFixed(0)}% · ${(worker.resources.available_memory_mb/1024).toFixed(1)} GB free` : ""}</p></div><WorkerSSH worker={worker} /></div>)}</div>{!cluster?.workers.length && <p className="mt-3 text-xs text-gray-500">No workers connected.</p>}</div>
          <ToolPriorities />
          <div className="space-y-3 border-t border-gray-800 pt-4"><h3 className="text-sm font-medium text-white">Read input again</h3><p className="text-xs text-gray-400">Reset checkpoints to read existing input from the beginning. Pause processing and finish or cancel waiting and running jobs first. This can repeat previous operations.</p><button type="button" className={secondary} disabled={resetDisabled} onClick={() => setResetPending(true)}>Reset checkpoints</button>{resetPending && <div role="alert" className="space-y-3 text-sm text-amber-200"><p>After restarting processing, existing input will be read again. History and results are kept, and a checkpoint backup is saved.</p><div className="flex gap-3"><button type="button" disabled={resetDisabled} className={secondary} onClick={() => void action(async () => { const result = await mutate("checkpoints/reset", "POST", { confirm: "reset-checkpoints" }); setResetPending(false); setNotice(`Reset ${result.checkpoints_reset} checkpoints. Processing remains paused.`); })}>Confirm checkpoint reset</button><button type="button" disabled={busy} onClick={() => setResetPending(false)}>Cancel</button></div></div>}</div>
        </div>}</details>
      </div>
    </>}
  </section>;
}
