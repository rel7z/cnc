"use client";

import { useState, useRef, useEffect } from "react";
import { X, RefreshCw, Terminal, CheckCircle2, AlertTriangle, ArrowRight } from "lucide-react";

interface UpdateServerModalProps {
  isOpen: boolean;
  onClose: () => void;
}

export function UpdateServerModal({ isOpen, onClose }: UpdateServerModalProps) {
  const [isUpdating, setIsUpdating] = useState(false);
  const [logs, setLogs] = useState("");
  const [status, setStatus] = useState<"idle" | "running" | "restarting" | "success" | "error">("idle");
  const [errorMsg, setErrorMsg] = useState("");
  const logEndRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (logEndRef.current) {
      logEndRef.current.scrollIntoView({ behavior: "smooth" });
    }
  }, [logs]);

  if (!isOpen) return null;

  const handleStartUpdate = async () => {
    setIsUpdating(true);
    setStatus("running");
    setLogs("[INFO] Triggering server update & recompile...\n");
    setErrorMsg("");

    try {
      const res = await fetch("/api/server/update", {
        method: "POST",
      });

      if (!res.ok) {
        const text = await res.text();
        throw new Error(text || `Server returned HTTP ${res.status}`);
      }

      const reader = res.body?.getReader();
      if (!reader) throw new Error("Stream reader not supported");

      const decoder = new TextDecoder();
      let completeLogs = "";

      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        const chunk = decoder.decode(value, { stream: true });
        completeLogs += chunk;
        setLogs(prev => prev + chunk);
      }

      if (completeLogs.includes("[ERROR]")) {
        setStatus("error");
        setIsUpdating(false);
      } else {
        setStatus("restarting");
        setLogs(prev => prev + "\n[INFO] Polling server health until restart completes...\n");
        pollServerHealth();
      }
    } catch (err: any) {
      setStatus("error");
      setErrorMsg(err.message || "Failed to update server");
      setLogs(prev => prev + `\n[FATAL ERROR] ${err.message}\n`);
      setIsUpdating(false);
    }
  };

  const pollServerHealth = () => {
    let attempts = 0;
    const maxAttempts = 30; // 60 seconds max

    const interval = setInterval(async () => {
      attempts++;
      try {
        const res = await fetch("/api/status", { cache: "no-store" });
        if (res.ok) {
          clearInterval(interval);
          setStatus("success");
          setIsUpdating(false);
          setLogs(prev => prev + "[SUCCESS] CNC Server is back online and healthy!\n");
        }
      } catch {
        // Expected while server restarts
        if (attempts >= maxAttempts) {
          clearInterval(interval);
          setStatus("error");
          setErrorMsg("Server took too long to restart. Check server status manually.");
          setIsUpdating(false);
        }
      }
    }, 2000);
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/70 backdrop-blur-sm animate-in fade-in duration-200">
      <div className="bg-gray-900 border border-gray-800 rounded-xl shadow-2xl w-full max-w-2xl overflow-hidden flex flex-col max-h-[90vh]">
        {/* Header */}
        <div className="flex items-center justify-between p-4 border-b border-gray-800 bg-gray-950/40">
          <div className="flex items-center gap-2.5">
            <RefreshCw className={`w-5 h-5 text-blue-400 ${isUpdating ? "animate-spin" : ""}`} />
            <div>
              <h2 className="text-base font-semibold text-white">Update & Recompile Server</h2>
              <p className="text-xs text-gray-400">Pulls git main, recompiles Go backend, builds UI & restarts services</p>
            </div>
          </div>
          <button
            onClick={onClose}
            disabled={isUpdating}
            className="p-1.5 text-gray-400 hover:text-white hover:bg-gray-800 rounded-md transition-colors disabled:opacity-30 disabled:cursor-not-allowed"
          >
            <X className="w-5 h-5" />
          </button>
        </div>

        {/* Body */}
        <div className="p-5 overflow-y-auto space-y-4">
          {status === "idle" && (
            <div className="space-y-3 bg-gray-950/50 p-4 rounded-lg border border-gray-800 text-sm text-gray-300">
              <p className="font-medium text-white">This automated process will:</p>
              <ul className="list-disc list-inside space-y-1 text-xs text-gray-400">
                <li>Execute <span className="font-mono text-gray-200">git pull origin main</span> to fetch the latest code</li>
                <li>Recompile Go binaries (<span className="font-mono text-gray-200">make build</span> and <span className="font-mono text-gray-200">make build-linux</span>)</li>
                <li>Update worker binary and tools for auto-deployments</li>
                <li>Rebuild the Next.js frontend (<span className="font-mono text-gray-200">npm run build</span>)</li>
                <li>Gracefully restart <span className="font-mono text-gray-200">cnc-server</span> and <span className="font-mono text-gray-200">cnc-ui</span> systemd services</li>
              </ul>
            </div>
          )}

          {status === "restarting" && (
            <div className="p-3 bg-blue-500/10 border border-blue-500/20 rounded-md flex items-center gap-3 text-blue-400 text-sm">
              <RefreshCw className="w-5 h-5 animate-spin shrink-0" />
              <div>
                <p className="font-medium">Services are restarting with updated binaries...</p>
                <p className="text-xs text-blue-300/80">Reconnecting to server health endpoint (/api/status)...</p>
              </div>
            </div>
          )}

          {status === "success" && (
            <div className="p-3 bg-green-500/10 border border-green-500/20 rounded-md flex items-center gap-3 text-green-400 text-sm">
              <CheckCircle2 className="w-5 h-5 shrink-0" />
              <div>
                <p className="font-medium">Update completed successfully!</p>
                <p className="text-xs text-green-300/80">The CNC server and dashboard are now running the latest code.</p>
              </div>
            </div>
          )}

          {status === "error" && (
            <div className="p-3 bg-red-500/10 border border-red-500/20 rounded-md flex items-center gap-3 text-red-400 text-sm">
              <AlertTriangle className="w-5 h-5 shrink-0" />
              <div>
                <p className="font-medium">Update failed</p>
                <p className="text-xs text-red-300/80">{errorMsg || "Review logs below for details."}</p>
              </div>
            </div>
          )}

          {logs && (
            <div>
              <div className="flex items-center gap-2 text-xs font-semibold text-gray-400 mb-1.5 uppercase tracking-wider">
                <Terminal className="w-3.5 h-3.5" />
                Live Build & Compilation Output
              </div>
              <div className="bg-black/95 border border-gray-800 rounded-lg p-3 h-64 overflow-y-auto font-mono text-xs text-gray-300 whitespace-pre-wrap break-all shadow-inner">
                {logs}
                {isUpdating && <span className="animate-pulse text-blue-400">▌</span>}
                <div ref={logEndRef} />
              </div>
            </div>
          )}
        </div>

        {/* Footer */}
        <div className="p-4 border-t border-gray-800 bg-gray-950/40 flex justify-end gap-2.5">
          {status === "idle" && (
            <>
              <button
                type="button"
                onClick={onClose}
                className="px-4 py-2 text-sm text-gray-400 hover:text-white transition-colors"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={handleStartUpdate}
                className="flex items-center gap-2 px-4 py-2 text-sm font-medium text-white bg-blue-600 hover:bg-blue-500 rounded-md transition-colors shadow-lg shadow-blue-500/20"
              >
                <RefreshCw className="w-4 h-4" />
                Start Update & Recompile
              </button>
            </>
          )}

          {isUpdating && (
            <button
              type="button"
              disabled
              className="flex items-center gap-2 px-4 py-2 text-sm font-medium text-gray-400 bg-gray-800 rounded-md cursor-not-allowed"
            >
              <RefreshCw className="w-4 h-4 animate-spin" />
              Updating Server...
            </button>
          )}

          {status === "success" && (
            <button
              type="button"
              onClick={() => window.location.reload()}
              className="flex items-center gap-2 px-4 py-2 text-sm font-medium text-white bg-green-600 hover:bg-green-500 rounded-md transition-colors shadow-lg shadow-green-500/20"
            >
              <ArrowRight className="w-4 h-4" />
              Reload Page
            </button>
          )}

          {status === "error" && (
            <>
              <button
                type="button"
                onClick={onClose}
                className="px-4 py-2 text-sm text-gray-400 hover:text-white transition-colors"
              >
                Close
              </button>
              <button
                type="button"
                onClick={handleStartUpdate}
                className="flex items-center gap-2 px-4 py-2 text-sm font-medium text-white bg-blue-600 hover:bg-blue-500 rounded-md transition-colors"
              >
                <RefreshCw className="w-4 h-4" />
                Retry Update
              </button>
            </>
          )}
        </div>
      </div>
    </div>
  );
}
