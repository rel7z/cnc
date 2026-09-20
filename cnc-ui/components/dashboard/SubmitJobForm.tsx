"use client";

import { useState, useEffect } from "react";
import { useRouter } from "next/navigation";
import type { JobMode } from "@/lib/types";
import { ToolSelectDropdown, type ToolDefinition } from "./ToolSelectDropdown";

// ── Helpers ───────────────────────────────────────────────────────────────────

function basename(p: string): string {
  return p.split("/").filter(Boolean).pop() ?? p;
}

// ── Shared form field wrapper ─────────────────────────────────────────────────

function Field({
  label,
  hint,
  required,
  children,
}: {
  label: string;
  hint?: string;
  required?: boolean;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <label className="block text-xs font-medium text-gray-300">
        {label}
        {required && <span className="text-red-400 ml-0.5">*</span>}
      </label>
      {children}
      {hint && <p className="text-xs text-gray-500">{hint}</p>}
    </div>
  );
}

const inputClass =
  "w-full bg-gray-800 border border-gray-700 rounded-lg px-3 py-2 text-sm text-gray-100 " +
  "placeholder:text-gray-600 focus:outline-none focus:ring-2 focus:ring-blue-500/50 focus:border-blue-500 " +
  "transition-colors";

const inputDisabledClass =
  "w-full bg-gray-800/50 border border-gray-700/50 rounded-lg px-3 py-2 text-sm text-gray-500 " +
  "cursor-not-allowed transition-colors";

// ── Mode selector ─────────────────────────────────────────────────────────────

function ModeCard({
  mode,
  selected,
  title,
  description,
  onSelect,
}: {
  mode: string;
  selected: boolean;
  title: string;
  description: string;
  onSelect: () => void;
}) {
  const getTheme = () => {
    if (!selected) {
      return {
        card: "border-gray-700 bg-gray-800/50 hover:border-gray-600",
        dot: "border-gray-500",
        badge: "bg-gray-700 text-gray-400",
      };
    }
    if (mode === "broadcast") {
      return {
        card: "border-purple-500 bg-purple-500/10",
        dot: "border-purple-500 bg-purple-500",
        badge: "bg-purple-500/20 text-purple-300",
      };
    }
    if (mode === "tools") {
      return {
        card: "border-emerald-500 bg-emerald-500/10",
        dot: "border-emerald-500 bg-emerald-500",
        badge: "bg-emerald-500/20 text-emerald-300",
      };
    }
    return {
      card: "border-blue-500 bg-blue-500/10",
      dot: "border-blue-500 bg-blue-500",
      badge: "bg-blue-500/20 text-blue-300",
    };
  };

  const theme = getTheme();

  return (
    <button
      type="button"
      onClick={onSelect}
      className={[
        "flex-1 text-left rounded-xl border p-4 transition-colors focus:outline-none focus:ring-2 focus:ring-blue-500/50",
        theme.card,
      ].join(" ")}
      aria-pressed={selected}
    >
      <div className="flex items-center gap-2 mb-1">
        <span
          className={[
            "h-3 w-3 rounded-full border-2 transition-colors",
            theme.dot,
          ].join(" ")}
          aria-hidden="true"
        />
        <span className="text-sm font-semibold text-white">{title}</span>
        <span
          className={[
            "ml-auto text-xs font-mono px-1.5 py-0.5 rounded",
            theme.badge,
          ].join(" ")}
        >
          {mode}
        </span>
      </div>
      <p className="text-xs text-gray-400 leading-relaxed">{description}</p>
    </button>
  );
}

// ── Shared timeout field ──────────────────────────────────────────────────────

function TimeoutField({
  value,
  noTimeout,
  onChange,
  onToggleNoTimeout,
}: {
  value: string;
  noTimeout: boolean;
  onChange: (v: string) => void;
  onToggleNoTimeout: (v: boolean) => void;
}) {
  return (
    <Field label="Timeout per task">
      <div className="space-y-2">
        <div className="flex items-center gap-3">
          <input
            type="number"
            min={1}
            disabled={noTimeout}
            className={noTimeout ? inputDisabledClass : inputClass}
            placeholder="300"
            value={noTimeout ? "" : value}
            onChange={(e) => onChange(e.target.value)}
            aria-label="Timeout in seconds"
          />
          <span className="text-xs text-gray-500 whitespace-nowrap">seconds</span>
        </div>
        <label className="flex items-center gap-2 cursor-pointer select-none w-fit">
          <input
            type="checkbox"
            checked={noTimeout}
            onChange={(e) => onToggleNoTimeout(e.target.checked)}
            className="h-3.5 w-3.5 rounded border-gray-600 bg-gray-800 accent-blue-500"
          />
          <span className="text-xs text-gray-400">No timeout</span>
        </label>
      </div>
    </Field>
  );
}

