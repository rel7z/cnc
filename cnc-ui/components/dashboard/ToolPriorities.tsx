"use client";
import { useEffect, useState } from "react";
export type ProgramPriority = { program_id: string; configured_rank: number; effective_rank: number; override_rank: number | null; revision: number };
export async function priorityRequest(id?: string, body?: { rank: number | null; revision: number }): Promise<ProgramPriority[] | ProgramPriority> {
 const response = await fetch('/api/processor/program-priorities' + (id ? '/' + encodeURIComponent(id) : ''), {
  method: body ? 'PUT' : 'GET', cache: 'no-store', ...(body ? { headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) } : {}),
 });
 const data = await response.json();
 if (!response.ok) throw new Error(data.error || 'Tool priorities are unavailable.');
 return data;
}
export function ToolPriorities() {
 const [priorities, setPriorities] = useState<ProgramPriority[]>([]);
 const [drafts, setDrafts] = useState<Record<string, number>>({});
 const [loading, setLoading] = useState(true);
 const [saving, setSaving] = useState<string | null>(null);
 const [error, setError] = useState('');
 const [status, setStatus] = useState('');
 async function refresh() {
  setLoading(true); setError('');
  try { setPriorities(await priorityRequest() as ProgramPriority[]); }
  catch (e) { setError((e as Error).message); }
  finally { setLoading(false); }
 }
 useEffect(() => { void refresh(); }, []);
 async function save(priority: ProgramPriority, reset: boolean) {
  setSaving(priority.program_id); setError(''); setStatus('');
  try {
   const next = await priorityRequest(priority.program_id, { rank: reset ? null : drafts[priority.program_id] ?? priority.effective_rank, revision: priority.revision }) as ProgramPriority;
   setPriorities(current => current.map(p => p.program_id === next.program_id ? next : p));
   setDrafts(current => { const nextDrafts = { ...current }; delete nextDrafts[priority.program_id]; return nextDrafts; });
   setStatus(`${priority.program_id}: ${reset ? 'reset to config' : 'rank saved'} (${next.effective_rank}).`);
  } catch (e) { setError((e as Error).message); }
  finally { setSaving(null); }
 }
 return <div className="space-y-2 rounded-lg border border-gray-700 p-3">
  <div className="flex items-center justify-between gap-2"><h3 className="text-sm font-semibold text-white">Tool priorities</h3><button type="button" onClick={() => void refresh()} disabled={loading || saving !== null} className="text-xs text-blue-400 disabled:opacity-50">Refresh priorities</button></div>
  <p className="text-xs text-gray-400">5 is highest, 1 is lowest. Higher ranks receive more job starts; every eligible tool gets turns. Changes apply to queued work after saving.</p>
  {loading && <p role="status" className="text-xs text-gray-400">Loading priorities…</p>}
  {error && <p role="alert" className="text-sm text-amber-400">{error} Refresh priorities to retrieve current revisions; your pending ranks are preserved.</p>}
  {status && <p role="status" className="text-xs text-green-400">{status}</p>}
  {priorities.length > 0 && <div className="overflow-x-auto"><table className="w-full text-left text-xs text-gray-300"><thead><tr><th className="p-2">Tool</th><th className="p-2">Config rank</th><th className="p-2">Effective rank</th><th className="p-2">Set rank</th><th className="p-2">Actions</th></tr></thead><tbody>{priorities.map(priority => {
   const draft = drafts[priority.program_id] ?? priority.effective_rank;
   const pending = draft !== priority.effective_rank;
   return <tr key={priority.program_id} className="border-t border-gray-800"><td className="p-2 break-all">{priority.program_id}</td><td className="p-2">{priority.configured_rank}</td><td className="p-2">{priority.effective_rank}{priority.override_rank !== null && <span className="ml-1 text-gray-500">saved override</span>}</td><td className="p-2"><select aria-label={`Rank for ${priority.program_id}`} value={draft} disabled={saving !== null || loading} onChange={event => setDrafts(current => ({ ...current, [priority.program_id]: Number(event.target.value) }))} className="rounded border border-gray-700 bg-gray-950 p-1 text-white">{[1,2,3,4,5].map(rank => <option key={rank} value={rank}>{rank}{rank === 5 ? ' — highest' : rank === 1 ? ' — lowest' : ''}</option>)}</select>{pending && <p className="text-amber-400">Unsaved</p>}</td><td className="p-2"><div className="flex gap-3"><button type="button" aria-label={`Save rank for ${priority.program_id}`} disabled={saving !== null || loading || !pending} onClick={() => void save(priority, false)} className="text-blue-400 disabled:opacity-40">Save</button><button type="button" aria-label={`Reset rank for ${priority.program_id} to config`} disabled={saving !== null || loading || priority.override_rank === null} onClick={() => void save(priority, true)} className="text-blue-400 disabled:opacity-40">Reset to config</button></div></td></tr>;
  })}</tbody></table></div>}
  {!loading && !error && priorities.length === 0 && <p className="text-xs text-gray-400">No registered processor tools.</p>}
 </div>;
}
