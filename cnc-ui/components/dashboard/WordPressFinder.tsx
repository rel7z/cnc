"use client";

import { useEffect, useState } from "react";

type GeneratorStatus = {
  enabled: boolean;
  active_job: string | null;
  merged_verified: number;
  hostnames_per_second: number | null;
  updated_at: number;
};

const control = "/api/tools/wordpress-generator/control";

export function WordPressFinder() {
  const [status, setStatus] = useState<GeneratorStatus | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    const controller = new AbortController();
    async function refresh() {
      try {
        const response = await fetch(control, { cache: "no-store", signal: controller.signal });
        if (!response.ok) throw new Error("Generator status unavailable.");
        setStatus(await response.json());
        setError("");
      } catch {
        if (!controller.signal.aborted) setError("Generator status unavailable.");
      }
    }
    void refresh();
    const timer = setInterval(refresh, 5000);
    return () => { clearInterval(timer); controller.abort(); };
  }, []);

  async function toggle() {
    if (!status) return;
    setSaving(true);
    try {
      const response = await fetch(control, {
        method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ enabled: !status.enabled }),
      });
      if (!response.ok) throw new Error("Could not change Generator state.");
      setStatus(await response.json());
      setError("");
    } catch {
      setError("Could not change Generator state.");
    } finally {
      setSaving(false);
    }
  }

  const stale = status && Date.now() / 1000 - status.updated_at > 90;
  const label = !status ? "Loading…" : stale ? "Service not reporting" :
    status.enabled ? (status.active_job ? "Scanning" : "Waiting for work") :
      status.active_job ? "Finishing current batch" : "Off";

  return (
    <section className="space-y-4 rounded-xl border border-white/10 bg-white/[0.03] p-5" aria-labelledby="wp-generator-title">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 id="wp-generator-title" className="font-semibold text-white">WordPress Generator</h2>
          <p className="mt-1 text-sm text-slate-400">Automatically discovers and verifies WordPress sites. Results go to Merged/wordpress.txt.</p>
        </div>
        <button type="button" role="switch" aria-label="WordPress Generator"
          aria-checked={status?.enabled ?? false} disabled={!status || saving} onClick={toggle}
          className="rounded-lg border border-white/15 px-4 py-2 text-sm text-white disabled:opacity-50">
          {saving ? "Saving…" : status?.enabled ? "On" : "Off"}
        </button>
      </div>
      <p className="text-sm text-slate-300" role="status">{label}{status && ` · ${status.merged_verified.toLocaleString()} verified`}
        {status?.hostnames_per_second != null && ` · ${status.hostnames_per_second} hosts/s last batch`}
      </p>
      {error && <p className="text-sm text-amber-300" role="alert">{error}</p>}
    </section>
  );
}