// ── Spread form ───────────────────────────────────────────────────────────────

interface SpreadFields {
  name: string;
  input_file: string;
  workers: string;
  timeout_seconds: string;
  no_timeout: boolean;
}

const spreadDefaults: SpreadFields = {
  name: "",
  input_file: "",
  workers: "",
  timeout_seconds: "300",
  no_timeout: false,
};

function SpreadForm({
  onSubmit,
  submitting,
  error,
}: {
  onSubmit: (fields: SpreadFields) => void;
  submitting: boolean;
  error: string | null;
}) {
  const [form, setForm] = useState<SpreadFields>(spreadDefaults);

  function set<K extends keyof SpreadFields>(key: K, value: SpreadFields[K]) {
    setForm((prev) => ({ ...prev, [key]: value }));
  }

  const destFilename = form.input_file ? basename(form.input_file) : null;

  return (
    <div className="space-y-4">
      <Field
        label="Input file"
        required
        hint="Full path to the file on the server — it will be split and distributed to workers."
      >
        <input
          type="text"
          className={`${inputClass} font-mono`}
          placeholder="~/targets.txt"
          value={form.input_file}
          onChange={(e) => set("input_file", e.target.value)}
          required
        />
      </Field>

      {destFilename && (
        <div className="flex items-center gap-2 rounded-lg bg-gray-800/60 border border-gray-700/50 px-3 py-2">
          <span className="text-xs text-gray-500">Each worker receives</span>
          <code className="text-xs text-emerald-400 font-mono">~/{destFilename}</code>
        </div>
      )}

      <Field
        label="Workers"
        hint="How many equal parts to split into — one part per worker. Leave blank to use all online workers automatically."
      >
        <input
          type="number"
          min={1}
          className={inputClass}
          placeholder="auto"
          value={form.workers}
          onChange={(e) => set("workers", e.target.value)}
        />
      </Field>

      <Field label="Job name" hint="Optional — defaults to the input filename">
        <input
          type="text"
          className={inputClass}
          placeholder="my-scan"
          value={form.name}
          onChange={(e) => set("name", e.target.value)}
        />
      </Field>

      <TimeoutField
        value={form.timeout_seconds}
        noTimeout={form.no_timeout}
        onChange={(v) => set("timeout_seconds", v)}
        onToggleNoTimeout={(v) => set("no_timeout", v)}
      />

      {error && (
        <div className="bg-red-950/50 border border-red-800 rounded-lg px-4 py-3 text-sm text-red-300">
          {error}
        </div>
      )}

      <div className="flex items-center gap-3 pt-1">
        <button
          type="button"
          onClick={() => onSubmit(form)}
          disabled={submitting || !form.input_file}
          className="inline-flex items-center gap-2 px-5 py-2.5 rounded-lg bg-blue-600 hover:bg-blue-500 disabled:opacity-50 disabled:cursor-not-allowed text-white text-sm font-medium transition-colors"
        >
          {submitting && (
            <svg className="animate-spin h-4 w-4" fill="none" viewBox="0 0 24 24" aria-hidden="true">
              <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
              <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v8z" />
            </svg>
          )}
          {submitting ? "Distributing…" : "Distribute File"}
        </button>
      </div>
    </div>
  );
}

// ── Broadcast form ────────────────────────────────────────────────────────────

interface BroadcastFields {
  name: string;
  command: string;
  timeout_seconds: string;
  no_timeout: boolean;
}

const broadcastDefaults: BroadcastFields = {
  name: "",
  command: "",
  timeout_seconds: "300",
  no_timeout: false,
};

