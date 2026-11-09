"use client";

import { useCallback, useEffect, useState } from "react";

type Settings = { configured: boolean; reason?: string; account?: string; region?: string; instance_type?: string; worker_instance_type?: string; hourly_price_usd?: number; worker_hourly_price_usd?: number };
type Instance = { id: string; name: string; state: string; type: string; private_ip: string; public_ip: string };
type Worker = { id: string; status: string };
type Creation = { requested: number; created: Instance[]; error?: string; uncertain_label?: string; not_attempted: string[] };

async function api<T>(path: string, body?: unknown): Promise<T> {
  const response = await fetch(`/api/aws/${path}`, {
    cache: "no-store",
    ...(body === undefined ? {} : { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) }),
  });
  const data = await response.json();
  if (!response.ok) throw new Error(data.error || `AWS request failed (${response.status})`);
  return data;
}

export function AWSIntegration() {
  const [settings, setSettings] = useState<Settings | null>(null);
  const [instances, setInstances] = useState<Instance[]>([]);
  const [workers, setWorkers] = useState<Worker[]>([]);
  const [quota, setQuota] = useState<number | null>(null);
  const [label, setLabel] = useState("cnc-aws-worker-4");
  const [count, setCount] = useState(1);
  const [confirmCost, setConfirmCost] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");

  const refresh = useCallback(async () => {
    const current = await api<Settings>("settings");
    setSettings(current);
    if (!current.configured) return;
    const [nodes, limits, registration] = await Promise.all([
      api<Instance[]>("instances"),
      api<{ standard_vcpus: number }>("quota"),
      fetch("/api/workers", { cache: "no-store" }),
    ]);
    if (!registration.ok) throw new Error("Could not check CNC worker registration");
    setInstances(nodes);
    setQuota(limits.standard_vcpus);
    setWorkers(await registration.json());
  }, []);

  useEffect(() => {
    void refresh().catch((cause: Error) => setError(cause.message));
    const timer = setInterval(() => { void refresh().catch((cause: Error) => setError(cause.message)); }, 15000);
    return () => clearInterval(timer);
  }, [refresh]);

  const runningVCPUs = instances.filter(node => ["pending", "running"].includes(node.state)).length * 2;
  const available = quota == null ? 0 : Math.max(0, Math.floor((quota - runningVCPUs) / 2));
  const hourly = settings?.worker_hourly_price_usd || 0;

  async function createWorkers() {
    setBusy(true); setError(""); setMessage("");
    try {
      const result = await api<Creation>("workers", { label, count, confirm_cost: confirmCost });
      setMessage(`Created ${result.created.length} of ${result.requested} workers. CNC registration may take several minutes.`);
      if (result.error) setError(`${result.error}. Refresh instances before retrying ${result.uncertain_label || "the uncertain request"}.`);
      setConfirmCost(false);
      await refresh();
    } catch (cause) {
      setError((cause as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return <div className="overflow-auto p-6 space-y-5 text-gray-200">
    <div className="flex items-start justify-between gap-4">
      <div><h2 className="text-xl font-semibold">AWS workers</h2><p className="text-sm text-gray-400 mt-1">4 GiB EC2 workers in Ohio. Workers join this 8 GiB CNC controller automatically.</p></div>
      <button type="button" onClick={() => void refresh().catch((cause: Error) => setError(cause.message))} className="text-blue-400 text-sm">Refresh</button>
    </div>
    {error && <p role="alert" className="text-red-300 text-sm">{error}</p>}
    {message && <p role="status" className="text-green-300 text-sm">{message}</p>}
    {!settings && <p className="text-sm text-gray-400">Checking AWS connection…</p>}
    {settings && !settings.configured && <p role="alert" className="text-sm text-amber-300">AWS is unavailable on this controller: {settings.reason}</p>}
    {settings?.configured && <>
      <div className="grid grid-cols-2 md:grid-cols-4 gap-3 text-sm">
        <div className="rounded border border-gray-800 p-3"><span className="text-gray-400">Account</span><p className="font-medium">{settings.account}</p></div>
        <div className="rounded border border-gray-800 p-3"><span className="text-gray-400">Region</span><p className="font-medium">{settings.region}</p></div>
        <div className="rounded border border-gray-800 p-3"><span className="text-gray-400">EC2 quota</span><p className="font-medium">{runningVCPUs}/{quota ?? "…"} vCPUs</p></div>
        <div className="rounded border border-gray-800 p-3"><span className="text-gray-400">Available slots</span><p className="font-medium">{available} worker{available === 1 ? "" : "s"}</p></div>
      </div>
      <form onSubmit={event => { event.preventDefault(); void createWorkers(); }} className="rounded border border-gray-800 p-4 space-y-3">
        <h3 className="font-medium">Add workers</h3>
        <div className="grid grid-cols-1 md:grid-cols-2 gap-3 text-sm">
          <label>Worker name or prefix<input className="mt-1 w-full bg-gray-800 border border-gray-700 rounded px-3 py-2" value={label} onChange={event => setLabel(event.target.value)} pattern="[A-Za-z0-9_-]{3,64}" required /></label>
          <label>Count<input type="number" min={1} max={7} step={1} className="mt-1 w-full bg-gray-800 border border-gray-700 rounded px-3 py-2" value={count} onChange={event => setCount(Number(event.target.value))} required /></label>
        </div>
        <p className="text-xs text-gray-400">{count === 1 ? `Name: ${label}` : `Names: ${label}-1 through ${label}-${count}`}. Each worker: {settings.worker_instance_type}, 4 GiB RAM, 2 vCPUs.</p>
        <p className="text-sm">Compute: <strong>${(hourly * count).toFixed(5)}/hour</strong> for {count} new node{count === 1 ? "" : "s"}. EBS storage, public IPv4, and transfer are additional.</p>
        <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={confirmCost} onChange={event => setConfirmCost(event.target.checked)} />I approve this EC2 hourly cost</label>
        <button disabled={busy || !confirmCost || count < 1 || count > available || count > 7 || !hourly} className="bg-blue-600 px-4 py-2 rounded text-sm disabled:opacity-50">{busy ? "Creating…" : `Create ${count} worker${count === 1 ? "" : "s"}`}</button>
      </form>
      <div className="overflow-x-auto"><h3 className="font-medium mb-2">Instances</h3><table className="w-full text-sm text-left"><thead><tr className="text-gray-400"><th className="p-2">Name</th><th className="p-2">EC2</th><th className="p-2">Private IP</th><th className="p-2">CNC</th></tr></thead><tbody>{instances.map(node => <tr key={node.id} className="border-t border-gray-800"><td className="p-2">{node.name}</td><td className="p-2">{node.state}</td><td className="p-2">{node.private_ip}</td><td className="p-2">{node.name.endsWith("-server") ? "Controller" : workers.find(worker => worker.id === node.name)?.status || "Not registered"}</td></tr>)}</tbody></table></div>
    </>}
  </div>;
}
