"use client";
import { useEffect, useRef, useState } from "react";
import { loadTemplateChecks, templateChecksKey, templateRequest, validateTemplateDraft, type VersionTemplate } from "@/lib/version-templates";
import { type RuleDraft } from "@/lib/version-rules";

import { readTemplateTextFile, serializeTemplateText, templateTextFilename, type TextTemplate } from "@/lib/version-template-text";

const field = "rounded-lg border border-gray-700 bg-gray-950 px-3 py-2 text-sm text-white";
const button = "rounded-lg border border-gray-600 px-3 py-2 text-sm text-gray-200 hover:bg-gray-800 disabled:opacity-40";
export function VersionTemplateControls({ rules, onRules }: { rules: RuleDraft[]; onRules: (rules: RuleDraft[]) => void }) {
  const [templates, setTemplates] = useState<VersionTemplate[]>([]);
  const [selected, setSelected] = useState("");
  const [loaded, setLoaded] = useState<VersionTemplate | null>(null);
  const [baseline, setBaseline] = useState("");
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [status, setStatus] = useState("");
  const [confirmation, setConfirmation] = useState<"load" | "delete" | "upload" | "update-upload" | null>(null);
  const [pendingUpload, setPendingUpload] = useState<TextTemplate | null>(null);
  const [uploaded, setUploaded] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);
  const currentRules = useRef(rules);
  currentRules.current = rules;
  const dirty = loaded !== null && (templateChecksKey(rules) !== baseline || name !== loaded.name);
  const updatable = (loaded !== null && loaded.id === selected) || (uploaded && !!selected);
  useEffect(() => {
    let active = true;
    setBusy(true);
    templateRequest().then((data: VersionTemplate[]) => {
      if (!active) return;
      setTemplates(data);
      const savedDefault = data.find(template => template.is_default);
      if (savedDefault && currentRules.current.length === 0) {
        const checks = loadTemplateChecks(savedDefault, []);
        onRules(checks);
        setLoaded(savedDefault); setSelected(savedDefault.id); setName(savedDefault.name);
        setBaseline(templateChecksKey(checks));
        setStatus(`Loaded default “${savedDefault.name}”. Clear checks for ordinary discovery.`);
      }
    }).catch(e => { if (active) setError(e.message); }).finally(() => { if (active) setBusy(false); });
    return () => { active = false; };
  }, []);
  async function run(action: () => Promise<void>) {
    setBusy(true); setError(""); setStatus(""); setConfirmation(null);
    try { await action(); } catch (e) { setError((e as Error).message); } finally { setBusy(false); }
  }
  function remember(template: VersionTemplate, checks: RuleDraft[]) {
    setUploaded(false); setLoaded(template); setSelected(template.id); setName(template.name); setBaseline(templateChecksKey(checks));
  }
  async function load() {
    // Fetch the current revision at the moment of loading, not a cached list entry.
    const template: VersionTemplate = await templateRequest("/" + selected);
    const checks = loadTemplateChecks(template, rules);
    onRules(checks); remember(template, checks); setStatus(`Loaded “${template.name}”.`);
  }
  async function save(update: boolean) {
    validateTemplateDraft(name, rules);
    const previous = update ? (uploaded ? templates.find(t => t.id === selected)! : loaded!) : null;
    if (update && !previous) throw new Error("Select the saved template you want to update.");
    const template: VersionTemplate = await templateRequest(update ? "/" + previous!.id : "", update ? "PUT" : "POST", {
      name: name.trim(), checks: rules, ...(update ? { revision: previous!.revision } : {}),
    });
    setTemplates(current => [...current.filter(t => t.id !== template.id), template].sort((a, b) => a.name.localeCompare(b.name)));
    remember(template, rules); setStatus(update ? "Template updated." : "Template created.");
  }
  function applyUpload(template: TextTemplate) {
    onRules(template.checks); setName(template.name); setLoaded(null); setSelected(""); setBaseline(""); setUploaded(true); setPendingUpload(null); setConfirmation(null);
    setError(""); setStatus(`Loaded “${template.name}” from TXT. Use Save as new to store it, or choose a database template and Update to replace it.`);
  }
  async function upload(file: File) {
    setBusy(true); setError(""); setStatus(""); setConfirmation(null);
    try {
      const template = await readTemplateTextFile(file, rules);
      if (dirty || (!loaded && (rules.length > 0 || name.trim()))) { setPendingUpload(template); setConfirmation("upload"); }
      else applyUpload(template);
    } catch (e) { setError((e as Error).message); }
    finally { setBusy(false); }
  }
  function download() {
    setError("");
    try {
      const text = serializeTemplateText(name, rules);
      const url = URL.createObjectURL(new Blob([text], { type: "text/plain;charset=utf-8" }));
      const link = document.createElement("a"); link.href = url; link.download = templateTextFilename(name); document.body.appendChild(link); link.click(); link.remove();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
      setStatus("TXT downloaded with the current name and checks, including unsaved edits.");
    } catch (e) { setError((e as Error).message); }
  }
  async function confirmAction() {
    if (confirmation === "upload" && pendingUpload) { applyUpload(pendingUpload); return; }
    await run(confirmation === "load" ? load : confirmation === "update-upload" ? () => save(true) : remove);
  }
  async function remove() {
    const entry = loaded?.id === selected ? loaded : templates.find(t => t.id === selected)!;
    await templateRequest("/" + selected, "DELETE", { revision: entry.revision });
    setTemplates(current => current.filter(t => t.id !== selected)); setSelected("");
    if (loaded?.id === selected) { setLoaded(null); setBaseline(""); }
    setStatus("Template deleted. Current checks remain in the form.");
  }
  return <div className="space-y-3 rounded-lg border border-gray-700 bg-gray-900/40 p-3">
    <div className="flex flex-wrap items-end gap-2">
      <label className="flex min-w-48 flex-1 flex-col gap-1 text-sm text-gray-300">Templates
        <select aria-label="Templates" className={field} disabled={busy} value={selected} onChange={e => { setSelected(e.target.value); setConfirmation(null); setError(""); setStatus(""); }}>
          <option value="">Choose a template</option>{templates.map(t => <option key={t.id} value={t.id}>{t.name}</option>)}
        </select>
      </label>
      <button type="button" className={button} disabled={busy || !selected} onClick={() => { if (dirty || (!loaded && rules.length)) setConfirmation("load"); else void run(load); }}>Load</button>
      <button type="button" className={button} disabled={busy || !selected} onClick={() => setConfirmation("delete")}>Delete</button>
      <button type="button" className={button} disabled={busy} onClick={() => void run(async () => { setTemplates(await templateRequest()); setStatus("Template list refreshed. Current edits are preserved."); })}>Refresh templates</button>
    </div>
    <div className="flex flex-wrap items-end gap-2">
      <label className="flex min-w-48 flex-1 flex-col gap-1 text-sm text-gray-300">Template name
        <input aria-label="Template name" className={field} value={name} placeholder="WordPress plugins to check" disabled={busy} onChange={e => setName(e.target.value)} />
      </label>
      <button type="button" className={button} disabled={busy} onClick={() => void run(() => save(false))}>Save as new</button>
      <button type="button" className={button} disabled={busy || !updatable} onClick={() => { if (uploaded) setConfirmation("update-upload"); else void run(() => save(true)); }}>Update</button>
    </div>
    <div className="flex flex-wrap items-center gap-2">
      <button type="button" className={button} disabled={busy} onClick={download}>Download TXT</button>
      <button type="button" className={button} disabled={busy} onClick={() => fileInput.current?.click()}>Upload TXT</button>
      <input ref={fileInput} type="file" accept=".txt,text/plain" className="hidden" aria-label="Upload TXT template file" onChange={event => { const file = event.target.files?.[0]; event.target.value = ""; if (file) void upload(file); }} />
      <a className="text-sm text-blue-400 underline" href="/version-template-format.txt" download>TXT format guide</a>
      <a className="text-sm text-blue-400 underline" href="/default-version-checks.txt" download>Download default TXT</a>
    </div>
    <p className="text-xs text-gray-400">One check per line: <code>elementor &lt; 3.25.0</code>. Use <code>&amp;</code> for multiple conditions. Upload loads the template into this form; it does not save or launch a scan.</p>
    <p className="text-xs text-gray-400">Templates save version checks only. {loaded ? `Editing “${loaded.name}”${dirty ? " — unsaved changes." : "."}` : "Load a template to edit it, or save your current checks as new."}</p>
    {confirmation && <div role="alertdialog" aria-label={confirmation === "update-upload" ? "Replace saved template" : confirmation === "delete" ? "Delete template" : "Replace current version checks"} className="space-y-2 rounded-lg border border-amber-700 p-3 text-sm text-gray-200">
      <p>{confirmation === "update-upload" ? `Replace “${templates.find(t => t.id === selected)?.name}” in the database with this uploaded template's name and checks?` : confirmation === "delete" ? `Delete “${templates.find(t => t.id === selected)?.name}” from the database? Current checks will remain.` : "Replace your unsaved checks and template name with the uploaded or selected template?"}</p>
      <div className="flex gap-2"><button type="button" className={button} disabled={busy} onClick={() => void confirmAction()}>{confirmation === "update-upload" ? "Replace saved template" : confirmation === "delete" ? "Confirm delete" : "Replace checks"}</button><button type="button" className={button} onClick={() => { setConfirmation(null); setPendingUpload(null); }}>Cancel</button></div>
    </div>}
    {busy && <p role="status" className="text-sm text-gray-400">Loading templates…</p>}
    {error && <p role="alert" className="text-sm text-red-400">{error}</p>}
    {status && <p role="status" className="text-sm text-emerald-400">{status}</p>}
  </div>;
}
