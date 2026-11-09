"use client";

import { useState, useEffect, useCallback } from "react";
import { DEFAULT_DRIVE_CALLBACK, driveConnectionURL } from "@/lib/drive-oauth";

interface GDriveConfig {
  redirect_url: string;
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

interface DriveRestoreStatus {
  status: string;
  scope: string;
  restored: number;
  skipped: number;
  failed: number;
  bytes: number;
  error?: string;
}

interface CallbackGuidance {
  savedOrigin: string;
}

const inputClass =
  "w-full bg-gray-800 border border-gray-700 rounded-lg px-3 py-2 text-sm text-gray-100 " +
  "placeholder:text-gray-600 focus:outline-none focus:ring-2 focus:ring-blue-500/50 focus:border-blue-500 " +
  "transition-colors";

export function GDriveSettings() {
  const [isOpen, setIsOpen] = useState(false);
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [callbackGuidance, setCallbackGuidance] = useState<CallbackGuidance | null>(null);
  const [config, setConfig] = useState<GDriveConfig>({
    redirect_url: "",
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
  const [secretSaved, setSecretSaved] = useState(false);
  const [connecting, setConnecting] = useState(false);
  const [syncing, setSyncing] = useState(false);
  const [restore, setRestore] = useState<DriveRestoreStatus | null>(null);
  const [startingRestore, setStartingRestore] = useState(false);
  const restoring = startingRestore || restore?.status === "running";
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

  useEffect(() => {
    if (!isOpen) return;
    let cancelled = false;
    const check = async () => {
      try {
        const response = await fetch("/api/config/gdrive/restore", { cache: "no-store", signal: AbortSignal.timeout(10000) });
        if (response.ok && !cancelled) setRestore(await response.json());
      } catch { /* the next poll can recover after a network interruption */ }
    };
    void check();
    const timer = setInterval(check, 2500);
    return () => { cancelled = true; clearInterval(timer); };
  }, [isOpen]);

  useEffect(() => {
    if (!connecting) return;
    let cancelled = false;
    const interval = setInterval(async () => {
      try {
        const res = await fetch("/api/config/gdrive/auth-status");
        if (res.ok && !cancelled) {
          const status: AuthStatus = await res.json();
          setAuthStatus(status);
          if (status.authorized) setConnecting(false);
        }
      } catch { /* retry while the authorization window is open */ }
    }, 2000);
    const timeout = setTimeout(() => {
      setConnecting(false);
      setMessage({ type: "warning", text: "Authorization timed out. Connect again to retry." });
    }, 10 * 60 * 1000);
    return () => { cancelled = true; clearInterval(interval); clearTimeout(timeout); };
  }, [connecting]);

  useEffect(() => {
    if (new URLSearchParams(window.location.search).get("connectDrive") === "1") setIsOpen(true);
  }, []);

  useEffect(() => {
    try {
      const callback = new URL(config.redirect_url);
      if (callback.origin === window.location.origin) {
        setCallbackGuidance(null);
        return;
      }
      setCallbackGuidance({
        savedOrigin: callback.origin,
      });
    } catch {
      setCallbackGuidance(null);
    }
  }, [config.redirect_url]);

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
      if (data.gdrive) {
        const callback = data.gdrive.redirect_url || DEFAULT_DRIVE_CALLBACK;
        setConfig({ ...data.gdrive, upload_interval: data.gdrive.upload_interval || "30s", client_secret: "", redirect_url: callback });
      }
      setSecretSaved(Boolean(data.client_secret_set));
      setAdvancedOpen(!data.client_secret_set);
    } catch (err) {
      setMessage({ type: "error", text: err instanceof Error ? err.message : "Failed to load config" });
    } finally {
      setLoading(false);
    }
  }

  async function handleSave(nextConfig: GDriveConfig = config): Promise<boolean> {
    setSaving(true);
    setMessage(null);
    try {
      const res = await fetch("/api/config/gdrive", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(nextConfig),
      });
      if (!res.ok) throw new Error(await res.text() || `Server returned ${res.status}`);
      setSecretSaved(Boolean(nextConfig.client_secret) || secretSaved);
      await fetchAuthStatus();
      setMessage({ type: "success", text: "Saved and applied. No restart required." });
      return true;
    } catch (err) {
      setMessage({ type: "error", text: err instanceof Error ? err.message : "Failed to save" });
      return false;
    } finally {
      setSaving(false);
    }
  }

