"use client";

import { useState, useEffect, useCallback } from "react";

interface TelegramStatusData {
  enabled: boolean;
  authorized: boolean;
  bot_username?: string;
  bot_name?: string;
  chat_id?: string;
  webapp_url?: string;
}

export function TelegramStatus() {
  const [status, setStatus] = useState<TelegramStatusData | null>(null);
  const [loading, setLoading] = useState(true);
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<{ success: boolean; message: string } | null>(null);

  const fetchStatus = useCallback(async () => {
    try {
      const res = await fetch("/api/config/telegram");
      if (res.ok) {
        const data = await res.json();
        if (data.status) {
          setStatus(data.status);
        }
      }
    } catch {
      // Backend may be offline, ignore
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchStatus();
    const interval = setInterval(fetchStatus, 5000);
    return () => clearInterval(interval);
  }, [fetchStatus]);

  async function handleSendTest() {
    setTesting(true);
    setTestResult(null);
    try {
      const res = await fetch("/api/config/telegram/test", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({}),
      });
      const data = await res.json();
      if (data.success) {
        setTestResult({ success: true, message: data.message || "Test alert sent!" });
      } else {
        setTestResult({ success: false, message: data.error || "Failed to send test alert" });
      }
    } catch (err) {
      setTestResult({ success: false, message: err instanceof Error ? err.message : "Network error" });
    } finally {
      setTesting(false);
    }
  }

  useEffect(() => {
    let timeoutId: NodeJS.Timeout;
    if (testResult) {
      timeoutId = setTimeout(() => setTestResult(null), 4000);
    }
    return () => clearTimeout(timeoutId);
  }, [testResult]);

  if (loading || !status?.enabled) {
    return null;
  }

  const isConnected = status.authorized;
  const botLink = status.bot_username ? `https://t.me/${status.bot_username}` : "https://t.me";

  return (
    <div className="bg-gray-900 border border-gray-800 rounded-xl overflow-hidden shadow-sm">
      <div className="px-5 py-3 border-b border-gray-800 flex items-center justify-between">
        <div className="flex items-center gap-2">
          <svg className="h-4 w-4 text-sky-400" viewBox="0 0 24 24" fill="currentColor">
            <path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm4.64 6.8c-.15 1.58-.8 5.42-1.13 7.19-.14.75-.42 1-.68 1.03-.58.05-1.02-.38-1.58-.75-.88-.58-1.38-.94-2.23-1.5-.99-.65-.35-1.01.22-1.59.15-.15 2.71-2.48 2.76-2.69a.2.2 0 00-.05-.18c-.06-.05-.14-.03-.21-.02-.09.02-1.49.95-4.22 2.79-.4.27-.76.41-1.08.4-.36-.01-1.04-.2-1.55-.37-.63-.2-1.12-.31-1.08-.66.02-.18.27-.36.74-.55 2.92-1.27 4.86-2.11 5.83-2.51 2.78-1.16 3.35-1.36 3.73-1.36.08 0 .27.02.39.12.1.08.13.19.14.27-.01.06.01.24 0 .38z" />
          </svg>
          <h2 className="text-sm font-semibold text-white">Telegram Integration</h2>
          <span className="flex items-center gap-1.5 ml-2">
            <span className="relative flex h-2 w-2">
              <span
                className={`animate-ping absolute inline-flex h-full w-full rounded-full opacity-75 ${
                  isConnected ? "bg-sky-400" : "bg-amber-400"
                }`}
              />
              <span
                className={`relative inline-flex rounded-full h-2 w-2 ${
                  isConnected ? "bg-sky-400" : "bg-amber-400"
                }`}
              />
            </span>
            <span className={`text-xs font-medium ${isConnected ? "text-sky-400" : "text-amber-400"}`}>
              {isConnected ? "Bot Connected" : "Connecting..."}
            </span>
          </span>
        </div>

        <div className="flex items-center gap-3">
          {testResult && (
            <span
              className={`text-xs px-2.5 py-1 rounded-md ${
                testResult.success ? "bg-emerald-950/80 text-emerald-400 border border-emerald-800" : "bg-red-950/80 text-red-400 border border-red-800"
              }`}
            >
              {testResult.message}
            </span>
          )}

          <button
            type="button"
            onClick={handleSendTest}
            disabled={testing || !isConnected}
            className="text-xs bg-gray-800 hover:bg-gray-700 text-gray-200 px-3 py-1.5 rounded-lg border border-gray-700 transition flex items-center gap-1.5 disabled:opacity-50"
          >
            {testing ? (
              <>
                <svg className="animate-spin h-3.5 w-3.5 text-sky-400" fill="none" viewBox="0 0 24 24">
                  <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                  <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v8H4z" />
                </svg>
                <span>Sending...</span>
              </>
            ) : (
              <>
                <span>⚡ Test Alert</span>
              </>
            )}
          </button>

          <a
            href={botLink}
            target="_blank"
            rel="noopener noreferrer"
            className="text-xs bg-sky-600/20 hover:bg-sky-600/30 text-sky-300 border border-sky-500/30 px-3 py-1.5 rounded-lg transition flex items-center gap-1.5 font-medium"
          >
            <span>Open @{status.bot_username || "bot"}</span>
            <svg className="w-3 h-3" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M10 6H6a2 2 0 00-2 2v10a2 2 0 002 2h10a2 2 0 002-2v-4M14 4h6m0 0v6m0-6L10 14" />
            </svg>
          </a>
        </div>
      </div>

      <div className="p-5">
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          <div className="bg-gray-800/50 rounded-lg p-3 border border-gray-700/50">
            <div className="text-xs text-gray-400 mb-1">Bot Name & Username</div>
            <div className="text-sm font-semibold text-white flex items-center gap-1.5">
              <span>{status.bot_name || "CNC Bot"}</span>
              {status.bot_username && (
                <span className="text-xs text-sky-400 font-mono">(@{status.bot_username})</span>
              )}
            </div>
            <div className="text-xs text-gray-500 mt-1">Interactive cluster management bot</div>
          </div>

          <div className="bg-gray-800/50 rounded-lg p-3 border border-gray-700/50">
            <div className="text-xs text-gray-400 mb-1">Subscribed Alert Chat</div>
            <div className="text-sm font-mono font-medium text-emerald-400">
              {status.chat_id ? status.chat_id : "Auto-detecting (Send /start)"}
            </div>
            <div className="text-xs text-gray-500 mt-1">
              {status.chat_id ? "Receiving real-time cluster notifications" : "Type /start in Telegram to bind"}
            </div>
          </div>

          <div className="bg-gray-800/50 rounded-lg p-3 border border-gray-700/50">
            <div className="text-xs text-gray-400 mb-1">Telegram WebApp UI</div>
            <div className="text-xs font-mono text-gray-300 truncate" title={status.webapp_url || "http://localhost:3000"}>
              {status.webapp_url || "http://localhost:3000"}
            </div>
            <div className="text-xs text-gray-500 mt-1">Direct WebApp integration inside Telegram</div>
          </div>
        </div>
      </div>
    </div>
  );
}
