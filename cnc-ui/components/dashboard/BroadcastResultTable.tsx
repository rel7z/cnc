"use client";

import { useState } from "react";
import type { Task } from "@/lib/types";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { duration, useTick } from "@/lib/utils";
import { LogViewerModal } from "./LogViewerModal";

interface BroadcastResultTableProps {
  tasks: Task[];
}

const INITIAL_VISIBLE = 5;

function formatWorker(raw?: string | null): { display: string; tag: string; raw: string } {
  if (!raw) return { display: "unassigned", tag: "", raw: "" };
  if (raw === "server") return { display: "server", tag: "local host", raw: "server" };
  let name = raw.replace(/^worker_/, "");
  let tag = "";
  const lastIdx = name.lastIndexOf("_");
  if (lastIdx !== -1 && /^\d+$/.test(name.slice(lastIdx + 1))) {
    tag = "#" + name.slice(lastIdx + 1);
    name = name.slice(0, lastIdx);
  }
  name = name.replace(/\.local$/, "");
  return { display: name, tag, raw };
}

export function BroadcastResultTable({ tasks }: BroadcastResultTableProps) {
  useTick(1000);
  const [showAll, setShowAll] = useState(false);
  const [expandedTaskId, setExpandedTaskId] = useState<string | null>(null);
  const [modalTask, setModalTask] = useState<Task | null>(null);
  const [copiedTaskId, setCopiedTaskId] = useState<string | null>(null);

  const visibleTasks = showAll ? tasks : tasks.slice(0, INITIAL_VISIBLE);
  const hiddenCount = tasks.length - INITIAL_VISIBLE;

  const toggleExpand = (taskId: string) => {
    setExpandedTaskId((prev) => (prev === taskId ? null : taskId));
  };

  const handleCopyOutput = async (taskId: string, text: string) => {
    try {
      await navigator.clipboard.writeText(text);
      setCopiedTaskId(taskId);
      setTimeout(() => setCopiedTaskId(null), 2000);
    } catch {
      // Fallback
    }
  };

  function getShortOutput(task: Task): string {
    if (!task.result) return "—";
    const exitCode = task.result.exit_code ?? 0;
    const text =
      exitCode !== 0
        ? task.result.stderr ?? task.result.stdout ?? ""
        : task.result.stdout ?? "";
    const trimmed = text.trim();
    if (!trimmed) return "—";
    return trimmed.length > 70 ? trimmed.slice(0, 70) + "…" : trimmed;
  }

  return (
    <div className="space-y-0">
      <div className="overflow-x-auto">
        <table className="w-full text-sm text-left">
          <thead className="text-xs text-gray-400 uppercase tracking-wider border-b border-gray-800 bg-gray-950/40">
            <tr>
              <th className="py-2.5 px-4 font-medium w-8"></th>
              <th className="py-2.5 px-4 font-medium">Worker</th>
              <th className="py-2.5 px-4 font-medium">Status</th>
              <th className="py-2.5 px-4 font-medium">Exit Code</th>
              <th className="py-2.5 px-4 font-medium">Output</th>
              <th className="py-2.5 px-4 font-medium">Duration</th>
              <th className="py-2.5 px-4 font-medium text-right">Actions</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-800/80">
            {visibleTasks.map((task) => {
              const exitCode = task.result?.exit_code ?? null;
              const isRunning =
                task.status === "running" ||
                task.status === "assigned" ||
                task.status === "pending";
              const isExpanded = expandedTaskId === task.id;
              const workerInfo = formatWorker(task.assigned_to);
              const stdout = task.result?.stdout ?? "";
              const stderr = task.result?.stderr ?? "";
              const rawOutput = stdout || stderr || "";
              const isCommandNotFound = exitCode === 127 || stderr.includes("command not found");

              return (
                <tbody key={task.id} className="group">
                  <tr
                    onClick={() => toggleExpand(task.id)}
                    className={`transition-colors cursor-pointer select-none ${
                      isExpanded
                        ? "bg-gray-800/60"
                        : "hover:bg-gray-800/30"
                    }`}
                  >
                    {/* Expand Chevron */}
                    <td className="py-3 px-3 text-gray-500 w-8">
                      <div
                        className={`w-5 h-5 rounded flex items-center justify-center transition-transform duration-200 ${
                          isExpanded ? "rotate-90 text-blue-400" : "group-hover:text-gray-300"
                        }`}
                      >
                        <svg
                          className="w-3.5 h-3.5"
                          fill="none"
                          viewBox="0 0 24 24"
                          stroke="currentColor"
                          strokeWidth={2.5}
                        >
                          <path
                            strokeLinecap="round"
                            strokeLinejoin="round"
                            d="M9 5l7 7-7 7"
                          />
                        </svg>
                      </div>
                    </td>

                    {/* Formatted Worker Name */}
                    <td className="py-3 px-4 max-w-[200px]">
                      <div
                        className="flex items-center gap-1.5 truncate font-mono text-xs text-gray-200"
                        title={workerInfo.raw}
                      >
                        <span className="truncate">{workerInfo.display}</span>
                        {workerInfo.tag && (
                          <span className="px-1.5 py-0.5 rounded text-[10px] bg-gray-800 text-blue-400 border border-gray-700 font-mono shrink-0">
                            {workerInfo.tag}
                          </span>
                        )}
                      </div>
                    </td>

                    {/* Status Badge */}
                    <td className="py-3 px-4 whitespace-nowrap">
                      <StatusBadge status={task.status} />
                    </td>

                    {/* Exit Code with Diagnostics Badge */}
                    <td className="py-3 px-4 text-xs whitespace-nowrap">
                      {exitCode === null ? (
                        <span className="text-gray-500">—</span>
                      ) : exitCode === 0 ? (
                        <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-md text-[11px] font-mono font-medium bg-emerald-500/10 text-emerald-400 border border-emerald-500/20">
                          <span className="w-1.5 h-1.5 rounded-full bg-emerald-400" />
                          0 (OK)
                        </span>
                      ) : isCommandNotFound ? (
                        <span
                          className="inline-flex items-center gap-1 px-2 py-0.5 rounded-md text-[11px] font-mono font-medium bg-amber-500/15 text-amber-400 border border-amber-500/30"
                          title="Tool binary not found in worker PATH"
                        >
                          <svg className="w-3 h-3 text-amber-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2.5}>
                            <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
                          </svg>
                          127 (Not Found)
                        </span>
                      ) : (
                        <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-md text-[11px] font-mono font-medium bg-red-500/15 text-red-400 border border-red-500/30">
                          {exitCode} (Failed)
                        </span>
                      )}
                    </td>

                    {/* Output Preview */}
                    <td className="py-3 px-4 font-mono text-xs max-w-[280px]">
                      {isRunning ? (
                        <span className="text-gray-600">—</span>
                      ) : (
                        <span
                          className={`truncate block ${
                            exitCode !== null && exitCode !== 0
                              ? "text-red-400"
                              : "text-gray-300"
                          }`}
                          title={rawOutput}
                        >
                          {getShortOutput(task)}
                        </span>
                      )}
                    </td>

                    {/* Duration */}
                    <td className="py-3 px-4 text-xs text-gray-400 whitespace-nowrap tabular-nums">
                      {duration(task.started_at, task.completed_at)}
                    </td>

                    {/* Action Buttons */}
                    <td className="py-3 px-4 text-right whitespace-nowrap">
                      <div className="flex items-center justify-end gap-2" onClick={(e) => e.stopPropagation()}>
                        <button
                          type="button"
                          onClick={() => setModalTask(task)}
                          className="px-2 py-1 rounded bg-gray-800/80 hover:bg-gray-700 text-xs font-medium text-gray-300 hover:text-white border border-gray-700/60 transition-colors flex items-center gap-1 cursor-pointer"
                          title="Open full execution log modal"
                        >
                          <svg className="w-3.5 h-3.5 text-blue-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                            <path strokeLinecap="round" strokeLinejoin="round" d="M10 6H6a2 2 0 00-2 2v10a2 2 0 002 2h10a2 2 0 002-2v-4M14 4h6m0 0v6m0-6L10 14" />
                          </svg>
                          <span>Log</span>
                        </button>
                      </div>
                    </td>
                  </tr>

                  {/* Expandable Inline Terminal Details */}
                  {isExpanded && (
                    <tr className="bg-gray-950/90 border-b border-gray-800">
                      <td colSpan={7} className="p-4 sm:p-5">
                        <div className="rounded-xl border border-gray-800 bg-black/90 p-4 font-mono text-xs shadow-inner space-y-3">
                          {/* Diagnostic Alert if 127 */}
                          {isCommandNotFound && (
                            <div className="p-3 rounded-lg bg-amber-500/10 border border-amber-500/20 text-amber-300 flex items-start gap-2.5">
                              <svg className="w-4 h-4 text-amber-400 shrink-0 mt-0.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                                <path strokeLinecap="round" strokeLinejoin="round" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                              </svg>
                              <div>
                                <strong className="font-semibold text-amber-200">Diagnostic Advice:</strong> The tool executable was not found on this worker machine.
                                <p className="mt-1 text-amber-400/90 text-[11px]">
                                  Ensure the binary is built into <code className="bg-black/60 px-1 py-0.5 rounded text-amber-200">./tools/</code> on the worker host or installed in the worker's system PATH.
                                </p>
                              </div>
                            </div>
                          )}

                          {/* Terminal Header Bar */}
                          <div className="flex items-center justify-between border-b border-gray-800/80 pb-2.5 text-gray-400 text-[11px]">
                            <div className="flex items-center gap-2">
                              <span className="h-2 w-2 rounded-full bg-emerald-400 inline-block" />
                              <span className="font-semibold text-gray-300">Terminal Output</span>
                              <span className="text-gray-600">|</span>
                              <span>Task <code className="text-gray-400">{task.id}</code></span>
                            </div>

                            <div className="flex items-center gap-2">
                              <button
                                type="button"
                                onClick={() => handleCopyOutput(task.id, rawOutput)}
                                className="px-2 py-0.5 rounded hover:bg-gray-800 text-[11px] text-gray-400 hover:text-white transition-colors flex items-center gap-1 cursor-pointer"
                              >
                                {copiedTaskId === task.id ? (
                                  <>
                                    <svg className="w-3 h-3 text-emerald-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2.5}>
                                      <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
                                    </svg>
                                    <span className="text-emerald-400">Copied!</span>
                                  </>
                                ) : (
                                  <>
                                    <svg className="w-3 h-3 text-gray-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                                      <path strokeLinecap="round" strokeLinejoin="round" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z" />
                                    </svg>
                                    <span>Copy</span>
                                  </>
                                )}
                              </button>
                              <button
                                type="button"
                                onClick={() => setModalTask(task)}
                                className="px-2 py-0.5 rounded bg-blue-600/20 hover:bg-blue-600/30 text-[11px] text-blue-300 border border-blue-500/30 transition-colors flex items-center gap-1 cursor-pointer"
                              >
                                <span>Full Modal</span>
                                <svg className="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                                  <path strokeLinecap="round" strokeLinejoin="round" d="M14 5l7 7m0 0l-7 7m7-7H3" />
                                </svg>
                              </button>
                            </div>
                          </div>

                          {/* Terminal Lines Content */}
                          <div className="max-h-60 overflow-y-auto font-mono text-xs leading-relaxed text-gray-300 select-text">
                            {stdout && (
                              <div className="text-emerald-400/90 whitespace-pre-wrap break-all">
                                {stdout}
                              </div>
                            )}
                            {stderr && (
                              <div className={`whitespace-pre-wrap break-all ${stdout ? "mt-2 pt-2 border-t border-gray-900" : ""} text-red-400`}>
                                {stderr}
                              </div>
                            )}
                            {!stdout && !stderr && (
                              <div className="text-gray-600 italic">
                                (No output recorded for this task)
                              </div>
                            )}
                          </div>
                        </div>
                      </td>
                    </tr>
                  )}
                </tbody>
              );
            })}
          </tbody>
        </table>
      </div>

      {tasks.length > INITIAL_VISIBLE && (
        <div className="px-4 py-3 border-t border-gray-800 flex items-center justify-between text-xs">
          <button
            type="button"
            onClick={() => setShowAll((v) => !v)}
            className="text-blue-400 hover:text-blue-300 transition-colors cursor-pointer font-medium"
          >
            {showAll ? "Show less" : `Show all ${tasks.length} workers (${hiddenCount} more)`}
          </button>
          <span className="text-gray-500 font-mono text-[11px]">
            {tasks.filter((t) => t.status === "completed").length} completed / {tasks.filter((t) => t.status === "failed").length} failed
          </span>
        </div>
      )}

      {/* Full Modal Viewer */}
      <LogViewerModal
        isOpen={modalTask !== null}
        onClose={() => setModalTask(null)}
        task={modalTask}
      />
    </div>
  );
}
