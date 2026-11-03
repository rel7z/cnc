"use client";

import { useEffect, useRef, useState } from "react";
import { Copy, Eye, EyeOff, Terminal, X } from "lucide-react";

type Target = { id: string; address?: string; metadata?: Record<string, string> };
const quote = (value: string) => "'" + value.replaceAll("'", "'\\''") + "'";

export function WorkerSSH({ worker, folder }: { worker: Target; folder?: string }) {
  const [open, setOpen] = useState(false);
  const [notice, setNotice] = useState("");
  const [password, setPassword] = useState("");
  const [knownPassword, setKnownPassword] = useState("");
  const [passwordError, setPasswordError] = useState("");
  const [passwordBusy, setPasswordBusy] = useState(false);
  const loginRequest = useRef(0);
  function clearLogin() { loginRequest.current++; setPassword(""); setKnownPassword(""); setPasswordError(""); setPasswordBusy(false); }
  function close() { clearLogin(); setOpen(false); }
  useEffect(() => { clearLogin(); return () => { loginRequest.current++; }; }, [worker.id]); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => { if (!password) return; const timer = setTimeout(() => setPassword(""), 60000); return () => clearTimeout(timer); }, [password]);
  async function login(action: "reveal" | "save") {
    const sequence = ++loginRequest.current;
    setPasswordBusy(true); setPasswordError(""); setNotice("");
    try {
      const response = await fetch("/api/workers/login", { method: "POST", cache: "no-store", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ worker_id: worker.id, action, ...(action === "save" ? { password: knownPassword } : {}) }) });
      const data = await response.json();
      if (sequence !== loginRequest.current) return;
      if (!response.ok) throw new Error(data.error || "Could not load worker login.");
      if (action === "reveal") setPassword(data.password);
      else { setPassword(""); setKnownPassword(""); setNotice("Password saved. This does not change the password on the worker."); }
    } catch (error) { if (sequence === loginRequest.current) setPasswordError((error as Error).message); }
    finally { if (sequence === loginRequest.current) setPasswordBusy(false); }
  }
  const dialog = useRef<HTMLDialogElement>(null);
  const passwordField = useRef<HTMLInputElement>(null);
  const commandField = useRef<HTMLTextAreaElement>(null);
  useEffect(() => { if (open) dialog.current?.showModal(); }, [open]);
  const root = folder || worker.metadata?.processor_workspace_root || "/root/processor";
  const host = worker.metadata?.ssh_host || worker.address || "";
  const user = worker.metadata?.ssh_user || "root";
  const port = Number(worker.metadata?.ssh_port) || 22;
  const valid = /^[a-zA-Z0-9][a-zA-Z0-9.:-]*$/.test(host) && /^[a-zA-Z0-9_-]+$/.test(user) && port > 0 && port <= 65535;
  const remote = `cd ${quote(root)} && exec bash -l`;
  const direct = valid ? `ssh -t -p ${port} ${quote(user + "@" + host)} ${quote(remote)}` : "SSH address is not available yet.";
  const manager = `python3 ~/Desktop/CNC_Manager.py worker-ssh --worker ${quote(worker.id)} --folder ${quote(root)}`;
  async function copy(value: string, secret = false) {
    try { await navigator.clipboard.writeText(value); setNotice("Copied to clipboard."); }
    catch { setNotice("Select the text and copy it manually."); if (secret) { passwordField.current?.focus(); passwordField.current?.select(); } else { commandField.current?.focus(); commandField.current?.select(); } }
  }
  return <>
    <button type="button" onClick={() => { setNotice(""); setOpen(true); }} className="inline-flex items-center gap-1.5 rounded-md border border-gray-700 px-2.5 py-1.5 text-xs text-blue-300 hover:border-blue-500 hover:bg-blue-500/10"><Terminal className="h-3.5 w-3.5" />SSH</button>
    {open && <dialog ref={dialog} onCancel={close} className="m-auto w-[min(95vw,640px)] max-h-[90vh] overflow-y-auto rounded-xl border border-gray-700 bg-gray-950 p-5 text-gray-200 shadow-2xl backdrop:bg-black/70">
      <div className="flex items-center justify-between gap-3"><div><h3 className="font-semibold text-white">Connect to {worker.id}</h3><p className="mt-1 text-xs text-gray-400">Open your Terminal and paste a command below.</p></div><button type="button" aria-label="Close SSH instructions" onClick={close} className="rounded p-2 hover:bg-gray-800"><X className="h-4 w-4" /></button></div>
      <div className="my-4 rounded-lg border border-gray-800 bg-gray-900 p-3"><p className="text-xs text-gray-500">Working folder</p><code className="break-all text-sm text-emerald-300">{root}</code></div>
      <label className="text-xs text-gray-400">CNC Manager on this Mac · uses your saved SSH key<textarea ref={commandField} readOnly value={manager} rows={3} onFocus={e => e.target.select()} className="mt-2 w-full resize-none rounded border border-gray-700 bg-black p-3 font-mono text-xs text-gray-200" /></label>
      <button type="button" onClick={() => void copy(manager)} className="mt-2 inline-flex items-center gap-2 rounded-md bg-blue-600 px-3 py-2 text-xs text-white hover:bg-blue-500"><Copy className="h-3.5 w-3.5" />Copy login command</button>
      <section aria-label="Worker password" className="mt-4 rounded-lg border border-gray-800 p-3">
        <h4 className="text-sm font-medium text-white">Worker password</h4>
        <p className="mt-1 text-xs text-gray-400">SSH login: <code>{user}@{host || "address unavailable"}</code> · port {port}. Passwords saved here are for logging into this worker.</p>
        <div className="mt-3 flex flex-wrap items-center gap-3">
          <button type="button" disabled={passwordBusy} onClick={() => password ? setPassword("") : void login("reveal")} className="inline-flex items-center gap-2 text-xs text-blue-300 disabled:opacity-50">{password ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}{passwordBusy ? "Loading…" : password ? "Hide password" : "Show password"}</button>
          {password && <button type="button" onClick={() => void copy(password, true)} className="text-xs text-blue-300">Copy password</button>}
        </div>
        {password && <input ref={passwordField} aria-label="Worker login password" readOnly autoComplete="off" value={password} onFocus={event => event.target.select()} className="mt-3 w-full rounded border border-gray-700 bg-black p-2 font-mono text-sm" />}
        {passwordError && <p role="alert" className="mt-2 text-xs text-amber-300">{passwordError}</p>}
        <details className="mt-3 text-xs text-gray-400"><summary className="cursor-pointer">Save a known password</summary>
          <p className="mt-2">For existing workers whose password was not saved. This stores your password; it does not reset or verify it.</p>
          <label className="mt-2 block">Known worker password<input type="password" autoComplete="new-password" value={knownPassword} maxLength={1024} onChange={event => setKnownPassword(event.target.value)} className="mt-1 w-full rounded border border-gray-700 bg-black p-2 text-white" /></label>
          <button type="button" disabled={passwordBusy || !knownPassword} onClick={() => void login("save")} className="mt-2 text-blue-300 disabled:opacity-50">Save password</button>
        </details>
      </section>
      <details className="mt-4 border-t border-gray-800 pt-3"><summary className="cursor-pointer text-xs text-gray-400">Other computers / SSH apps</summary><p className="mt-2 text-xs text-gray-500">Use an SSH key already authorized on this worker.</p><pre className="mt-2 overflow-x-auto rounded bg-black p-3 text-xs">{direct}</pre>{valid && <div className="flex gap-3 text-xs"><button type="button" onClick={() => void copy(direct)} className="text-blue-300">Copy SSH command</button><a className="text-blue-300" href={`ssh://${user}@${host.includes(":") ? `[${host}]` : host}:${port}`}>Open SSH app</a></div>}</details>
      {notice && <p role="status" className="mt-3 text-xs text-emerald-300">{notice}</p>}
    </dialog>}
  </>;
}