function BroadcastForm({
  onSubmit,
  submitting,
  error,
}: {
  onSubmit: (fields: BroadcastFields) => void;
  submitting: boolean;
  error: string | null;
}) {
  const [form, setForm] = useState<BroadcastFields>(broadcastDefaults);

  function set<K extends keyof BroadcastFields>(key: K, value: BroadcastFields[K]) {
    setForm((prev) => ({ ...prev, [key]: value }));
  }

  return (
    <div className="space-y-4">
      <Field
        label="Command"
        required
        hint="Runs as-is on every online worker — no file splitting. E.g. &quot;hostname &amp;&amp; whoami&quot; or &quot;uptime&quot;."
      >
        <input
          type="text"
          className={`${inputClass} font-mono`}
          placeholder="hostname && whoami"
          value={form.command}
          onChange={(e) => set("command", e.target.value)}
          required
        />
      </Field>

      <Field label="Job name" hint="Optional — defaults to the first word of the command">
        <input
          type="text"
          className={inputClass}
          placeholder="my-broadcast"
          value={form.name}
          onChange={(e) => set("name", e.target.value)}
        />
      </Field>

      <TimeoutField
        value={form.timeout_seconds}
        noTimeout={form.no_timeout}
        onChange={(v) => set("timeout_seconds", v)}
        onToggleNoTimeout={(v) => set("no_timeout", v)}
      />

      {error && (
        <div className="bg-red-950/50 border border-red-800 rounded-lg px-4 py-3 text-sm text-red-300">
          {error}
        </div>
      )}

      <div className="flex items-center gap-3 pt-1">
        <button
          type="button"
          onClick={() => onSubmit(form)}
          disabled={submitting || !form.command}
          className="inline-flex items-center gap-2 px-5 py-2.5 rounded-lg bg-purple-600 hover:bg-purple-500 disabled:opacity-50 disabled:cursor-not-allowed text-white text-sm font-medium transition-colors"
        >
          {submitting && (
            <svg className="animate-spin h-4 w-4" fill="none" viewBox="0 0 24 24" aria-hidden="true">
              <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
              <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v8z" />
            </svg>
          )}
          {submitting ? "Broadcasting…" : "Broadcast Command"}
        </button>
      </div>
    </div>
  );
}

// ── Tools form ────────────────────────────────────────────────────────────────

interface ToolFields {
  tool_id: string;
  input_file: string;
  output_file: string;
  threads: string;
  extra_args: string;
  wp_plugins: string;
  joomla_exts: string;
}

