"use client";

import { useState, useEffect, useCallback } from "react";

interface TelegramConfig {
  enabled: boolean;
  bot_token: string;
  chat_id: string;
  webapp_url: string;
  notify_job_start: boolean;
  notify_job_complete: boolean;
  notify_job_fail: boolean;
  notify_worker_events: boolean;
}

interface TelegramBotInfo {
  enabled: boolean;
  authorized: boolean;
  bot_username?: string;
  bot_name?: string;
  chat_id?: string;
}

const inputClass =
  "w-full bg-gray-800 border border-gray-700 rounded-lg px-3 py-2 text-sm text-gray-100 " +
  "placeholder:text-gray-600 focus:outline-none focus:ring-2 focus:ring-sky-500/50 focus:border-sky-500 " +
  "transition-colors font-mono text-xs";

export function TelegramSettings() {
  const [isOpen, setIsOpen] = useState(false);
  const [config, setConfig] = useState<TelegramConfig>({
    enabled: true,
    bot_token: "",
    chat_id: "",
    webapp_url: "http://localhost:3000",
    notify_job_start: true,
    notify_job_complete: true,
    notify_job_fail: true,
    notify_worker_events: false,
  });

  const [botInfo, setBotInfo] = useState<TelegramBotInfo | null>(null);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [message, setMessage] = useState<{ type: "success" | "error" | "warning"; text: string } | null>(null);

  const loadConfig = useCallback(async () => {
    setLoading(true);
    try {
      const res = await fetch("/api/config/telegram");
      if (!res.ok) throw new Error("Failed to load Telegram configuration");
      const data = await res.json();
      if (data.telegram) setConfig(data.telegram);
      if (data.status) setBotInfo(data.status);
    } catch (err) {
      setMessage({ type: "error", text: err instanceof Error ? err.message : "Failed to load config" });
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (isOpen) {
      loadConfig();
    }
  }, [isOpen, loadConfig]);

  async function handleSave() {
    setSaving(true);
    setMessage(null);
    try {
      const res = await fetch("/api/config/telegram", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(config),
      });

      if (!res.ok) {
        const errorText = await res.text();
        throw new Error(errorText || "Failed to save configuration");
      }

      const data = await res.json();
      setMessage({ type: "success", text: data.message || "Settings saved successfully!" });
      loadConfig();
    } catch (err) {
      setMessage({ type: "error", text: err instanceof Error ? err.message : "Failed to save config" });
    } finally {
      setSaving(false);
    }
  }

  async function handleTest() {
    setTesting(true);
    setMessage(null);
    try {
      const res = await fetch("/api/config/telegram/test", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ chat_id: config.chat_id }),
      });

      const data = await res.json();
      if (data.success) {
        setMessage({ type: "success", text: data.message });
      } else {
        setMessage({ type: "error", text: data.error || "Failed to send test alert." });
      }
    } catch (err) {
      setMessage({ type: "error", text: err instanceof Error ? err.message : "Test request failed" });
    } finally {
      setTesting(false);
    }
  }

  const botLink = botInfo?.bot_username ? `https://t.me/${botInfo.bot_username}` : "https://t.me";

  return (
    <div className="bg-gray-900 border border-gray-800 rounded-xl overflow-hidden shadow-sm">
      {/* Header / Toggle Button */}
      <button
        type="button"
        onClick={() => setIsOpen(!isOpen)}
        className="w-full px-5 py-4 flex items-center justify-between hover:bg-gray-800/40 transition-colors text-left"
        aria-expanded={isOpen}
      >
        <div className="flex items-center gap-3">
          <div className="p-2 rounded-lg bg-sky-500/10 text-sky-400 border border-sky-500/20">
            <svg className="h-5 w-5" viewBox="0 0 24 24" fill="currentColor">
              <path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm4.64 6.8c-.15 1.58-.8 5.42-1.13 7.19-.14.75-.42 1-.68 1.03-.58.05-1.02-.38-1.58-.75-.88-.58-1.38-.94-2.23-1.5-.99-.65-.35-1.01.22-1.59.15-.15 2.71-2.48 2.76-2.69a.2.2 0 00-.05-.18c-.06-.05-.14-.03-.21-.02-.09.02-1.49.95-4.22 2.79-.4.27-.76.41-1.08.4-.36-.01-1.04-.2-1.55-.37-.63-.2-1.12-.31-1.08-.66.02-.18.27-.36.74-.55 2.92-1.27 4.86-2.11 5.83-2.51 2.78-1.16 3.35-1.36 3.73-1.36.08 0 .27.02.39.12.1.08.13.19.14.27-.01.06.01.24 0 .38z" />
            </svg>
          </div>
          <div>
            <div className="flex items-center gap-2">
              <h2 className="text-sm font-semibold text-white">Telegram Bot & WebApp Settings</h2>
              {config.enabled && (
                <span className="text-[10px] bg-sky-950 text-sky-400 border border-sky-800 px-2 py-0.5 rounded-full font-medium">
                  Active
                </span>
              )}
            </div>
            <p className="text-xs text-gray-400 mt-0.5">
              Configure bot credentials, alert subscription chat, and Telegram WebApp integration
            </p>
          </div>
        </div>
        <svg
          className={`h-5 w-5 text-gray-400 transition-transform duration-200 ${isOpen ? "rotate-180" : ""}`}
          fill="none"
          viewBox="0 0 24 24"
          stroke="currentColor"
          aria-hidden="true"
        >
          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 9l-7 7-7-7" />
        </svg>
      </button>

      {/* Collapsible Content */}
      {isOpen && (
        <div className="border-t border-gray-800 p-5 space-y-6">
          {message && (
            <div
              className={`p-3.5 rounded-lg text-xs flex items-center justify-between ${
                message.type === "success"
                  ? "bg-emerald-950/80 border border-emerald-800/80 text-emerald-300"
                  : message.type === "warning"
                  ? "bg-amber-950/80 border border-amber-800/80 text-amber-300"
                  : "bg-red-950/80 border border-red-800/80 text-red-300"
              }`}
            >
              <span>{message.text}</span>
              <button
                type="button"
                onClick={() => setMessage(null)}
                className="text-gray-400 hover:text-white ml-2 text-sm"
              >
                ✕
              </button>
            </div>
          )}

          {loading ? (
            <div className="py-6 text-center text-xs text-gray-400 animate-pulse">Loading settings...</div>
          ) : (
            <div className="space-y-5">
              {/* Bot Info Header Card */}
              {botInfo?.authorized && (
                <div className="bg-sky-950/30 border border-sky-800/50 rounded-xl p-4 flex items-center justify-between">
                  <div className="flex items-center gap-3">
                    <div className="h-10 w-10 rounded-full bg-sky-500/20 flex items-center justify-center text-sky-400 font-bold">
                      TG
                    </div>
                    <div>
                      <div className="text-sm font-semibold text-white">
                        {botInfo.bot_name} <span className="text-sky-400 font-mono text-xs">(@{botInfo.bot_username})</span>
                      </div>
                      <div className="text-xs text-gray-400">
                        Bot authenticated &amp; listening for commands.
                      </div>
                    </div>
                  </div>
                  <a
                    href={botLink}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="bg-sky-600 hover:bg-sky-500 text-white text-xs px-3 py-1.5 rounded-lg font-medium transition flex items-center gap-1.5"
                  >
                    <span>Launch Bot</span>
                    <svg className="w-3 h-3" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M10 6H6a2 2 0 00-2 2v10a2 2 0 002 2h10a2 2 0 002-2v-4M14 4h6m0 0v6m0-6L10 14" />
                    </svg>
                  </a>
                </div>
              )}

              {/* Toggle Enable */}
              <div className="flex items-center justify-between p-3 bg-gray-800/50 rounded-lg border border-gray-700/50">
                <div>
                  <div className="text-xs font-medium text-white">Enable Telegram Integration</div>
                  <div className="text-[11px] text-gray-400">
                    Runs background command listener and dispatches cluster alerts
                  </div>
                </div>
                <label className="relative inline-flex items-center cursor-pointer">
                  <input
                    type="checkbox"
                    checked={config.enabled}
                    onChange={(e) => setConfig({ ...config, enabled: e.target.checked })}
                    className="sr-only peer"
                  />
                  <div className="w-10 h-5 bg-gray-700 peer-focus:outline-none rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:rounded-full after:h-4 after:w-4 after:transition-all peer-checked:bg-sky-500" />
                </label>
              </div>

              {/* Bot Token */}
              <div>
                <label className="block text-xs font-medium text-gray-300 mb-1">
                  Telegram Bot Token <span className="text-red-400">*</span>
                </label>
                <input
                  type="password"
                  value={config.bot_token}
                  onChange={(e) => setConfig({ ...config, bot_token: e.target.value })}
                  placeholder="8763217188:AAHQCttBHBskdaCqLiUkqqYAWnSg4c3SiRw"
                  className={inputClass}
                />
                <p className="text-[11px] text-gray-500 mt-1">Obtained from @BotFather on Telegram.</p>
              </div>

              <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                {/* Target Chat ID */}
                <div>
                  <label className="block text-xs font-medium text-gray-300 mb-1">
                    Alert Chat ID (User or Channel/Group)
                  </label>
                  <input
                    type="text"
                    value={config.chat_id}
                    onChange={(e) => setConfig({ ...config, chat_id: e.target.value })}
                    placeholder="Auto-detected when you send /start"
                    className={inputClass}
                  />
                  <p className="text-[11px] text-gray-500 mt-1">
                    Tip: Leave empty and simply type <code className="text-sky-400">/start</code> in your Telegram bot to auto-bind.
                  </p>
                </div>

                {/* WebApp URL */}
                <div>
                  <label className="block text-xs font-medium text-gray-300 mb-1">
                    Telegram WebApp URL (CNC UI)
                  </label>
                  <input
                    type="text"
                    value={config.webapp_url}
                    onChange={(e) => setConfig({ ...config, webapp_url: e.target.value })}
                    placeholder="http://localhost:3000 or https://your-domain.com"
                    className={inputClass}
                  />
                  <p className="text-[11px] text-gray-500 mt-1">
                    URL launched when clicking "Open Web Dashboard" in Telegram.
                  </p>
                </div>
              </div>

              {/* Notification Toggles */}
              <div>
                <label className="block text-xs font-medium text-gray-300 mb-2">Automated Notifications</label>
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-2.5">
                  <label className="flex items-center gap-2 p-2.5 bg-gray-800/40 rounded-lg border border-gray-700/40 cursor-pointer hover:bg-gray-800">
                    <input
                      type="checkbox"
                      checked={config.notify_job_start}
                      onChange={(e) => setConfig({ ...config, notify_job_start: e.target.checked })}
                      className="rounded border-gray-700 text-sky-500 focus:ring-sky-500"
                    />
                    <span className="text-xs text-gray-300">Notify on Job Started</span>
                  </label>

                  <label className="flex items-center gap-2 p-2.5 bg-gray-800/40 rounded-lg border border-gray-700/40 cursor-pointer hover:bg-gray-800">
                    <input
                      type="checkbox"
                      checked={config.notify_job_complete}
                      onChange={(e) => setConfig({ ...config, notify_job_complete: e.target.checked })}
                      className="rounded border-gray-700 text-sky-500 focus:ring-sky-500"
                    />
                    <span className="text-xs text-gray-300">Notify on Job Completed</span>
                  </label>

                  <label className="flex items-center gap-2 p-2.5 bg-gray-800/40 rounded-lg border border-gray-700/40 cursor-pointer hover:bg-gray-800">
                    <input
                      type="checkbox"
                      checked={config.notify_job_fail}
                      onChange={(e) => setConfig({ ...config, notify_job_fail: e.target.checked })}
                      className="rounded border-gray-700 text-sky-500 focus:ring-sky-500"
                    />
                    <span className="text-xs text-gray-300">Notify on Job Failed</span>
                  </label>

                  <label className="flex items-center gap-2 p-2.5 bg-gray-800/40 rounded-lg border border-gray-700/40 cursor-pointer hover:bg-gray-800">
                    <input
                      type="checkbox"
                      checked={config.notify_worker_events}
                      onChange={(e) => setConfig({ ...config, notify_worker_events: e.target.checked })}
                      className="rounded border-gray-700 text-sky-500 focus:ring-sky-500"
                    />
                    <span className="text-xs text-gray-300">Notify on Worker Online / Offline</span>
                  </label>
                </div>
              </div>

              {/* Action Buttons */}
              <div className="flex flex-wrap items-center gap-3 pt-2">
                <button
                  type="button"
                  onClick={handleSave}
                  disabled={saving}
                  className="bg-sky-600 hover:bg-sky-500 text-white text-xs font-semibold px-4 py-2 rounded-lg transition disabled:opacity-50 flex items-center gap-1.5"
                >
                  {saving ? "Saving..." : "Save Settings"}
                </button>

                <button
                  type="button"
                  onClick={handleTest}
                  disabled={testing}
                  className="bg-gray-800 hover:bg-gray-700 text-gray-200 text-xs font-medium px-4 py-2 rounded-lg border border-gray-700 transition disabled:opacity-50 flex items-center gap-1.5"
                >
                  {testing ? "Sending..." : "⚡ Send Test Alert"}
                </button>

                <a
                  href={botLink}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="ml-auto text-xs text-sky-400 hover:text-sky-300 flex items-center gap-1 transition"
                >
                  <span>Open in Telegram</span>
                  <svg className="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M14 5l7 7m0 0l-7 7m7-7H3" />
                  </svg>
                </a>
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
