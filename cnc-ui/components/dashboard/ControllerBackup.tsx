"use client";

import { useCallback, useEffect, useState } from "react";
import { Archive, Loader2 } from "lucide-react";

type BackupStatus = {
  running: boolean;
  latest: { filename: string; bytes: number; updated_at: number; uploaded: boolean } | null;
};

export function ControllerBackup() {
  const [status, setStatus] = useState<BackupStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const refresh = useCallback(async () => {
    try {
      const response = await fetch("/api/controller/backup", { cache: "no-store" });
      const data = await response.json();
      if (!response.ok) throw new Error(data.error || "Status backup belum tersedia.");
      setStatus(data); setError("");
    } catch (err) { setError(err instanceof Error ? err.message : "Status backup belum tersedia."); }
  }, []);
  useEffect(() => {
    void refresh();
    const timer = setInterval(() => void refresh(), 5000);
    return () => clearInterval(timer);
  }, [refresh]);

  async function create() {
    setBusy(true); setError("");
    try {
      const response = await fetch("/api/controller/backup", { method: "POST" });
      const data = await response.json();
      if (!response.ok || !data.success) throw new Error(data.error || data.message || "Backup gagal dimulai.");
      setStatus(previous => ({ running: true, latest: previous?.latest || null }));
      await refresh();
    } catch (err) { setError(err instanceof Error ? err.message : "Backup gagal dimulai."); }
    finally { setBusy(false); }
  }

  const running = busy || status?.running;
  return (
    <section className="mb-4 flex flex-wrap items-center justify-between gap-3 rounded-xl border border-gray-800 bg-gray-900 px-4 py-3">
      <div className="min-w-0 text-sm">
        <p className="font-medium text-white">Backup database <span className="ml-2 text-xs font-normal text-gray-400">Manual</span></p>
        <p className="mt-1 text-xs text-gray-400" role="status">
          {running ? "Backup sedang dibuat… Halaman ini boleh ditutup." : status?.latest ? `${new Date(status.latest.updated_at * 1000).toLocaleString()} · ${(status.latest.bytes / 1e9).toFixed(2)} GB · ${status.latest.uploaded ? "Tersimpan di Drive" : "Menunggu upload ke Drive"}` : "Backup hanya dibuat saat tombol ditekan."}
        </p>
        {error && <p className="mt-1 text-xs text-red-400" role="alert">{error}</p>}
      </div>
      <button type="button" onClick={() => void create()} disabled={!!running || !status} className="flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-500 disabled:cursor-not-allowed disabled:opacity-50">
        {running ? <Loader2 className="h-4 w-4 animate-spin" /> : <Archive className="h-4 w-4" />}
        {running ? "Membuat backup…" : "Backup sekarang"}
      </button>
    </section>
  );
}