function ToolForm({
  onSubmit,
  submitting,
  error,
}: {
  onSubmit: (fields: ToolFields) => void;
  submitting: boolean;
  error: string | null;
}) {
  const [tools, setTools] = useState<ToolDefinition[]>([]);
  const [loadingTools, setLoadingTools] = useState(true);
  const [form, setForm] = useState<ToolFields>({
    tool_id: "cms-scan",
    input_file: "/root/file/x.txt",
    output_file: "x.txt",
    threads: "50",
    extra_args: "",
    wp_plugins: "",
    joomla_exts: "",
  });

  useEffect(() => {
    async function loadTools() {
      try {
        const res = await fetch("/api/tools");
        if (res.ok) {
          const data: ToolDefinition[] = await res.json();
          setTools(data);
          if (data.length > 0 && !form.tool_id) {
            setForm((prev) => ({ ...prev, tool_id: data[0].id }));
          }
        }
      } catch (err) {
        console.error("Failed to load tools:", err);
      } finally {
        setLoadingTools(false);
      }
    }
    loadTools();
  }, []);

  function set<K extends keyof ToolFields>(key: K, value: ToolFields[K]) {
    setForm((prev) => ({ ...prev, [key]: value }));
  }

  const selectedTool = tools.find((t) => t.id === form.tool_id);
  const isEnumTool = form.tool_id === "enum";

  const getCommandPreview = () => {
    const exec = selectedTool?.executable || form.tool_id || "tool";
    let cmd = exec;
    if (form.input_file) cmd += ` -l ${form.input_file}`;
    if (form.threads) cmd += ` -t ${form.threads}`;
    if (isEnumTool && form.wp_plugins) cmd += ` -wp-plugins /tmp/cnc_wp_plugins.txt`;
    if (isEnumTool && form.joomla_exts) cmd += ` -joomla-exts /tmp/cnc_joomla_exts.txt`;
    if (form.extra_args) cmd += ` ${form.extra_args}`;
    return cmd;
  };

  return (
    <div className="space-y-4">
      <Field
        label="Select Tool"
        required
        hint="Blueprint tool to execute across online workers in broadcast mode."
      >
        <ToolSelectDropdown
          tools={tools}
          selectedToolId={form.tool_id}
          onSelect={(id) => set("tool_id", id)}
          loading={loadingTools}
        />
      </Field>

      <Field
        label="Target Directory / Input File"
        required
        hint="Path to the file on worker machines (e.g. /root/file/x.txt)."
      >
        <input
          type="text"
          className={`${inputClass} font-mono`}
          placeholder="/root/file/x.txt"
          value={form.input_file}
          onChange={(e) => set("input_file", e.target.value)}
          required
        />
      </Field>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <Field
          label="Watcher Output Filename"
          hint="Destination in Google Drive watcher folder (auto-synced & deduplicated)."
        >
          <input
            type="text"
            className={`${inputClass} font-mono`}
            placeholder="x.txt"
            value={form.output_file}
            onChange={(e) => set("output_file", e.target.value)}
          />
        </Field>

        <Field
          label="Threads (Concurrency)"
          hint="Number of concurrent threads (-t flag)."
        >
          <input
            type="number"
            min={1}
            className={inputClass}
            placeholder="50"
            value={form.threads}
            onChange={(e) => set("threads", e.target.value)}
          />
        </Field>
      </div>

      <Field
        label="Extra Arguments"
        hint="Optional additional arguments to pass to the tool."
      >
        <input
          type="text"
          className={`${inputClass} font-mono`}
          placeholder="e.g. -v --debug"
          value={form.extra_args}
          onChange={(e) => set("extra_args", e.target.value)}
        />
      </Field>

      {/* ── Enum-specific wordlist inputs ─────────────────────────────── */}
      {isEnumTool && (
        <div className="space-y-4 rounded-xl border border-violet-800/40 bg-violet-950/20 p-4">
          <div className="flex items-center gap-2 mb-1">
            <span className="inline-flex items-center gap-1.5 rounded-full bg-violet-500/15 px-3 py-0.5 text-xs font-medium text-violet-300 border border-violet-700/40">
              <svg className="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z" /></svg>
              Custom Wordlists
            </span>
            <span className="text-xs text-gray-500">Leave blank to use built-in defaults</span>
          </div>

          <Field
            label="WordPress Plugin List"
            hint="One plugin slug per line (e.g. elementor, woocommerce). Overrides built-in defaults (20 common plugins)."
          >
            <textarea
              id="wp-plugins-textarea"
              rows={6}
              className={`${inputClass} font-mono resize-y text-xs`}
              placeholder={`elementor-pro\nwoocommerce\nwordfence\ncontact-form-7\n...`}
              value={form.wp_plugins}
              onChange={(e) => set("wp_plugins", e.target.value)}
            />
            {form.wp_plugins && (
              <p className="text-xs text-violet-400 mt-1">
                {form.wp_plugins.split("\n").filter(l => l.trim()).length} plugin(s) loaded
              </p>
            )}
          </Field>

          <Field
            label="Joomla Extension List"
            hint="One extension per line. Supports com_*, mod_*, tpl_*, lib_*, plugin:group:slug formats."
          >
            <textarea
              id="joomla-exts-textarea"
              rows={6}
              className={`${inputClass} font-mono resize-y text-xs`}
              placeholder={`com_akeeba\ncom_k2\ncom_virtuemart\nmod_menu\nplugin:system:astroid\n...`}
              value={form.joomla_exts}
              onChange={(e) => set("joomla_exts", e.target.value)}
            />
            {form.joomla_exts && (
              <p className="text-xs text-violet-400 mt-1">
                {form.joomla_exts.split("\n").filter(l => l.trim()).length} extension(s) loaded
              </p>
            )}
          </Field>
        </div>
      )}

      {/* Live Command Preview */}
      <div className="rounded-lg bg-gray-950 border border-gray-800 p-3 space-y-1.5">
        <span className="text-xs font-medium text-gray-400">Command Preview (Broadcast):</span>
        <div className="font-mono text-xs text-emerald-400 break-all">
          <span className="text-gray-500 mr-1.5">$</span>
          {getCommandPreview()}
        </div>
      </div>

      {error && (
        <div className="bg-red-950/50 border border-red-800 rounded-lg px-4 py-3 text-sm text-red-300">
          {error}
        </div>
      )}

      <div className="flex items-center gap-3 pt-1">
        <button
          type="button"
          onClick={() => onSubmit(form)}
          disabled={submitting || !form.input_file}
          className="inline-flex items-center gap-2 px-5 py-2.5 rounded-lg bg-emerald-600 hover:bg-emerald-500 disabled:opacity-50 disabled:cursor-not-allowed text-white text-sm font-medium transition-colors"
        >
          {submitting && (
            <svg className="animate-spin h-4 w-4" fill="none" viewBox="0 0 24 24" aria-hidden="true">
              <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
              <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v8z" />
            </svg>
          )}
          {submitting ? "Launching Tool…" : "Launch Tool (Broadcast)"}
        </button>
      </div>
    </div>
  );
}

