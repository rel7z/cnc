"use client";

import React, { useState, useEffect, useRef } from "react";
import { X, Play, Loader2 } from "lucide-react";
import { useDashboard } from "@/components/providers/EventProvider";

interface DeployWorkerModalProps {
  onClose: () => void;
}

export function DeployWorkerModal({ onClose }: DeployWorkerModalProps) {
  const { workers } = useDashboard();
  const [ips, setIps] = useState("");
  const [username, setUsername] = useState("root");
  const [password, setPassword] = useState("");
  const [isDeploying, setIsDeploying] = useState(false);
  const [isDone, setIsDone] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [logs, setLogs] = useState<string>("");
  const logEndRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    logEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [logs]);

  useEffect(() => {
    if (Object.keys(workers).length > 0) {
      const uniqueIps = Array.from(
        new Set(
          Object.values(workers)
            .map((w) => w.address.split(":")[0])
            .filter((ip) => ip !== "127.0.0.1" && ip !== "localhost" && ip.length > 0)
        )
      );
      if (uniqueIps.length > 0) {
        setIps(uniqueIps.join("\n"));
      }
    }
  }, []);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const ipList = ips.split("\n").map(ip => ip.trim()).filter(ip => ip.length > 0);
    
    if (ipList.length === 0) {
      setError("Please enter at least one IP address");
      return;
    }
    
    setIsDeploying(true);
    setError(null);

    try {
      const res = await fetch("/api/workers/deploy", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ ips: ipList, username, password })
      });

      if (!res.ok) {
        throw new Error("Failed to trigger deployment");
      }
      
      const reader = res.body?.getReader();
      if (!reader) throw new Error("Stream not supported");
      
      const decoder = new TextDecoder();
      
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        const text = decoder.decode(value, { stream: true });
        setLogs(prev => prev + text);
      }
      
      setIsDeploying(false);
      setIsDone(true);
    } catch (err: any) {
      setError(err.message || "An error occurred");
      setIsDeploying(false);
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/60 backdrop-blur-sm animate-in fade-in duration-200">
      <div className="bg-gray-900 border border-gray-800 rounded-xl shadow-2xl w-full max-w-lg overflow-hidden flex flex-col max-h-[90vh]">
        <div className="flex items-center justify-between p-4 border-b border-gray-800">
          <h2 className="text-lg font-semibold text-white">Deploy Workers</h2>
          <button
            onClick={onClose}
            className="p-1.5 text-gray-400 hover:text-white hover:bg-gray-800 rounded-md transition-colors"
          >
            <X className="w-5 h-5" />
          </button>
        </div>

        <div className="p-6 overflow-y-auto">
          <form id="deploy-form" onSubmit={handleSubmit} className="space-y-4">
            
            {error && (
              <div className="p-3 bg-red-500/10 border border-red-500/20 rounded-md text-red-400 text-sm">
                {error}
              </div>
            )}

            <div>
              <label className="block text-sm font-medium text-gray-300 mb-1.5">
                Target IP Addresses (one per line)
              </label>
              <textarea
                value={ips}
                onChange={(e) => setIps(e.target.value)}
                rows={5}
                className="w-full bg-gray-950 border border-gray-800 rounded-md p-2.5 text-sm text-gray-200 font-mono placeholder:text-gray-600 focus:outline-none focus:border-blue-500 focus:ring-1 focus:ring-blue-500"
                placeholder="192.168.1.10&#10;192.168.1.11"
                required
              />
            </div>

            <div className="grid grid-cols-2 gap-4">
              <div>
                <label className="block text-sm font-medium text-gray-300 mb-1.5">
                  SSH Username
                </label>
                <input
                  type="text"
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                  className="w-full bg-gray-950 border border-gray-800 rounded-md px-3 py-2 text-sm text-gray-200 focus:outline-none focus:border-blue-500 focus:ring-1 focus:ring-blue-500"
                  required
                />
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-300 mb-1.5">
                  SSH Password
                </label>
                <input
                  type="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  className="w-full bg-gray-950 border border-gray-800 rounded-md px-3 py-2 text-sm text-gray-200 focus:outline-none focus:border-blue-500 focus:ring-1 focus:ring-blue-500"
                  required
                />
              </div>
            </div>
            
            <p className="text-xs text-gray-500 leading-relaxed mt-2">
              The CNC server will SSH into these IPs, pull the latest worker code, and start the systemd service.
            </p>
          </form>

          {(logs || isDeploying || isDone) && (
            <div className="mt-6">
              <h3 className="text-sm font-medium text-gray-300 mb-2">Deployment Logs</h3>
              <div className="bg-black border border-gray-800 rounded-md p-3 h-48 overflow-y-auto font-mono text-xs text-gray-300 whitespace-pre-wrap break-all shadow-inner">
                {logs}
                {isDeploying && <span className="animate-pulse">_</span>}
                <div ref={logEndRef} />
              </div>
            </div>
          )}
        </div>

        <div className="p-4 border-t border-gray-800 bg-gray-900/50 flex justify-end gap-3">
          {isDone ? (
            <button
              onClick={onClose}
              className="px-4 py-2 bg-blue-600 hover:bg-blue-500 text-white text-sm font-medium rounded-md transition-colors"
            >
              Done
            </button>
          ) : (
            <>
              <button
                type="button"
                onClick={onClose}
                disabled={isDeploying}
                className="px-4 py-2 text-sm font-medium text-gray-300 hover:text-white transition-colors disabled:opacity-50"
              >
                Cancel
              </button>
              <button
                type="submit"
                form="deploy-form"
                disabled={isDeploying}
                className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-500 text-white text-sm font-medium rounded-md transition-colors disabled:opacity-50"
              >
                {isDeploying ? (
                  <>
                    <Loader2 className="w-4 h-4 animate-spin" />
                    Deploying...
                  </>
                ) : (
                  <>
                    <Play className="w-4 h-4" />
                    Deploy Workers
                  </>
                )}
              </button>
            </>
          )}
        </div>
      </div>
    </div>
  );
}
