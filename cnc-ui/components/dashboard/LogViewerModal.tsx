"use client";

import { useState, useEffect } from "react";
import type { Task } from "@/lib/types";
import { StatusBadge } from "@/components/ui/StatusBadge";
import { duration } from "@/lib/utils";

interface LogViewerModalProps {
  isOpen: boolean;
  onClose: () => void;
  task: Task | null;
}

export function LogViewerModal({
  isOpen,
  onClose,
  task,
}: LogViewerModalProps) {
  const [activeTab, setActiveTab] = useState<"all" | "stdout" | "stderr">("all");
  const [copied, setCopied] = useState(false);

  // Close on Escape key
  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent) {
      if (e.key === "Escape") {
        onClose();
      }
    }
    if (isOpen) {
      window.addEventListener("keydown", handleKeyDown);
    }
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [isOpen, onClose]);

  // Reset tab when task changes
  useEffect(() => {
    if (task) {
      const exitCode = task.result?.exit_code ?? 0;
      if (exitCode !== 0 && task.result?.stderr) {
        setActiveTab("stderr");
      } else {
        setActiveTab("all");
      }
    }
  }, [task]);

  if (!isOpen || !task) return null;

  const stdout = task.result?.stdout ?? "";
  const stderr = task.result?.stderr ?? "";
  const exitCode = task.result?.exit_code ?? null;

  let currentContent = "";
  if (activeTab === "all") {
    if (stdout && stderr) {
      currentContent = `--- STDOUT ---\n${stdout}\n\n--- STDERR ---\n${stderr}`;
    } else {
      currentContent = stdout || stderr || "(No output)";
    }
  } else if (activeTab === "stdout") {
    currentContent = stdout || "(Empty stdout)";
  } else if (activeTab === "stderr") {
    currentContent = stderr || "(Empty stderr)";
  }

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(currentContent);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // Fallback
    }
  };

  const isExit127 = exitCode === 127 || stderr.includes("command not found");

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 sm:p-6 bg-black/80 backdrop-blur-sm animate-in fade-in duration-200">
      {/* Click outside backdrop */}
      <div className="absolute inset-0" onClick={onClose} />

      {/* Modal Dialog */}
      <div className="relative w-full max-w-4xl max-h-[88vh] bg-gray-950 border border-gray-800/90 rounded-2xl shadow-2xl flex flex-col overflow-hidden ring-1 ring-white/10 z-10">
        {/* Header */}
        <div className="px-6 py-4 border-b border-gray-800/80 bg-gray-900/60 flex items-center justify-between gap-4 shrink-0">
          <div className="flex items-center gap-3 min-w-0">
            <div className="w-9 h-9 rounded-lg bg-blue-500/15 border border-blue-500/30 flex items-center justify-center text-blue-400 shrink-0">
              <svg
                className="w-5 h-5"
                fill="none"
                viewBox="0 0 24 24"
                stroke="currentColor"
                strokeWidth={2}
              >
                <path
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  d="M8 9l3 3-3 3m5 0h3M5 20h14a2 2 0 002-2V6a2 2 0 00-2-2H5a2 2 0 00-2 2v12a2 2 0 002 2z"
                />
              </svg>
            </div>
            <div className="min-w-0">
              <div className="flex items-center gap-2 flex-wrap">
                <h3 className="text-base font-semibold text-white tracking-tight">
                  Task Execution Log
                </h3>
                <span className="font-mono text-xs px-2 py-0.5 rounded-md bg-gray-800 text-gray-300 border border-gray-700/80">
                  {task.assigned_to || "unassigned"}
                </span>
                <StatusBadge status={task.status} />
                {exitCode !== null && (
                  <span
                    className={`font-mono text-xs px-2 py-0.5 rounded-md font-semibold border ${
                      exitCode === 0
                        ? "bg-emerald-500/10 text-emerald-400 border-emerald-500/20"
                        : exitCode === 127
                        ? "bg-amber-500/10 text-amber-400 border-amber-500/20"
                        : "bg-red-500/10 text-red-400 border-red-500/20"
                    }`}
                  >
                    exit {exitCode}
                    {exitCode === 127 && " (Command Not Found)"}
                  </span>
                )}
              </div>
              <p className="text-xs text-gray-400 mt-0.5 flex items-center gap-3">
                <span>ID: <code className="text-gray-300">{task.id}</code></span>
                <span>Duration: {duration(task.started_at, task.completed_at)}</span>
              </p>
            </div>
          </div>

          {/* Action buttons */}
          <div className="flex items-center gap-2 shrink-0">
            <button
              type="button"
              onClick={handleCopy}
              className="px-3 py-1.5 rounded-lg bg-gray-800/80 hover:bg-gray-700/80 border border-gray-700/70 text-xs font-medium text-gray-200 hover:text-white flex items-center gap-1.5 transition-colors cursor-pointer"
            >
              {copied ? (
                <>
                  <svg className="w-3.5 h-3.5 text-emerald-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2.5}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
                  </svg>
                  <span className="text-emerald-400">Copied!</span>
                </>
              ) : (
                <>
                  <svg className="w-3.5 h-3.5 text-gray-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z" />
                  </svg>
                  <span>Copy</span>
                </>
              )}
            </button>
            <button
              type="button"
              onClick={onClose}
              className="p-1.5 rounded-lg text-gray-400 hover:text-white hover:bg-gray-800 transition-colors cursor-pointer"
              title="Close (Esc)"
            >
              <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
              </svg>
            </button>
          </div>
        </div>

        {/* Diagnostic Banner if exit 127 */}
        {isExit127 && (
          <div className="px-6 py-3 bg-amber-500/10 border-b border-amber-500/20 text-xs text-amber-300 flex items-start gap-2.5">
            <svg className="w-4 h-4 text-amber-400 shrink-0 mt-0.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
            </svg>
            <div>
              <span className="font-semibold text-amber-200">Executable Not Found Diagnostic:</span>{" "}
              The target command was not found on this worker machine (exit code 127). Ensure the tool binary is built into{" "}
              <code className="bg-black/40 px-1 py-0.5 rounded text-amber-100 font-mono">./tools/</code> or installed in the worker's system PATH.
            </div>
          </div>
        )}

        {/* Tab Controls */}
        <div className="px-6 pt-3 border-b border-gray-800 bg-gray-950 flex items-center gap-2 text-xs">
          <button
            type="button"
            onClick={() => setActiveTab("all")}
            className={`pb-2.5 px-2 font-medium border-b-2 transition-colors cursor-pointer ${
              activeTab === "all"
                ? "text-blue-400 border-blue-500"
                : "text-gray-400 border-transparent hover:text-gray-200"
            }`}
          >
            All Output
          </button>
          <button
            type="button"
            onClick={() => setActiveTab("stdout")}
            className={`pb-2.5 px-2 font-medium border-b-2 transition-colors cursor-pointer flex items-center gap-1.5 ${
              activeTab === "stdout"
                ? "text-blue-400 border-blue-500"
                : "text-gray-400 border-transparent hover:text-gray-200"
            }`}
          >
            <span>Stdout</span>
            {stdout && (
              <span className="text-[10px] px-1.5 py-0.2 rounded-full bg-emerald-500/10 text-emerald-400 border border-emerald-500/20">
                {stdout.trim().split("\n").length} lines
              </span>
            )}
          </button>
          <button
            type="button"
            onClick={() => setActiveTab("stderr")}
            className={`pb-2.5 px-2 font-medium border-b-2 transition-colors cursor-pointer flex items-center gap-1.5 ${
              activeTab === "stderr"
                ? "text-blue-400 border-blue-500"
                : "text-gray-400 border-transparent hover:text-gray-200"
            }`}
          >
            <span>Stderr</span>
            {stderr && (
              <span className="text-[10px] px-1.5 py-0.2 rounded-full bg-red-500/10 text-red-400 border border-red-500/20">
                {stderr.trim().split("\n").length} lines
              </span>
            )}
          </button>
        </div>

        {/* Terminal Body */}
        <div className="p-6 overflow-y-auto flex-1 bg-black/80 font-mono text-xs leading-relaxed text-gray-300">
          <pre className="whitespace-pre-wrap break-all font-mono select-text">
            {currentContent}
          </pre>
        </div>

        {/* Footer */}
        <div className="px-6 py-3 border-t border-gray-800/80 bg-gray-900/40 flex items-center justify-between text-xs text-gray-500">
          <div>
            Press <kbd className="px-1.5 py-0.5 rounded bg-gray-800 text-gray-300 border border-gray-700 text-[10px]">Esc</kbd> to close
          </div>
          <div className="font-mono">
            {task.completed_at ? `Finished: ${new Date(task.completed_at).toLocaleTimeString()}` : "Running..."}
          </div>
        </div>
      </div>
    </div>
  );
}