// ── Main component ────────────────────────────────────────────────────────────

export function SubmitJobForm() {
  const router = useRouter();
  const [mode, setMode] = useState<"spread" | "broadcast" | "tools">("spread");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleSpreadSubmit(fields: SpreadFields) {
    setSubmitting(true);
    setError(null);

    const timeoutValue = fields.no_timeout
      ? -1
      : parseInt(fields.timeout_seconds, 10) || 300;

    const body: Record<string, unknown> = {
      name: fields.name || basename(fields.input_file),
      command: "cat {input}",
      input_file: fields.input_file,
      mode: "spread",
      timeout_seconds: timeoutValue,
    };

    const w = parseInt(fields.workers, 10);
    if (w > 0) body.workers = w;

    await submitAndRedirect("/api/jobs", body);
  }

  async function handleBroadcastSubmit(fields: BroadcastFields) {
    setSubmitting(true);
    setError(null);

    const timeoutValue = fields.no_timeout
      ? -1
      : parseInt(fields.timeout_seconds, 10) || 300;

    const words = fields.command.trim().split(/\s+/);
    const body: Record<string, unknown> = {
      name: fields.name || words[0] || "broadcast",
      command: fields.command,
      mode: "broadcast",
      timeout_seconds: timeoutValue,
    };

    await submitAndRedirect("/api/jobs", body);
  }

  async function handleToolSubmit(fields: ToolFields) {
    setSubmitting(true);
    setError(null);

    const threadsNum = parseInt(fields.threads, 10) || 50;

    const body: Record<string, unknown> = {
      tool_id: fields.tool_id || "cms-scan",
      input_file: fields.input_file,
      output_file: fields.output_file,
      options: {
        threads: threadsNum,
        extra_args: fields.extra_args,
      },
    };

    // Include custom wordlists only when enum tool and content is provided
    if (fields.tool_id === "enum") {
      if (fields.wp_plugins.trim()) body.wp_plugins = fields.wp_plugins.trim();
      if (fields.joomla_exts.trim()) body.joomla_exts = fields.joomla_exts.trim();
    }

    await submitAndRedirect("/api/tools/launch", body);
  }

  async function submitAndRedirect(endpoint: string, body: Record<string, unknown>) {
    try {
      const res = await fetch(endpoint, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });

      if (!res.ok) {
        const msg = await res.text();
        let parsedMsg = msg;
        try {
          const parsed = JSON.parse(msg);
          parsedMsg = parsed.error || msg;
        } catch {}
        throw new Error(parsedMsg || `Server returned ${res.status}`);
      }

      const data = (await res.json()) as { job_id: string };
      router.push(`/dashboard/jobs/${data.job_id}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unknown error");
      setSubmitting(false);
    }
  }

  return (
    <div className="space-y-6 max-w-3xl">
      {/* Mode selector */}
      <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
        <ModeCard
          mode="spread"
          selected={mode === "spread"}
          title="Spread"
          description="Split a file into N equal parts — one chunk per worker. Uses {input}."
          onSelect={() => { setMode("spread"); setError(null); }}
        />
        <ModeCard
          mode="broadcast"
          selected={mode === "broadcast"}
          title="Broadcast"
          description="Run the same shell command on every online worker at once. No file splitting."
          onSelect={() => { setMode("broadcast"); setError(null); }}
        />
        <ModeCard
          mode="tools"
          selected={mode === "tools"}
          title="Tools"
          description="Run security tools (e.g. CMS Scanner) across workers and auto-sync output."
          onSelect={() => { setMode("tools"); setError(null); }}
        />
      </div>

      {/* Mode-specific form */}
      {mode === "spread" && (
        <SpreadForm
          onSubmit={handleSpreadSubmit}
          submitting={submitting}
          error={error}
        />
      )}
      {mode === "broadcast" && (
        <BroadcastForm
          onSubmit={handleBroadcastSubmit}
          submitting={submitting}
          error={error}
        />
      )}
      {mode === "tools" && (
        <ToolForm
          onSubmit={handleToolSubmit}
          submitting={submitting}
          error={error}
        />
      )}
    </div>
  );
}