  async function handleConnect() {
    const host = window.location.hostname;
    const loopback = host === "localhost" || host === "127.0.0.1" || host === "[::1]";
    if (window.location.protocol !== "https:" && !loopback) {
      setMessage({ type: "error", text: "Open this dashboard through the localhost tunnel or an HTTPS domain to connect Drive." });
      return;
    }
    const callback = config.redirect_url || DEFAULT_DRIVE_CALLBACK;
    const nextConfig = { ...config, redirect_url: callback };
    // Open before awaiting requests so browsers allow the authorization window.
    const popup = window.open("about:blank", "cnc-google-drive", "width=500,height=650");
    if (!popup) { setMessage({ type: "error", text: "Allow popups for this dashboard, then connect again." }); return; }
    popup.opener = null;
    setConnecting(true);
    try {
      if (!await handleSave(nextConfig)) { popup.close(); setConnecting(false); return; }
      setConfig(nextConfig);
      popup.location.href = await driveConnectionURL(callback);
      setMessage({ type: "warning", text: "Finish authorization in Google. CNC Manager keeps the saved callback unchanged across servers." });
    } catch (err) {
      popup.close(); setConnecting(false);
      setMessage({ type: "error", text: err instanceof Error ? err.message : "Failed to connect" });
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

  async function handleSync() {
    setSyncing(true);
    setMessage(null);
    try {
      const res = await fetch("/api/config/gdrive/sync", { method: "POST" });
      if (!res.ok) throw new Error(await res.text() || `Server returned ${res.status}`);
      setMessage({ type: "success", text: "Drive sync queued. Files in the watch directory will be checked shortly." });
    } catch (err) {
      setMessage({ type: "error", text: err instanceof Error ? err.message : "Could not start Drive sync." });
    } finally {
      setSyncing(false);
    }
  }

  async function handleRestore() {
    setStartingRestore(true);
    setMessage(null);
    try {
      const response = await fetch("/api/config/gdrive/restore", { method: "POST", signal: AbortSignal.timeout(15000) });
      if (!response.ok) throw new Error((await response.text()) || "Could not start Drive restore.");
      setRestore(await response.json());
      setMessage({ type: "success", text: "Restoring missing files from Drive in the background. Existing local files are kept." });
    } catch (error) {
      setMessage({ type: "error", text: error instanceof Error ? error.message : "Could not start Drive restore." });
    } finally { setStartingRestore(false); }
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
                  role="switch" aria-label="Enable Google Drive" aria-checked={config.enabled}
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
                    <div className="flex items-center gap-2">
                    <button type="button" onClick={handleSync} disabled={syncing || !config.enabled}
                      className="text-xs text-blue-300 hover:text-blue-200 disabled:opacity-50 px-3 py-1.5 rounded-lg border border-blue-800">
                      {syncing ? "Queuing..." : "Sync now"}
                    </button>
                    <button type="button" onClick={handleRestore} disabled={restoring}
                      className="text-xs text-blue-300 hover:text-blue-200 disabled:opacity-50 px-3 py-1.5 rounded-lg border border-blue-800">
                      {restoring ? "Restoring…" : "Restore from Drive"}
                    </button>
                    <button
                      type="button"
                      onClick={handleDisconnect}
                      className="text-xs text-gray-400 hover:text-red-400 transition-colors px-3 py-1.5 rounded-lg border border-gray-700 hover:border-red-800"
                    >
                      Disconnect
                    </button>
                    </div>
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
                          {connecting ? "Waiting for authorization..." : "Choose your Google account to connect"}
                        </div>
                      </div>
                    </div>
                    <button
                      type="button"
                      onClick={handleConnect}
                      disabled={connecting || saving || !config.client_id.trim() || (!config.client_secret.trim() && !secretSaved)}
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
                {!authStatus.authorized && <div className="text-xs text-gray-400 space-y-2">
                  <button type="button" disabled className="rounded-lg border border-gray-700 px-3 py-1.5 opacity-50">Restore from Drive</button>
                  <p>Connect Google Drive to restore missing files from your old server.</p>
                </div>}
                {authStatus.authorized && <p className="text-xs text-gray-400">Restore downloads missing files into the watch folder. Existing local files are kept. {config.parent_folder_id ? "Uses your saved Drive folder." : "Uses CNC uploads in Drive root (files marked SHA256)."} Restored copies stay local even with Delete After Upload enabled.</p>}
                {restore && restore.status !== "idle" && <div role="status" aria-live="polite" className="text-xs text-gray-300">
                  <p>Restore: {restore.status.replaceAll("_", " ")} · {restore.restored} restored · {restore.skipped} skipped · {restore.failed} failed</p>
                  {restore.error && <p className="text-amber-300">{restore.error}</p>}
                </div>}
                {callbackGuidance && !authStatus.authorized && (
                  <div role="status" className="rounded-lg border border-blue-800/70 bg-blue-950/30 p-3 text-xs text-blue-200 space-y-1">
                    <p>Google returns through the fixed address <code className="text-blue-100">{callbackGuidance.savedOrigin}</code>.</p>
                    <p>Keep CNC Manager open. This address stays the same when the dashboard port or server changes.</p>
                  </div>
                )}
                {secretSaved && !callbackGuidance && <p className="text-xs text-gray-400">Your saved Google connection settings are ready. Click Connect and approve in Google.</p>}
                {(!config.client_id.trim() || (!config.client_secret.trim() && !secretSaved)) && (
                  <p className="text-xs text-amber-400">Enter your OAuth Client ID and Secret below. Connect saves and applies the settings automatically.</p>
                )}
              </div>

              {connecting && <button type="button" onClick={() => { setConnecting(false); setMessage(null); }} className="text-xs text-gray-400">Cancel waiting</button>}

              <details open={advancedOpen} onToggle={event => setAdvancedOpen(event.currentTarget.open)} className="rounded-lg border border-gray-700 p-3">
                <summary className="cursor-pointer text-sm text-gray-300">Advanced settings</summary>
                <div className="space-y-4 pt-4">
              {/* Config fields */}
              <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                <div>
                  <label htmlFor="drive-watch_dir" className="block text-xs font-medium text-gray-300 mb-1.5">Watch Directory</label>
                  <input type="text" className={inputClass} id="drive-watch_dir" value={config.watch_dir}
                    onChange={(e) => updateField("watch_dir", e.target.value)} placeholder="~/merged" />
                </div>

                <div>
                  <label htmlFor="drive-upload_interval" className="block text-xs font-medium text-gray-300 mb-1.5">Upload Interval</label>
                  <input type="text" className={inputClass} id="drive-upload_interval" value={config.upload_interval}
                    onChange={(e) => updateField("upload_interval", e.target.value)} placeholder="30s" />
                </div>

                <div>
                  <label htmlFor="drive-client_id" className="block text-xs font-medium text-gray-300 mb-1.5">OAuth Client ID</label>
                  <input type="text" className={`${inputClass} font-mono text-xs`} id="drive-client_id" value={config.client_id}
                    onChange={(e) => { updateField("client_id", e.target.value); setSecretSaved(false); }} placeholder="xxxxx.apps.googleusercontent.com" />
                </div>

                <div>
                  <label htmlFor="drive-client_secret" className="block text-xs font-medium text-gray-300 mb-1.5">OAuth Client Secret</label>
                  <input type="password" className={`${inputClass} font-mono text-xs`} id="drive-client_secret" value={config.client_secret}
                    onChange={(e) => updateField("client_secret", e.target.value)} placeholder={secretSaved ? "Saved secret — leave blank to keep" : "GOCSPX-..."} />
                </div>

                <div className="md:col-span-2">
                  <label htmlFor="drive-redirect" className="block text-xs font-medium text-gray-300 mb-1.5">OAuth Redirect URL</label>
                  <input id="drive-redirect" type="url" className={inputClass} value={config.redirect_url}
                    onChange={(e) => updateField("redirect_url", e.target.value)} />
                  <p className="text-xs text-gray-400 mt-1">Add this exact URL to your OAuth client’s Authorized redirect URIs in Google Cloud. Enable the Google Drive API and add your Google account as a test user if the app is in Testing.</p>
                  <p className="text-xs text-gray-400 mt-1">For HTTP, open the dashboard through your localhost SSH tunnel before connecting. Public callbacks require an HTTPS domain.</p>
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
                  role="switch" aria-label="Delete After Upload" aria-checked={config.delete_after_upload}
                  onClick={() => updateField("delete_after_upload", !config.delete_after_upload)}
                  className={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors ${config.delete_after_upload ? "bg-blue-600" : "bg-gray-700"}`}
                >
                  <span className={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${config.delete_after_upload ? "translate-x-6" : "translate-x-1"}`} />
                </button>
              </div>

                <a href="https://console.cloud.google.com/apis/credentials" target="_blank" rel="noopener noreferrer" className="text-xs text-blue-400">Manage Google credentials →</a>
                </div>
              </details>

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
                    <div className="flex items-center gap-2">
                  <button type="button" onClick={() => void handleSave()} disabled={saving || connecting}
                    className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-blue-600 hover:bg-blue-500 disabled:opacity-50 disabled:cursor-not-allowed text-white text-sm font-medium transition-colors">
                    {saving ? (
                      <><svg className="animate-spin h-3.5 w-3.5" fill="none" viewBox="0 0 24 24">
                        <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                        <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v8z" />
                      </svg>Saving...</>
                    ) : "Save"}
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
