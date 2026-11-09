"use client";

import { useCallback, useEffect, useState } from "react";
import { DEFAULT_DRIVE_CALLBACK, driveConnectionURL } from "@/lib/drive-oauth";

export default function DriveRecovery() {
  const [authorized, setAuthorized] = useState(false);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("Checking your saved connection…");
  const [signIn, setSignIn] = useState(false);
  const refresh = useCallback(async () => {
    try {
      const response = await fetch("/api/config/gdrive/auth-status", { cache: "no-store", signal: AbortSignal.timeout(10000) });
      if (response.status === 401) {
        setSignIn(true);
        setMessage("Sign in to check your connection and sync files.");
        return;
      }
      if (!response.ok) throw new Error();
      const status = await response.json();
      setSignIn(false);
      setAuthorized(Boolean(status.authorized));
      setMessage(status.authorized ? "Your saved Google Drive connection is available. You can sync without connecting again." : "Start a fresh connection below. The old link cannot be reused.");
    } catch {
      setAuthorized(false);
      setMessage("Cannot reach the controller. Check your localhost SSH tunnel, then check again.");
    }
  }, []);

  useEffect(() => { void refresh(); }, [refresh]);

  async function connect() {
    setBusy(true);
    try {
      const host = window.location.hostname;
      if (window.location.protocol !== "https:" && !["localhost", "127.0.0.1", "[::1]"].includes(host)) {
        throw new Error("Open Drive settings through your localhost SSH tunnel or an HTTPS domain to connect.");
      }
      const response = await fetch("/api/config/gdrive", { cache: "no-store", signal: AbortSignal.timeout(10000) });
      if (response.status === 401) {
        setSignIn(true);
        throw new Error("Sign in before connecting Google Drive.");
      }
      if (!response.ok) throw new Error("Could not load Drive settings. Check the tunnel and retry.");
      const { gdrive } = await response.json();
      if (!gdrive) throw new Error("Configure Google Drive in Drive settings first.");
      const saved = await fetch("/api/config/gdrive", {
        method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ ...gdrive, redirect_url: gdrive.redirect_url || DEFAULT_DRIVE_CALLBACK }),
        signal: AbortSignal.timeout(15000),
      });
      if (!saved.ok) throw new Error((await saved.text()) || "Could not save the current callback address.");
      window.location.assign(await driveConnectionURL(gdrive.redirect_url || DEFAULT_DRIVE_CALLBACK));
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "Could not connect Google Drive.");
      setBusy(false);
    }
  }

  async function sync() {
    setBusy(true);
    try {
      const response = await fetch("/api/config/gdrive/sync", { method: "POST", signal: AbortSignal.timeout(15000) });
      if (response.status === 401) {
        setSignIn(true);
        setAuthorized(false);
        setMessage("Sign in before syncing files.");
        return;
      }
      if (!response.ok) throw new Error((await response.text()) || "Could not queue sync. Check the connection and try again.");
      setMessage("Sync queued. The controller will check the watch directory shortly.");
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "Could not queue sync.");
    } finally { setBusy(false); }
  }

  const button = "rounded-lg border border-gray-600 px-4 py-2 text-sm disabled:opacity-50";
  return (
    <main className="min-h-screen bg-gray-950 text-gray-100 flex items-center justify-center p-6">
      <div className="max-w-lg space-y-5 rounded-xl border border-gray-700 bg-gray-900 p-7">
        <h1 className="text-xl font-semibold">Restore Google Drive connection</h1>
        <p className="text-sm text-gray-300">This authorization link expired, was already used, or belongs to an earlier dashboard session.</p>
        <p role="status" aria-live="polite" className="text-sm">{message}</p>
        <div className="flex flex-wrap gap-3">
          {signIn ? <a className={button} href="/login?next=%2Fdrive-recover">Sign in</a> : <>
            <button className={button} disabled={busy} onClick={connect}>Connect Google Drive</button>
            <button className={button} disabled={busy || !authorized} onClick={sync}>{busy ? "Queuing…" : "Sync now"}</button>
          </>}
          <button className={button} disabled={busy} onClick={() => void refresh()}>Check connection</button>
          <a className={button} href="/dashboard?connectDrive=1">Drive settings</a>
        </div>
        <p className="text-xs text-gray-400">For future connections, open Drive settings instead of saving a ticket link. HTTP authorization needs your localhost SSH tunnel to be running.</p>
      </div>
    </main>
  );
}
