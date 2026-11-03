"use client";
import { useEffect, useState } from "react";

type Region = { id: string; label: string; capabilities: string[] };
type Plan = { id: string; label: string; memory: number; vcpus: number; price: { monthly: number | null; hourly: number | null } };
type Instance = { id: number; label: string; status: string; type: string; region: string; ipv4: string[] };
type BatchResult = { requested: number; created: Instance[]; error?: string; uncertain_label?: string; not_attempted: string[] };
type Connection = { server_addr: string; http_addr: string; region: string; network?: string };
type Worker = { id: string; status: string };
async function request<T>(path: string, body?: unknown): Promise<T> {
 const response = await fetch(`/api/linode/${path}`, { cache: "no-store", ...(body === undefined ? {} : { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) }) });
 const data = await response.json(); if (!response.ok) throw new Error(data.error || `Request failed (${response.status})`); return data;
}
function isBatchResult(result: Instance | BatchResult): result is BatchResult { return Array.isArray((result as BatchResult).created); }
function priceLabel(plan: Plan) { return plan.price.monthly != null ? `$${plan.price.monthly}/month` : plan.price.hourly != null ? `$${plan.price.hourly}/hour` : "Price unavailable"; }
const field = "w-full bg-gray-800 border border-gray-700 rounded px-3 py-2 text-white";
export function LinodeIntegration() {
 const [token, setToken] = useState(""); const [configured, setConfigured] = useState(false);
 const [regions, setRegions] = useState<Region[]>([]); const [plans, setPlans] = useState<Plan[]>([]);
 const [instances, setInstances] = useState<Instance[]>([]); const [workers, setWorkers] = useState<Worker[]>([]);
 const [region, setRegion] = useState("sg-sin-2"); const [size, setSize] = useState("g6-standard-2");
 const [label, setLabel] = useState(""); const [serverAddr, setServerAddr] = useState(""); const [httpAddr, setHTTPAddr] = useState("");
 const [connection, setConnection] = useState<Connection | null>(null); const [advanced, setAdvanced] = useState(false);
 const [count, setCount] = useState(1); const [batch, setBatch] = useState<BatchResult | null>(null);
 const [publicKey, setPublicKey] = useState("");
 const [preview, setPreview] = useState("");
 const [backups, setBackups] = useState(false); const [busy, setBusy] = useState(false); const [error, setError] = useState(""); const [message, setMessage] = useState("");
 async function refresh() {
  const [nodes, response] = await Promise.all([request<Instance[]>("instances"), fetch("/api/workers", { cache: "no-store" })]);
  if (!response.ok) throw new Error("Could not check worker registration"); setInstances(nodes); setWorkers(await response.json());
 }
 async function load() {
  const catalog = await request<{ regions: Region[]; types: Plan[] }>("catalog");
  setRegions(catalog.regions.filter(item => item.capabilities.includes("Metadata"))); setPlans(catalog.types); await refresh();
  try { const detected = await request<Connection>("connection"); setConnection(detected); if (detected.region) setRegion(detected.region); } catch (e) { setConnection(null); setError((e as Error).message); }
 }
 useEffect(() => { let cancelled = false; request<{ configured: boolean }>("settings").then(async settings => { if (cancelled) return; setConfigured(settings.configured); if (settings.configured) await load(); }).catch(e => { if (!cancelled) setError(e.message); }); return () => { cancelled = true; }; }, []); // eslint-disable-line react-hooks/exhaustive-deps
 useEffect(() => { if (!configured) return; const timer = setInterval(() => refresh().catch(e => setError(e.message)), 10000); return () => clearInterval(timer); }, [configured]); // eslint-disable-line react-hooks/exhaustive-deps
 async function saveToken() { setBusy(true); setError(""); try { await request("settings", { token }); setToken(""); setConfigured(true); await load(); setMessage("Linode connected. API token saved on the server."); } catch (e) { setError((e as Error).message); } finally { setBusy(false); } }
 async function createWorker() {
  setBusy(true); setError(""); setMessage(""); setBatch(null);
  try {
   const result = await request<Instance | BatchResult>("workers", { label, count, region, type: size, image: "linode/ubuntu24.04", server_addr: advanced ? serverAddr : "", http_addr: advanced ? httpAddr : "", backups, public_key: publicKey });
   const summary: BatchResult = isBatchResult(result) ? result : { requested: 1, created: [result], not_attempted: [] };
   setBatch(summary); setMessage(`Created ${summary.created.length} of ${summary.requested} workers. Bootstrapping may take several minutes; watch CNC status below.`);
   if (summary.error) setError(summary.uncertain_label ? `${summary.error}. Check ${summary.uncertain_label} in the instance list before retrying; its creation result is uncertain.` : `${summary.error}. The listed workers already exist; do not recreate them.`);
   else setLabel("");
   await refresh();
  } catch (e) { setError(`${(e as Error).message}. Refresh instances before trying again.`); } finally { setBusy(false); }
 }

 const selected = plans.find(plan => plan.id === size);
 return <div className="p-6 overflow-auto space-y-6 text-gray-200">
  <div><h2 className="text-xl font-semibold">Linode</h2><p className="text-sm text-gray-400 mt-1">Create Ubuntu workers, choose their resources, and check registration with this server.</p></div>
  {error && <p role="alert" className="text-red-300">{error}</p>}{message && <p role="status" className="text-green-300">{message}</p>}
  <form onSubmit={event => { event.preventDefault(); void saveToken(); }} className="flex gap-3 items-end"><label className="flex-1">API token {configured && <span className="text-green-400 text-sm">Connected</span>}<input type="password" autoComplete="off" className={field} value={token} onChange={event => setToken(event.target.value)} placeholder={configured ? "Enter a token to replace the saved credential" : "Linode API token"} required /></label><button disabled={busy} className="px-4 py-2 bg-blue-600 rounded disabled:opacity-50">Connect</button></form>
  {configured && <>
   {batch && <div role="status" className="rounded border border-gray-700 p-3 text-sm"><p>{batch.created.map(node => node.label).join(", ") || "No confirmed creations"}</p>{batch.not_attempted.length > 0 && <p>Not attempted: {batch.not_attempted.join(", ")}</p>}</div>}
   <form onSubmit={event => { event.preventDefault(); void createWorker(); }} className="grid grid-cols-1 md:grid-cols-2 gap-4">
    <label>Workers to create<input type="number" min={1} max={20} step={1} required className={field} value={count} onChange={event => setCount(Number(event.target.value))} /></label>
    <label>{count > 1 ? "Worker label prefix" : "Worker label"}<input className={field} value={label} onChange={event => setLabel(event.target.value)} pattern="[A-Za-z0-9_-]{3,64}" required placeholder="cnc-linode-worker-7" /></label>
    <p className="text-sm text-gray-400 md:col-span-2">{count > 1 ? `Labels: ${label || "prefix"}-1 through ${label || "prefix"}-${count}` : "One worker uses the label exactly as entered."}</p>
    <label>Region<select className={field} value={region} onChange={event => setRegion(event.target.value)}>{regions.map(item => <option key={item.id} value={item.id}>{item.label} ({item.id})</option>)}</select></label>
    <label>Size<select className={field} value={size} onChange={event => setSize(event.target.value)}>{plans.map(plan => <option key={plan.id} value={plan.id} disabled={plan.price.monthly == null && plan.price.hourly == null}>{plan.label} · {plan.memory / 1024} GB · {plan.vcpus} vCPU · {priceLabel(plan)}</option>)}</select></label>
    <label>Image<input className={field} value="Ubuntu 24.04 LTS" readOnly /></label>
    <div className="md:col-span-2 rounded border border-gray-700 p-3 text-sm"><p>{connection ? connection.network ? `Controller connection configured automatically over ${connection.network}.` : `Controller connection detected automatically in ${connection.region}.` : "Automatic controller connection unavailable. Configure reachable networking before creating workers."}</p><p className="text-gray-400 mt-1">Workers use the controller connection for registration and download their program files from the same controller. Addresses are filled automatically for a same-region controller or a configured private cross-cloud connection.</p></div>
    <label className="md:col-span-2 flex gap-2 items-center"><input type="checkbox" checked={advanced} onChange={event => setAdvanced(event.target.checked)} />Advanced: override controller addresses</label>
    {advanced && <><label>Worker registration address<input className={field} value={serverAddr} onChange={event => setServerAddr(event.target.value)} required placeholder="Controller IP:port" /></label><label>Program download address<input className={field} value={httpAddr} onChange={event => setHTTPAddr(event.target.value)} required placeholder="Controller IP:port" /></label><p className="text-sm text-gray-400 md:col-span-2">Both addresses must already be reachable from the worker network. Private addresses do not connect different cloud providers automatically.</p></>}
    <label className="md:col-span-2">Additional SSH public key (optional)<input className={field} value={publicKey} onChange={event => setPublicKey(event.target.value)} placeholder="ssh-ed25519 … (server administration keys are included automatically)" /></label>
    <label className="flex gap-2 items-center"><input type="checkbox" checked={backups} onChange={event => setBackups(event.target.checked)} />Enable Linode backups (additional charges)</label>
    <div className="flex gap-4 items-center"><span>{selected ? priceLabel({ ...selected, price: { monthly: selected.price.monthly == null ? null : selected.price.monthly * count, hourly: selected.price.hourly == null ? null : selected.price.hourly * count } }) + ` for ${count} worker${count === 1 ? "" : "s"}` : "Choose a size"}</span><button disabled={busy || (!advanced && (!connection || (connection.region !== "" && connection.region !== region))) || !Number.isInteger(count) || count < 1 || count > 20 || !selected || (selected.price.monthly == null && selected.price.hourly == null)} className="bg-blue-600 px-4 py-2 rounded disabled:opacity-50">{busy ? `Creating ${count} worker${count === 1 ? "" : "s"}…` : `Create ${count} paid worker${count === 1 ? "" : "s"}`}</button></div>
   </form>
   <button className="text-blue-400" onClick={async () => { try { const query = new URLSearchParams({ label: count > 1 ? `${label}-1` : label, server_addr: advanced ? serverAddr : "", http_addr: advanced ? httpAddr : "" }); const result = await request<{ script: string }>(`bootstrap?${query}`); setPreview(result.script); } catch (e) { setError((e as Error).message); } }}>Preview worker setup</button>
   {preview && <pre className="bg-gray-950 p-4 rounded overflow-auto text-xs max-h-64">{preview}</pre>}
   <div className="flex items-center justify-between"><h3 className="font-semibold">Instances</h3><button onClick={() => refresh().catch(e => setError(e.message))} className="text-blue-400">Refresh</button></div>
   <table className="w-full text-sm text-left"><thead><tr><th className="p-2">Label</th><th>Resources / region</th><th>IP addresses</th><th>Cloud</th><th>CNC worker</th></tr></thead><tbody>{instances.map(node => <tr key={node.id} className="border-t border-gray-800"><td className="p-2">{node.label}</td><td>{node.type}<br />{node.region}</td><td>{node.ipv4.join(", ")}</td><td>{node.status}</td><td>{workers.find(worker => worker.id === node.label)?.status || (node.label.endsWith("-server") ? "Server" : "Not registered")}</td></tr>)}</tbody></table>
  </>}
 </div>;
}
