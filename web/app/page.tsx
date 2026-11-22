"use client";
import { useEffect, useState } from "react";

type Site = { url: string; host: string };
type Observation = { source: string; imported_at: string; evidence: string[] };

export default function Page() {
  const [stats, setStats] = useState<{ urls: number; imports: number; wordpress: number } | null>(null);
  const [sites, setSites] = useState<Site[]>([]);
  const [host, setHost] = useState("");
  const [filter, setFilter] = useState("");
  const [wordpress, setWordpress] = useState(false);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(25);
  const [jumpPage, setJumpPage] = useState("");
  const [more, setMore] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [refresh, setRefresh] = useState(0);
  const [selected, setSelected] = useState("");
  const [observations, setObservations] = useState<Observation[]>([]);

  // Fetch metrics on mount and when manually clicking Refresh
  useEffect(() => {
    const abort = new AbortController();
    async function loadStats() {
      try {
        const res = await fetch("/api/catalog/stats", { signal: abort.signal });
        if (res.ok) {
          const data = await res.json();
          setStats(data);
        }
      } catch {}
    }
    void loadStats();
    return () => abort.abort();
  }, [refresh]);

  // Fetch sites when page, filter, pageSize, or refresh changes
  useEffect(() => {
    const abort = new AbortController();
    setBusy(true);
    setError("");
    async function load() {
      try {
        const query = new URLSearchParams({
          host: filter,
          page: String(page),
          limit: String(pageSize),
          wordpress: wordpress ? "1" : "0",
        });
        const res = await fetch("/api/catalog/sites?" + query, { signal: abort.signal });
        const val = await res.json();
        if (!res.ok) throw Error(val.error);
        setSites(val.items);
        setMore(val.has_more);
      } catch (e) {
        if (!abort.signal.aborted) setError(e instanceof Error ? e.message : "Request failed");
      } finally {
        if (!abort.signal.aborted) setBusy(false);
      }
    }
    void load();
    return () => abort.abort();
  }, [filter, page, wordpress, pageSize, refresh]);

  // Fetch observation details for selected URL
  useEffect(() => {
    if (!selected) return;
    const abort = new AbortController();
    setObservations([]);
    async function load() {
      try {
        const res = await fetch("/api/catalog/observations?" + new URLSearchParams({ url: selected }), {
          signal: abort.signal,
        });
        const val = await res.json();
        if (!res.ok) throw Error(val.error);
        setObservations(val.items);
      } catch (e) {
        if (!abort.signal.aborted) setError(e instanceof Error ? e.message : "Request failed");
      }
    }
    void load();
    return () => abort.abort();
  }, [selected]);

  const totalPages = stats?.urls && !filter && !wordpress ? Math.ceil(stats.urls / pageSize) : null;

  const handleJump = (e: React.FormEvent) => {
    e.preventDefault();
    const p = parseInt(jumpPage, 10);
    if (!isNaN(p) && p >= 1 && (!totalPages || p <= totalPages)) {
      setPage(p);
      setJumpPage("");
    }
  };

  const handleClear = () => {
    setHost("");
    setFilter("");
    setPage(1);
  };

  return (
    <>
      <header>
        <h1>Internet Map</h1>
        <span>Dataset intelligence</span>
      </header>
      <main>
        <div className="controls">
          <h2 style={{ flex: 1 }}>Catalog</h2>
          <button disabled={busy} onClick={() => setRefresh((v) => v + 1)}>
            Refresh
          </button>
        </div>

        {error && <p role="alert">{error}</p>}

        <div id="metrics">
          {[
            ["URLs", stats?.urls],
            ["Dataset imports", stats?.imports],
            ["WordPress evidence", stats?.wordpress],
          ].map(([label, value]) => (
            <div key={String(label)}>
              <strong>{typeof value === "number" ? value.toLocaleString() : "—"}</strong>
              {label}
            </div>
          ))}
        </div>

        <form
          onSubmit={(e) => {
            e.preventDefault();
            setPage(1);
            setFilter(host);
          }}
        >
          <input
            type="search"
            aria-label="Search hostname"
            value={host}
            onChange={(e) => setHost(e.target.value)}
            placeholder="Search hostname (e.g. wikipedia, archive)"
          />
          {host && (
            <button type="button" onClick={handleClear} title="Clear search">
              ✕
            </button>
          )}
          <label>
            <input
              type="checkbox"
              checked={wordpress}
              onChange={(e) => {
                setWordpress(e.target.checked);
                setPage(1);
              }}
            />
            WordPress evidence
          </label>
          <button className="primary" disabled={busy}>
            Search
          </button>
        </form>

        <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginTop: 16 }}>
          <p role="status" aria-live="polite" style={{ margin: 0 }}>
            {busy ? "Loading catalog…" : !error ? `${sites.length} URLs shown (page ${page})` : ""}
          </p>
          <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
            <label style={{ fontSize: 13, color: "#93a8b9" }}>Rows per page:</label>
            <select
              value={pageSize}
              onChange={(e) => {
                setPageSize(Number(e.target.value));
                setPage(1);
              }}
              style={{
                background: "#131e29",
                color: "#e4edf5",
                border: "1px solid #354655",
                borderRadius: 7,
                padding: "6px 10px",
                font: "inherit",
              }}
            >
              <option value={25}>25</option>
              <option value={50}>50</option>
              <option value={100}>100</option>
            </select>
          </div>
        </div>

        <table>
          <thead>
            <tr>
              <th>URL</th>
              <th>Hostname</th>
              <th style={{ width: 80, textAlign: "center" }}>Link</th>
            </tr>
          </thead>
          <tbody>
            {sites.map((site) => (
              <tr key={site.url}>
                <td>
                  <button onClick={() => setSelected(site.url)} title="View observations">
                    {site.url}
                  </button>
                </td>
                <td>{site.host}</td>
                <td style={{ textAlign: "center" }}>
                  <a
                    href={site.url}
                    target="_blank"
                    rel="noopener noreferrer"
                    style={{ color: "#81acb9", textDecoration: "none", fontSize: 14 }}
                    title="Open in new tab"
                  >
                    ↗
                  </a>
                </td>
              </tr>
            ))}
            {!busy && !error && !sites.length && (
              <tr>
                <td colSpan={3}>No matching URLs found.</td>
              </tr>
            )}
          </tbody>
        </table>

        <footer>
          <button disabled={busy || page === 1} onClick={() => setPage(1)}>
            « First
          </button>
          <button disabled={busy || page === 1} onClick={() => setPage((v) => Math.max(1, v - 1))}>
            Previous
          </button>
          {page > 10 && (
            <button disabled={busy} onClick={() => setPage((v) => Math.max(1, v - 10))}>
              -10
            </button>
          )}
          <span>
            Page {page}
            {totalPages ? ` of ${totalPages.toLocaleString()}` : ""}
          </span>
          <button disabled={busy || !more} onClick={() => setPage((v) => v + 1)}>
            Next
          </button>
          {more && (!totalPages || page + 10 <= totalPages) && (
            <button disabled={busy} onClick={() => setPage((v) => v + 10)}>
              +10
            </button>
          )}
          <form onSubmit={handleJump} style={{ display: "inline-flex", gap: 6, margin: 0 }}>
            <input
              type="number"
              min={1}
              max={totalPages || 10000000}
              placeholder="Page #"
              value={jumpPage}
              onChange={(e) => setJumpPage(e.target.value)}
              style={{ width: 85, padding: "6px 8px" }}
            />
            <button type="submit" disabled={busy || !jumpPage}>
              Go
            </button>
          </form>
        </footer>

        {selected && (
          <aside aria-label="Source observations">
            <button onClick={() => setSelected("")} style={{ float: "right" }}>
              Close
            </button>
            <h2>{selected}</h2>
            {observations.map((o, i) => (
              <article key={i}>
                <p>
                  <strong>Source:</strong> {o.source}
                </p>
                <small>Imported: {new Date(o.imported_at).toLocaleString()}</small>
                <div style={{ marginTop: 8 }}>
                  {o.evidence.length > 0 ? (
                    o.evidence.map((ev, ei) => (
                      <span
                        key={ei}
                        style={{
                          display: "inline-block",
                          background: "#1e3040",
                          color: "#78dccc",
                          padding: "2px 8px",
                          borderRadius: 4,
                          fontSize: 12,
                          marginRight: 6,
                          marginBottom: 4,
                        }}
                      >
                        {ev}
                      </span>
                    ))
                  ) : (
                    <p style={{ color: "#9bafbf", margin: "4px 0" }}>No WordPress evidence recorded</p>
                  )}
                </div>
              </article>
            ))}
            {!observations.length && <p>No observations recorded.</p>}
          </aside>
        )}
      </main>
    </>
  );
}
