"use client";

import { useState, useEffect, useCallback } from "react";

interface GDriveConfig {
  enabled: boolean;
  watch_dir: string;
  client_id: string;
  client_secret: string;
  token_path: string;
  upload_interval: string;
  parent_folder_id: string;
  delete_after_upload: boolean;
}

interface AuthStatus {
  authorized: boolean;
  configured: boolean;
  connected_email?: string;
}

const inputClass =
  "w-full bg-gray-800 border border-gray-700 rounded-lg px-3 py-2 text-sm text-gray-100 " +
  "placeholder:text-gray-600 focus:outline-none focus:ring-2 focus:ring-blue-500/50 focus:border-blue-500 " +
  "transition-colors";

export function GDriveSettings() {
  const [isOpen, setIsOpen] = useState(false);
  const [config, setConfig] = useState<GDriveConfig>({
    enabled: false,
    watch_dir: "~/merged",
    client_id: "",
    client_secret: "",
    token_path: "",
    upload_interval: "30s",
    parent_folder_id: "",
    delete_after_upload: false,
  });

  const [authStatus, setAuthStatus] = useState<AuthStatus>({ authorized: false, configured: false });
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [restarting, setRestarting] = useState(false);
  const [connecting, setConnecting] = useState(false);
  const [message, setMessage] = useState<{ type: "success" | "error" | "warning"; text: string } | null>(null);

  const fetchAuthStatus = useCallback(async () => {
    try {
      const res = await fetch("/api/config/gdrive/auth-status");
      if (res.ok) {
        const data = await res.json();
        setAuthStatus(data);
      }
    } catch {
      // server might be down, ignore
    }
  }, []);

  // Poll auth status every 3s when connecting (waiting for OAuth callback)
  useEffect(() => {
    if (!connecting) return;
    const interval = setInterval(async () => {
      await fetchAuthStatus();
      // Stop polling once authorized
      setAuthStatus((prev) => {
        if (prev.authorized) setConnecting(false);
        return prev;
      });
    }, 2000);
    return () => clearInterval(interval);
  }, [connecting, fetchAuthStatus]);

  useEffect(() => {
    if (isOpen) {
      loadConfig();
      fetchAuthStatus();
    }
  }, [isOpen, fetchAuthStatus]);

  async function loadConfig() {
    setLoading(true);
    try {
      const res = await fetch("/api/config/gdrive");
      if (!res.ok) throw new Error("Failed to load config");
      const data = await res.json();
      if (data.gdrive) setConfig(data.gdrive);
    } catch (err) {
      setMessage({ type: "error", text: err instanceof Error ? err.message : "Failed to load config" });
    } finally {
      setLoading(false);
    }
  }

  async function handleSave() {
    setSaving(true);
    setMessage(null);
    try {
      const res = await fetch("/api/config/gdrive", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(config),
      });
      if (!res.ok) throw new Error(await res.text() || `Server returned ${res.status}`);
      setMessage({ type: "success", text: "Saved! Restart server to apply." });
    } catch (err) {
      setMessage({ type: "error", text: err instanceof Error ? err.message : "Failed to save" });
    } finally {
      setSaving(false);
    }
  }

  async function handleConnect() {
    try {
      const res = await fetch("/api/config/gdrive/auth-url");
      if (!res.ok) throw new Error(await res.text());
      const { url } = await res.json();
      // Open Google auth in a new window
      window.open(url, "_blank", "width=500,height=600,noopener");
      setConnecting(true);
      setMessage({ type: "warning", text: "Waiting for Google authorization..." });
    } catch (err) {
      setMessage({ type: "error", text: err instanceof Error ? err.message : "Failed to get auth URL" });
    }
  }

  async function handleDisconnect() {
    try {
      const res = await fetch("/api/config/gdrive/auth-disconnect", { method: "POST" });
      if (!res.ok) throw new Error(await res.text());
      setAuthStatus({ authorized: false, configured: true });
      setMessage({ type: "success", text: "Disconnected from Google Drive." });
      setTimeout(() => setMessage(null), 3000);
    } catch (err) {
      setMessage({ type: "error", text: err instanceof Error ? err.message : "Failed to disconnect" });
    }
  }

  async function handleRestart() {
    setRestarting(true);
    setMessage({ type: "warning", text: "Restarting server..." });
    try {
      await fetch("/api/server/restart", { method: "POST" });
      // Wait for server to come back
      await new Promise((r) => setTimeout(r, 3000));
      // Poll until it's back
      for (let i = 0; i < 10; i++) {
        try {
          const check = await fetch("/api/stats");
          if (check.ok) {
            setMessage({ type: "success", text: "Server restarted!" });
            setTimeout(() => setMessage(null), 3000);
            await fetchAuthStatus();
            return;
          }
        } catch { /* still restarting */ }
        await new Promise((r) => setTimeout(r, 1000));
      }
      setMessage({ type: "warning", text: "Server restarting... Refresh the page." });
    } catch {
      setMessage({ type: "warning", text: "Server restarting... Refresh the page." });
    } finally {
      setRestarting(false);
    }
  }

  // When connecting becomes false and authorized, show success
  useEffect(() => {
    if (!connecting && authStatus.authorized && authStatus.connected_email) {
      setMessage({ type: "success", text: `Connected as ${authStatus.connected_email}` });
      setTimeout(() => setMessage(null), 4000);
    }
  }, [connecting, authStatus.authorized, authStatus.connected_email]);

  function updateField<K extends keyof GDriveConfig>(key: K, value: GDriveConfig[K]) {
    setConfig((prev) => ({ ...prev, [key]: value }));
  }

  return (
    <div className="bg-gray-900 border border-gray-800 rounded-xl overflow-hidden">
      {/* Header */}
      <button
        onClick={() => setIsOpen(!isOpen)}
        className="w-full px-5 py-3 flex items-center justify-between hover:bg-gray-800/50 transition-colors"
      >
        <div className="flex items-center gap-2">
          <svg className="h-4 w-4 text-gray-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
            <path strokeLinecap="round" strokeLinejoin="round"
              d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z"
            />
            <circle cx="12" cy="12" r="3" />
          </svg>
          <h2 className="text-sm font-semibold text-white">Google Drive Settings</h2>
        </div>
        <div className="flex items-center gap-2">
          {authStatus.authorized ? (
            <span className="text-xs text-emerald-400 flex items-center gap-1">
              <span className="h-1.5 w-1.5 rounded-full bg-emerald-400 animate-pulse" />
              {authStatus.connected_email ?? "Connected"}
            </span>
          ) : config.enabled ? (
            <span className="text-xs text-amber-400 flex items-center gap-1">
              <span className="h-1.5 w-1.5 rounded-full bg-amber-400" />
              Not connected
            </span>
          ) : null}
          <svg
            className={`h-4 w-4 text-gray-400 transition-transform ${isOpen ? "rotate-180" : ""}`}
            fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}
          >
            <path strokeLinecap="round" strokeLinejoin="round" d="M19 9l-7 7-7-7" />
          </svg>
        </div>
      </button>

      {/* Content */}
      {isOpen && (
        <div className="border-t border-gray-800 p-5 space-y-4">
          {loading ? (
            <div className="flex items-center justify-center py-8">
              <svg className="animate-spin h-6 w-6 text-blue-500" fill="none" viewBox="0 0 24 24">
                <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v8z" />
              </svg>
            </div>
          ) : (
            <div className="space-y-4 max-w-4xl">

              {/* Enable toggle */}
              <div className="flex items-center justify-between p-3 bg-gray-800/50 rounded-lg">
                <div>
                  <div className="text-sm font-medium text-white">Enable Google Drive</div>
                  <div className="text-xs text-gray-400 mt-0.5">Auto-upload files from watch directory</div>
                </div>
                <button
                  type="button"
                  onClick={() => updateField("enabled", !config.enabled)}
                  className={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors ${config.enabled ? "bg-blue-600" : "bg-gray-700"}`}
                >
                  <span className={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${config.enabled ? "translate-x-6" : "translate-x-1"}`} />
                </button>
              </div>

              {/* Google Account Connection */}
              <div className="p-4 bg-gray-800/50 rounded-lg space-y-3">
                <div className="text-xs font-semibold text-gray-300 uppercase tracking-wider">Google Account</div>
                {authStatus.authorized ? (
                  <div className="flex items-center justify-between">
                    <div className="flex items-center gap-3">
                      <div className="h-8 w-8 rounded-full bg-emerald-500/20 flex items-center justify-center">
                        <svg className="h-4 w-4 text-emerald-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                          <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
                        </svg>
                      </div>
                      <div>
                        <div className="text-sm text-white font-medium">{authStatus.connected_email}</div>
                        <div className="text-xs text-emerald-400">Connected</div>
                      </div>
                    </div>
                    <button
                      type="button"
                      onClick={handleDisconnect}
                      className="text-xs text-gray-400 hover:text-red-400 transition-colors px-3 py-1.5 rounded-lg border border-gray-700 hover:border-red-800"
                    >
                      Disconnect
                    </button>
                  </div>
                ) : (
                  <div className="flex items-center justify-between">
                    <div className="flex items-center gap-3">
                      <div className="h-8 w-8 rounded-full bg-gray-700 flex items-center justify-center">
                        <svg className="h-4 w-4 text-gray-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                          <path strokeLinecap="round" strokeLinejoin="round" d="M16 7a4 4 0 11-8 0 4 4 0 018 0zM12 14a7 7 0 00-7 7h14a7 7 0 00-7-7z" />
                        </svg>
                      </div>
                      <div>
                        <div className="text-sm text-gray-300">No account connected</div>
                        <div className="text-xs text-gray-500">
                          {connecting ? "Waiting for authorization..." : "Click to authorize with Google"}
                        </div>
                      </div>
                    </div>
                    <button
                      type="button"
                      onClick={handleConnect}
                      disabled={connecting || !authStatus.configured}
                      className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-blue-600 hover:bg-blue-500 disabled:opacity-50 disabled:cursor-not-allowed text-white text-sm font-medium transition-colors"
                    >
                      {connecting ? (
                        <>
                          <svg className="animate-spin h-3.5 w-3.5" fill="none" viewBox="0 0 24 24">
                            <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                            <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v8z" />
                          </svg>
                          Waiting...
                        </>
                      ) : (
                        <>
                          <svg className="h-3.5 w-3.5" viewBox="0 0 24 24" fill="currentColor">
                            <path d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92c-.26 1.37-1.04 2.53-2.21 3.31v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.09z" fill="#4285F4"/>
                            <path d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z" fill="#34A853"/>
                            <path d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l2.85-2.22.81-.62z" fill="#FBBC05"/>
                            <path d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z" fill="#EA4335"/>
                          </svg>
                          Connect Google Account
                        </>
                      )}
                    </button>
                  </div>
                )}
                {!authStatus.configured && (
                  <p className="text-xs text-amber-400">Save config first to enable Connect button.</p>
                )}
              </div>

              {/* Config fields */}
              <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                <div>
                  <label className="block text-xs font-medium text-gray-300 mb-1.5">Watch Directory</label>
                  <input type="text" className={inputClass} value={config.watch_dir}
                    onChange={(e) => updateField("watch_dir", e.target.value)} placeholder="~/merged" />
                </div>

                <div>
                  <label className="block text-xs font-medium text-gray-300 mb-1.5">Upload Interval</label>
                  <input type="text" className={inputClass} value={config.upload_interval}
                    onChange={(e) => updateField("upload_interval", e.target.value)} placeholder="30s" />
                </div>

                <div>
                  <label className="block text-xs font-medium text-gray-300 mb-1.5">OAuth Client ID</label>
                  <input type="text" className={`${inputClass} font-mono text-xs`} value={config.client_id}
                    onChange={(e) => updateField("client_id", e.target.value)} placeholder="xxxxx.apps.googleusercontent.com" />
                </div>

                <div>
                  <label className="block text-xs font-medium text-gray-300 mb-1.5">OAuth Client Secret</label>
                  <input type="password" className={`${inputClass} font-mono text-xs`} value={config.client_secret}
                    onChange={(e) => updateField("client_secret", e.target.value)} placeholder="GOCSPX-..." />
                </div>

                <div className="md:col-span-2">
                  <label className="block text-xs font-medium text-gray-300 mb-1.5">Drive Folder ID <span className="text-gray-500">(optional)</span></label>
                  <input type="text" className={inputClass} value={config.parent_folder_id}
                    onChange={(e) => updateField("parent_folder_id", e.target.value)} placeholder="Leave empty for root" />
                  <p className="text-xs text-gray-500 mt-1">From the folder URL: drive.google.com/drive/folders/<span className="text-gray-400">THIS_PART</span></p>
                </div>
              </div>

              {/* Delete after upload */}
              <div className="flex items-center justify-between p-3 bg-gray-800/50 rounded-lg">
                <div>
                  <div className="text-sm font-medium text-white">Delete After Upload</div>
                  <div className="text-xs text-gray-400 mt-0.5">Remove local files after successful upload</div>
                </div>
                <button
                  type="button"
                  onClick={() => updateField("delete_after_upload", !config.delete_after_upload)}
                  className={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors ${config.delete_after_upload ? "bg-blue-600" : "bg-gray-700"}`}
                >
                  <span className={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${config.delete_after_upload ? "translate-x-6" : "translate-x-1"}`} />
                </button>
              </div>

              {/* Message */}
              {message && (
                <div className={`rounded-lg px-4 py-2.5 text-xs ${
                  message.type === "success" ? "bg-emerald-950/50 border border-emerald-800 text-emerald-300"
                  : message.type === "warning" ? "bg-amber-950/50 border border-amber-800 text-amber-300"
                  : "bg-red-950/50 border border-red-800 text-red-300"
                }`}>
                  {message.text}
                </div>
              )}

              {/* Actions */}
              <div className="flex items-center justify-between pt-1">
                <a href="https://console.cloud.google.com/apis/credentials" target="_blank" rel="noopener noreferrer"
                  className="text-xs text-blue-400 hover:text-blue-300 transition-colors">
                  Manage credentials →
                </a>
                <div className="flex items-center gap-2">
                  <button type="button" onClick={handleSave} disabled={saving}
                    className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-blue-600 hover:bg-blue-500 disabled:opacity-50 disabled:cursor-not-allowed text-white text-sm font-medium transition-colors">
                    {saving ? (
                      <><svg className="animate-spin h-3.5 w-3.5" fill="none" viewBox="0 0 24 24">
                        <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                        <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v8z" />
                      </svg>Saving...</>
                    ) : "Save"}
                  </button>

                  <button type="button" onClick={handleRestart} disabled={restarting}
                    className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-emerald-600 hover:bg-emerald-500 disabled:opacity-50 disabled:cursor-not-allowed text-white text-sm font-medium transition-colors">
                    {restarting ? (
                      <><svg className="animate-spin h-3.5 w-3.5" fill="none" viewBox="0 0 24 24">
                        <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                        <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v8z" />
                      </svg>Restarting...</>
                    ) : (
                      <><svg className="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                        <path strokeLinecap="round" strokeLinejoin="round" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
                      </svg>Restart Server</>
                    )}
                  </button>
                </div>
              </div>

            </div>
          )}
        </div>
      )}
    </div>
  );
}
