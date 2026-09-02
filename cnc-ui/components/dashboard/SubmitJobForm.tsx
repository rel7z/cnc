"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";

interface FormState {
  name: string;
  command: string;
  input_file: string;
  workers: string;
  timeout_seconds: string;
}

const defaults: FormState = {
  name: "",
  command: "",
  input_file: "",
  workers: "",
  timeout_seconds: "300",
};

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

export function SubmitJobForm() {
  const router = useRouter();
  const [form, setForm] = useState<FormState>(defaults);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  function set(key: keyof FormState, value: string) {
    setForm((prev) => ({ ...prev, [key]: value }));
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setError(null);

    const body: Record<string, unknown> = {
      name: form.name || form.input_file.split("/").pop(),
      command: form.command,
      input_file: form.input_file,
      timeout_seconds: parseInt(form.timeout_seconds, 10) || 300,
    };

    const w = parseInt(form.workers, 10);
    if (w > 0) body.workers = w;

    try {
      const res = await fetch("/api/jobs", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });

      if (!res.ok) {
        const msg = await res.text();
        throw new Error(msg || `Server returned ${res.status}`);
      }

      const data = (await res.json()) as { job_id: string };
      router.push(`/dashboard/jobs/${data.job_id}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unknown error");
      setSubmitting(false);
    }
  }

  return (
    <form onSubmit={handleSubmit} className="space-y-6 max-w-xl">
      <div className="bg-gray-900 border border-gray-800 rounded-xl p-5 space-y-4">
        <h3 className="text-sm font-semibold text-white">Job</h3>

        <Field label="Input file" required hint="Path to the file on the server to split and distribute">
          <input
            type="text"
            className={`${inputClass} font-mono`}
            placeholder="/data/targets.txt"
            value={form.input_file}
            onChange={(e) => set("input_file", e.target.value)}
            required
          />
        </Field>

        <Field
          label="Command"
          required
          hint="Use {input} as the placeholder for each chunk — e.g. node index.js {input}"
        >
          <input
            type="text"
            className={`${inputClass} font-mono`}
            placeholder="node index.js {input}"
            value={form.command}
            onChange={(e) => set("command", e.target.value)}
            required
          />
        </Field>

        <Field
          label="Workers"
          hint="Number of equal parts to split into. Leave blank to use all online workers."
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

        <Field label="Timeout per task (seconds)">
          <input
            type="number"
            min={1}
            className={inputClass}
            value={form.timeout_seconds}
            onChange={(e) => set("timeout_seconds", e.target.value)}
          />
        </Field>
      </div>

      {error && (
        <div className="bg-red-950/50 border border-red-800 rounded-lg px-4 py-3 text-sm text-red-300">
          {error}
        </div>
      )}

      <div className="flex items-center gap-3">
        <button
          type="submit"
          disabled={submitting}
          className="inline-flex items-center gap-2 px-5 py-2.5 rounded-lg bg-blue-600 hover:bg-blue-500 disabled:opacity-50 disabled:cursor-not-allowed text-white text-sm font-medium transition-colors"
        >
          {submitting && (
            <svg className="animate-spin h-4 w-4" fill="none" viewBox="0 0 24 24" aria-hidden="true">
              <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
              <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8v8z" />
            </svg>
          )}
          {submitting ? "Submitting…" : "Submit Job"}
        </button>
        <button
          type="button"
          onClick={() => router.back()}
          className="px-4 py-2.5 rounded-lg border border-gray-700 text-gray-400 hover:text-white hover:border-gray-500 text-sm transition-colors"
        >
          Cancel
        </button>
      </div>
    </form>
  );
}
