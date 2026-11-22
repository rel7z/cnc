"use client";
import React, { useEffect, useState, useCallback } from "react";

type Site = { url: string; host: string };
type Observation = { source: string; imported_at: string; evidence: string[] };

type IntelSnippetLine = { line_number: number; content: string; is_match: boolean };
type IntelSnippet = { line_number: number; matched_line: string; context: IntelSnippetLine[] };
type IntelResultItem = {
  url: string;
  host: string;
  title: string;
  status_code: number;
  content_length: number;
  scanned_at: string;
  signatures: string[];
  match_count: number;
  snippets: IntelSnippet[];
};

type IntelSourceData = {
  url: string;
  host: string;
  title: string;
  status_code: number;
  scanned_at: string;
  signatures: string[];
  html: string;
  line_count: number;
};

type SignatureStat = { name: string; count: number };

type CrawlerRecentItem = {
  url: string;
  host: string;
  status_code: number;
  title: string;
  signatures: string[];
  content_length: number;
  scanned_at: string;
};

type CrawlerStatus = {
  running: boolean;
  pid?: number;
  scanned: number;
  html_indexed?: number;
  wordpress_found: number;
  speed: number;
  last_url?: string;
};

export default function Page() {
  const [activeTab, setActiveTab] = useState<"intel" | "scanner" | "catalog">("intel");

  // Global / Header Stats
  const [stats, setStats] = useState<{ urls: number; imports: number; wordpress: number } | null>(null);
  const [intelStats, setIntelStats] = useState<{ total_pages: number; total_bytes: number } | null>(null);
  const [crawlerStatus, setCrawlerStatus] = useState<CrawlerStatus>({
    running: false,
    scanned: 0,
    html_indexed: 0,
    wordpress_found: 0,
    speed: 0,
  });

  // Tab 1: Intel (PublicWWW) State
  const [intelQuery, setIntelQuery] = useState("");
  const [intelInput, setIntelInput] = useState("gtag('config'");
  const [selectedSignature, setSelectedSignature] = useState("");
  const [signatureList, setSignatureList] = useState<SignatureStat[]>([]);
  const [intelPage, setIntelPage] = useState(1);
  const [intelResults, setIntelResults] = useState<IntelResultItem[]>([]);
  const [intelTotal, setIntelTotal] = useState(0);
  const [intelHasMore, setIntelHasMore] = useState(false);
  const [intelBusy, setIntelBusy] = useState(false);
  const [intelError, setIntelError] = useState("");

  // Full Source View Modal
  const [viewSourceUrl, setViewSourceUrl] = useState("");
  const [sourceData, setSourceData] = useState<IntelSourceData | null>(null);
  const [sourceLoading, setSourceLoading] = useState(false);
  const [sourceCopied, setSourceCopied] = useState(false);

  // Tab 2: Live Scanner & Crawler State
  const [scanTargetUrl, setScanTargetUrl] = useState("");
  const [scanResult, setScanResult] = useState<{
    url: string;
    host: string;
    title: string;
    status_code: number;
    content_length: number;
    signatures: string[];
  } | null>(null);
  const [scanBusy, setScanBusy] = useState(false);
  const [scanError, setScanError] = useState("");

  // Crawler Controls State
  const [crawlerConcurrency, setCrawlerConcurrency] = useState(500);
  const [crawlerLimit, setCrawlerLimit] = useState(0);
  const [crawlerActionBusy, setCrawlerActionBusy] = useState(false);
  const [recentFeed, setRecentFeed] = useState<CrawlerRecentItem[]>([]);

  // Tab 3: Catalog State
  const [sites, setSites] = useState<Site[]>([]);
  const [host, setHost] = useState("");
  const [filter, setFilter] = useState("");
  const [wordpress, setWordpress] = useState(false);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(25);
  const [jumpPage, setJumpPage] = useState("");
  const [more, setMore] = useState(false);
  const [busy, setBusy] = useState(false);
  const [catalogError, setCatalogError] = useState("");
  const [catalogRefresh, setCatalogRefresh] = useState(0);
  const [selectedObsUrl, setSelectedObsUrl] = useState("");
  const [observations, setObservations] = useState<Observation[]>([]);

  // Load General Stats & Crawler Status
  const loadGeneralStats = useCallback(async () => {
    try {
      const res = await fetch("/api/catalog/stats?extended=1");
      if (res.ok) {
        const data = await res.json();
        setStats({ urls: data.urls, imports: data.imports, wordpress: data.wordpress });
        if (data.scanner) {
          setCrawlerStatus(data.scanner);
        }
      }
      const iRes = await fetch("/api/catalog/intel-stats");
      if (iRes.ok) {
        setIntelStats(await iRes.json());
      }
    } catch {}
  }, []);

  useEffect(() => {
    void loadGeneralStats();
  }, [loadGeneralStats, catalogRefresh]);

  // Load Signatures List
  useEffect(() => {
    async function loadSignatures() {
      try {
        const res = await fetch("/api/catalog/intel-signatures");
        if (res.ok) {
          const data = await res.json();
          setSignatureList(data.signatures || []);
        }
      } catch {}
    }
    void loadSignatures();
  }, [activeTab]);

  // Polling for Crawler Updates and Recent Feed
  useEffect(() => {
    let interval: NodeJS.Timeout | null = null;
    const fetchCrawlerData = async () => {
      try {
        const stRes = await fetch("/api/catalog/scanner");
        if (stRes.ok) {
          const st: CrawlerStatus = await stRes.json();
          setCrawlerStatus(st);
        }
        const recRes = await fetch("/api/catalog/scanner-recent");
        if (recRes.ok) {
          const rec = await recRes.json();
          setRecentFeed(rec.recent || []);
        }
        // Also refresh intel stats
        const iRes = await fetch("/api/catalog/intel-stats");
        if (iRes.ok) {
          setIntelStats(await iRes.json());
        }
      } catch {}
    };

    if (crawlerStatus.running || activeTab === "scanner") {
      void fetchCrawlerData();
      interval = setInterval(fetchCrawlerData, 2000);
    }

    return () => {
      if (interval) clearInterval(interval);
    };
  }, [crawlerStatus.running, activeTab]);

  // Crawler Start / Stop handlers
  const handleStartCrawler = async () => {
    setCrawlerActionBusy(true);
    try {
      const res = await fetch("/api/catalog/scanner-start", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          action: "start",
          concurrency: crawlerConcurrency,
          limit: crawlerLimit,
        }),
      });
      if (res.ok) {
        void loadGeneralStats();
      }
    } catch {}
    finally {
      setCrawlerActionBusy(false);
    }
  };

  const handleStopCrawler = async () => {
    setCrawlerActionBusy(true);
    try {
      const res = await fetch("/api/catalog/scanner-stop", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ action: "stop" }),
      });
      if (res.ok) {
        void loadGeneralStats();
      }
    } catch {}
    finally {
      setCrawlerActionBusy(false);
    }
  };

  // Search Intel
  useEffect(() => {
    if (activeTab !== "intel" || (!intelQuery && !selectedSignature)) {
      setIntelResults([]);
      setIntelTotal(0);
      return;
    }
    const abort = new AbortController();
    setIntelBusy(true);
    setIntelError("");
    async function search() {
      try {
        const params = new URLSearchParams({
          q: intelQuery,
          signature: selectedSignature,
          page: String(intelPage),
          limit: "25",
        });
        const res = await fetch("/api/catalog/intel-search?" + params, { signal: abort.signal });
        const data = await res.json();
        if (!res.ok) throw Error(data.error || "Search failed");
        setIntelResults(data.items || []);
        setIntelTotal(data.total || 0);
        setIntelHasMore(Boolean(data.has_more));
      } catch (err) {
        if (!abort.signal.aborted) {
          setIntelError(err instanceof Error ? err.message : "Search failed");
        }
      } finally {
        if (!abort.signal.aborted) setIntelBusy(false);
      }
    }
    void search();
    return () => abort.abort();
  }, [intelQuery, selectedSignature, intelPage, activeTab]);

  // Load Source View Data
  useEffect(() => {
    if (!viewSourceUrl) {
      setSourceData(null);
      return;
    }
    const abort = new AbortController();
    setSourceLoading(true);
    async function loadSource() {
      try {
        const res = await fetch("/api/catalog/intel-source?" + new URLSearchParams({ url: viewSourceUrl }), {
          signal: abort.signal,
        });
        const data = await res.json();
        if (!res.ok) throw Error(data.error || "Failed to load source");
        setSourceData(data);
      } catch {
        setSourceData(null);
      } finally {
        setSourceLoading(false);
      }
    }
    void loadSource();
    return () => abort.abort();
  }, [viewSourceUrl]);

  // Load Catalog Sites
  useEffect(() => {
    if (activeTab !== "catalog") return;
    const abort = new AbortController();
    setBusy(true);
    setCatalogError("");
    async function load() {
      try {
        const q = new URLSearchParams({
          host: filter,
          page: String(page),
          limit: String(pageSize),
          wordpress: wordpress ? "1" : "0",
        });
        const res = await fetch("/api/catalog/sites?" + q, { signal: abort.signal });
        const val = await res.json();
        if (!res.ok) throw Error(val.error);
        setSites(val.items);
        setMore(val.has_more);
      } catch (e) {
        if (!abort.signal.aborted) setCatalogError(e instanceof Error ? e.message : "Request failed");
      } finally {
        if (!abort.signal.aborted) setBusy(false);
      }
    }
    void load();
    return () => abort.abort();
  }, [filter, page, wordpress, pageSize, catalogRefresh, activeTab]);

  // Load Observations for a URL
  useEffect(() => {
    if (!selectedObsUrl) return;
    const abort = new AbortController();
    setObservations([]);
    async function load() {
      try {
        const res = await fetch("/api/catalog/observations?" + new URLSearchParams({ url: selectedObsUrl }), {
          signal: abort.signal,
        });
        const val = await res.json();
        if (!res.ok) throw Error(val.error);
        setObservations(val.items);
      } catch (e) {
        if (!abort.signal.aborted) setCatalogError(e instanceof Error ? e.message : "Request failed");
      }
    }
    void load();
    return () => abort.abort();
  }, [selectedObsUrl]);

  // Handle On-Demand Live Scan
  const handleLiveScan = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!scanTargetUrl) return;
    setScanBusy(true);
    setScanError("");
    setScanResult(null);
    try {
      const res = await fetch("/api/catalog/intel-scan", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ url: scanTargetUrl }),
      });
      const data = await res.json();
      if (!res.ok) throw Error(data.error || "Scan failed");
      setScanResult(data.item);
      void loadGeneralStats();
    } catch (err) {
      setScanError(err instanceof Error ? err.message : "Scan failed");
    } finally {
      setScanBusy(false);
    }
  };

  const handleIntelPreset = (preset: string) => {
    setIntelInput(preset);
    setIntelQuery(preset);
    setIntelPage(1);
  };

  // Export search results
  const handleExportIntel = () => {
    if (!intelResults.length) return;
    const urls = intelResults.map((r) => r.url).join("\n");
    const blob = new Blob([urls], { type: "text/plain;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `intel-${(intelQuery || selectedSignature || "results").replace(/[^a-zA-Z0-9]/g, "-")}.txt`;
    a.click();
    URL.revokeObjectURL(url);
  };

  // Export catalog
  const handleExportCatalog = async () => {
    try {
      const q = new URLSearchParams({
        host: filter,
        wordpress: wordpress ? "1" : "0",
      });
      const res = await fetch("/api/catalog/export?" + q);
      if (res.ok) {
        const data = await res.json();
        const urls = (data.urls || []).join("\n");
        const blob = new Blob([urls], { type: "text/plain;charset=utf-8" });
        const url = URL.createObjectURL(blob);
        const a = document.createElement("a");
        a.href = url;
        a.download = `catalog-export-${new Date().toISOString().slice(0, 10)}.txt`;
        a.click();
        URL.revokeObjectURL(url);
      }
    } catch {}
  };

  const highlightMatches = (text: string, query: string) => {
    if (!query) return text;
    const parts = [];
    const lower = text.toLowerCase();
    const qLower = query.toLowerCase();
    let idx = 0;
    let matchIdx = lower.indexOf(qLower, idx);
    while (matchIdx !== -1) {
      if (matchIdx > idx) {
        parts.push(text.slice(idx, matchIdx));
      }
      parts.push(
        <mark key={matchIdx} className="code-match-highlight">
          {text.slice(matchIdx, matchIdx + query.length)}
        </mark>
      );
      idx = matchIdx + query.length;
      matchIdx = lower.indexOf(qLower, idx);
    }
    if (idx < text.length) {
      parts.push(text.slice(idx));
    }
    return parts;
  };

  const catalogTotalPages = stats?.urls && !filter && !wordpress ? Math.ceil(stats.urls / pageSize) : null;

  return (
    <>
      <header>
        <div>
          <h1>Internet Map</h1>
          <span>PublicWWW HTML Source Intelligence & 12.25M .org Catalog</span>
        </div>
        <div style={{ display: "flex", gap: 18, alignItems: "center", flexWrap: "wrap" }}>
          {crawlerStatus.running ? (
            <span className="pulse-indicator">
              <span className="pulse-dot"></span>
              Crawler Active ({crawlerStatus.speed} req/s)
            </span>
          ) : (
            <span style={{ color: "#70899e", fontSize: 13 }}>⏸ Crawler Idle</span>
          )}
          {intelStats && (
            <span style={{ color: "#78dccc", fontSize: 13 }}>
              ⚡ <strong>{intelStats.total_pages.toLocaleString()}</strong> HTML Pages Indexed (
              {(intelStats.total_bytes / (1024 * 1024)).toFixed(1)} MB)
            </span>
          )}
        </div>
      </header>

      <main>
        {/* Navigation Tabs */}
        <div className="tabs">
          <button
            className={`tab-btn ${activeTab === "intel" ? "active" : ""}`}
            onClick={() => setActiveTab("intel")}
          >
            ⚡ HTML Source Intel (PublicWWW)
          </button>
          <button
            className={`tab-btn ${activeTab === "scanner" ? "active" : ""}`}
            onClick={() => setActiveTab("scanner")}
          >
            🔍 Live Scanner & Crawler Control
          </button>
          <button
            className={`tab-btn ${activeTab === "catalog" ? "active" : ""}`}
            onClick={() => setActiveTab("catalog")}
          >
            🌐 Catalog Domains ({stats?.urls ? stats.urls.toLocaleString() : "12.2M"})
          </button>
        </div>

        {/* TAB 1: HTML SOURCE INTEL (PUBLICWWW STYLE) */}
        {activeTab === "intel" && (
          <section>
            <div style={{ marginBottom: 18 }}>
              <h2 style={{ fontSize: 20, marginBottom: 6 }}>Source Code Intelligence Engine</h2>
              <p style={{ color: "#93a8b9", margin: 0, fontSize: 14 }}>
                Search through indexed HTML source code for tracking codes, JavaScript snippets, AdSense, CMS tags, or specific text phrases.
              </p>
            </div>

            {/* Search Input and Signature Filter */}
            <form
              onSubmit={(e) => {
                e.preventDefault();
                setIntelPage(1);
                setIntelQuery(intelInput.trim());
              }}
              style={{ display: "flex", gap: 10, flexWrap: "wrap", alignItems: "center" }}
            >
              <input
                type="search"
                value={intelInput}
                onChange={(e) => setIntelInput(e.target.value)}
                placeholder="Search HTML source (e.g. gtag, adsbygoogle, wp-content, UA-1234, turnstile, meta generator)..."
                style={{ fontSize: 15, padding: "12px 16px", flex: "1 1 300px" }}
              />
              <select
                value={selectedSignature}
                onChange={(e) => {
                  setSelectedSignature(e.target.value);
                  setIntelPage(1);
                }}
                style={{ padding: "12px 14px", minWidth: 200, fontSize: 14 }}
              >
                <option value="">All Technologies ({signatureList.length})</option>
                {signatureList.map((sig) => (
                  <option key={sig.name} value={sig.name}>
                    {sig.name} ({sig.count.toLocaleString()})
                  </option>
                ))}
              </select>
              <button className="primary" style={{ padding: "12px 24px" }} disabled={intelBusy}>
                {intelBusy ? "Searching..." : "Search Code"}
              </button>
            </form>

            {/* PublicWWW Preset Chips */}
            <div className="chips-container">
              <span style={{ fontSize: 12, color: "#70899e", marginRight: 4 }}>Example queries:</span>
              {[
                "gtag('config'",
                "adsbygoogle",
                "wp-content",
                "challenges.cloudflare.com/turnstile",
                "Shopify.theme",
                "UA-",
                "fbq('init'",
                'meta name="generator"',
                "tailwindcss",
                "js.stripe.com",
              ].map((chip) => (
                <button key={chip} type="button" className="chip" onClick={() => handleIntelPreset(chip)}>
                  {chip}
                </button>
              ))}
            </div>

            {intelError && <p role="alert" style={{ color: "#e74c3c" }}>{intelError}</p>}

            {/* Results Header with Export */}
            {(intelQuery || selectedSignature) && !intelBusy && (
              <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", margin: "16px 0", flexWrap: "wrap", gap: 10 }}>
                <p style={{ margin: 0, color: "#8ab4cc", fontSize: 14 }}>
                  Found <strong>{intelTotal.toLocaleString()}</strong> website{intelTotal === 1 ? "" : "s"}
                  {intelQuery ? <> containing code matching &ldquo;<strong>{intelQuery}</strong>&rdquo;</> : ""}
                  {selectedSignature ? <> with technology &ldquo;<strong>{selectedSignature}</strong>&rdquo;</> : ""}
                </p>
                <div style={{ display: "flex", gap: 10, alignItems: "center" }}>
                  {intelResults.length > 0 && (
                    <button onClick={handleExportIntel} style={{ fontSize: 12, padding: "5px 12px" }}>
                      ⬇ Export URLs
                    </button>
                  )}
                  <span style={{ fontSize: 13, color: "#6a8296" }}>Page {intelPage}</span>
                </div>
              </div>
            )}

            {intelBusy && <p style={{ color: "#78dccc", padding: "20px 0" }}>Searching HTML source database…</p>}

            {/* Results Cards (PublicWWW Style) */}
            <div style={{ marginTop: 12 }}>
              {intelResults.map((item) => (
                <div key={item.url} className="intel-card">
                  <div className="intel-card-header">
                    <div>
                      <h3 className="intel-title">{item.title || item.host}</h3>
                      <div style={{ display: "flex", gap: 10, alignItems: "center", flexWrap: "wrap" }}>
                        <a href={item.url} target="_blank" rel="noopener noreferrer" className="intel-url">
                          {item.url} ↗
                        </a>
                        <span className="badge badge-status">HTTP {item.status_code}</span>
                        <span style={{ fontSize: 12, color: "#70899e" }}>
                          {(item.content_length / 1024).toFixed(1)} KB · Scanned {new Date(item.scanned_at).toLocaleDateString()}
                        </span>
                      </div>
                    </div>
                    <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
                      <span className="badge badge-count">{item.match_count} match{item.match_count === 1 ? "" : "es"}</span>
                      <button
                        onClick={() => setViewSourceUrl(item.url)}
                        style={{ padding: "6px 12px", fontSize: 12 }}
                      >
                        View Source
                      </button>
                    </div>
                  </div>

                  {/* Technology Badges */}
                  {item.signatures.length > 0 && (
                    <div style={{ display: "flex", gap: 6, flexWrap: "wrap", margin: "8px 0" }}>
                      {item.signatures.map((sig) => (
                        <span key={sig} className="badge badge-tech">
                          {sig}
                        </span>
                      ))}
                    </div>
                  )}

                  {/* PublicWWW Code Snippet Box */}
                  {item.snippets.map((snip, sIdx) => (
                    <div key={sIdx} className="code-box">
                      {snip.context.map((line, lIdx) => (
                        <div
                          key={lIdx}
                          className={`code-line ${line.is_match ? "match-line" : ""}`}
                        >
                          <span className="line-no">{line.line_number}</span>
                          <span className="line-content">
                            {line.is_match && intelQuery ? highlightMatches(line.content, intelQuery) : line.content}
                          </span>
                        </div>
                      ))}
                    </div>
                  ))}
                </div>
              ))}

              {!intelBusy && (intelQuery || selectedSignature) && intelResults.length === 0 && (
                <div style={{ padding: 40, textAlign: "center", background: "#111a24", borderRadius: 10, border: "1px dashed #243547" }}>
                  <p style={{ color: "#8ab4cc", fontSize: 16 }}>No matching HTML source found.</p>
                  <p style={{ color: "#546e7a", fontSize: 13, marginTop: 6 }}>
                    Tip: Use the &ldquo;Live Scanner & Crawler Control&rdquo; tab to fetch and index new domains into the intelligence database!
                  </p>
                </div>
              )}
            </div>

            {/* Pagination for Intel Results */}
            {intelTotal > 25 && (
              <footer>
                <button disabled={intelBusy || intelPage === 1} onClick={() => setIntelPage(1)}>
                  « First
                </button>
                <button
                  disabled={intelBusy || intelPage === 1}
                  onClick={() => setIntelPage((v) => Math.max(1, v - 1))}
                >
                  Previous
                </button>
                <span>Page {intelPage} of {Math.ceil(intelTotal / 25)}</span>
                <button disabled={intelBusy || !intelHasMore} onClick={() => setIntelPage((v) => v + 1)}>
                  Next »
                </button>
              </footer>
            )}
          </section>
        )}

        {/* TAB 2: LIVE SCANNER & CRAWLER CONTROL */}
        {activeTab === "scanner" && (
          <section>
            <div style={{ marginBottom: 20 }}>
              <h2 style={{ fontSize: 20, marginBottom: 6 }}>Live HTML Scanner & Catalog Crawler Control</h2>
              <p style={{ color: "#93a8b9", margin: 0, fontSize: 14 }}>
                Control the high-throughput asynchronous catalog crawler or inspect any URL live to extract technology forensics.
              </p>
            </div>

            {/* On-Demand URL Inspector */}
            <div className="crawler-panel">
              <h3 style={{ fontSize: 16, marginTop: 0, marginBottom: 12 }}>⚡ On-Demand URL Inspector</h3>
              <form onSubmit={handleLiveScan}>
                <input
                  type="text"
                  value={scanTargetUrl}
                  onChange={(e) => setScanTargetUrl(e.target.value)}
                  placeholder="Enter target URL (e.g. https://eff.org/, https://wikipedia.org/, https://wordpress.org/)"
                  style={{ fontSize: 14 }}
                />
                <button className="primary" disabled={scanBusy || !scanTargetUrl}>
                  {scanBusy ? "Fetching & Indexing…" : "Scan & Index"}
                </button>
              </form>

              {scanError && <p role="alert" style={{ color: "#e74c3c", marginTop: 12 }}>{scanError}</p>}

              {scanResult && (
                <div style={{ marginTop: 20, padding: 16, background: "#0c1219", borderRadius: 8, border: "1px solid #1c2b3a" }}>
                  <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
                    <h4 style={{ margin: 0, color: "#6ce0c7" }}>✓ Successfully Indexed into Intel</h4>
                    <span className="badge badge-status">HTTP {scanResult.status_code}</span>
                  </div>
                  <table style={{ marginTop: 12 }}>
                    <tbody>
                      <tr>
                        <td style={{ width: 140, color: "#70899e" }}>URL</td>
                        <td><a href={scanResult.url} target="_blank" rel="noopener noreferrer" style={{ color: "#78dccc" }}>{scanResult.url} ↗</a></td>
                      </tr>
                      <tr>
                        <td style={{ color: "#70899e" }}>Page Title</td>
                        <td><strong>{scanResult.title || "—"}</strong></td>
                      </tr>
                      <tr>
                        <td style={{ color: "#70899e" }}>HTML Size</td>
                        <td>{(scanResult.content_length / 1024).toFixed(1)} KB</td>
                      </tr>
                      <tr>
                        <td style={{ color: "#70899e" }}>Detected Tech</td>
                        <td>
                          {scanResult.signatures?.length > 0 ? (
                            scanResult.signatures.map((s: string) => (
                              <span key={s} className="badge badge-tech" style={{ marginRight: 6 }}>{s}</span>
                            ))
                          ) : (
                            <span style={{ color: "#6a8296" }}>No common framework signatures detected</span>
                          )}
                        </td>
                      </tr>
                    </tbody>
                  </table>
                  <div style={{ marginTop: 14 }}>
                    <button onClick={() => setViewSourceUrl(scanResult.url)} style={{ fontSize: 13 }}>
                      Inspect Full HTML Source
                    </button>
                  </div>
                </div>
              )}
            </div>

            {/* Background Crawler Operations Panel */}
            <div className="crawler-panel">
              <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 16, flexWrap: "wrap", gap: 10 }}>
                <div>
                  <h3 style={{ fontSize: 17, margin: "0 0 4px" }}>🤖 High-Throughput Catalog Crawler</h3>
                  <span style={{ color: "#8ab4cc", fontSize: 13 }}>
                    Discovers WordPress and indexes full HTML across the 12.25M .org domains.
                  </span>
                </div>
                <div style={{ display: "flex", gap: 12, alignItems: "center" }}>
                  {crawlerStatus.running ? (
                    <button className="danger" onClick={handleStopCrawler} disabled={crawlerActionBusy}>
                      {crawlerActionBusy ? "Stopping..." : "⏹ Stop Crawler"}
                    </button>
                  ) : (
                    <button className="success" onClick={handleStartCrawler} disabled={crawlerActionBusy}>
                      {crawlerActionBusy ? "Starting..." : "▶ Start Crawler"}
                    </button>
                  )}
                </div>
              </div>

              {/* Live Crawler Configuration & Status */}
              <div style={{ display: "flex", gap: 20, alignItems: "center", margin: "16px 0", flexWrap: "wrap", background: "#0c131a", padding: "12px 16px", borderRadius: 8 }}>
                <label style={{ fontSize: 13, color: "#93a8b9" }}>
                  Concurrency:
                  <select
                    value={crawlerConcurrency}
                    onChange={(e) => setCrawlerConcurrency(Number(e.target.value))}
                    disabled={crawlerStatus.running}
                    style={{ marginLeft: 8, padding: "5px 10px", fontSize: 13 }}
                  >
                    <option value={100}>100 Workers</option>
                    <option value={200}>200 Workers</option>
                    <option value={500}>500 Workers (High Speed Recommended)</option>
                    <option value={1000}>1,000 Workers (Turbo)</option>
                  </select>
                </label>

                <label style={{ fontSize: 13, color: "#93a8b9" }}>
                  Limit:
                  <select
                    value={crawlerLimit}
                    onChange={(e) => setCrawlerLimit(Number(e.target.value))}
                    disabled={crawlerStatus.running}
                    style={{ marginLeft: 8, padding: "5px 10px", fontSize: 13 }}
                  >
                    <option value={0}>Continuous (No limit)</option>
                    <option value={500}>500 URLs</option>
                    <option value={2000}>2,000 URLs</option>
                    <option value={10000}>10,000 URLs</option>
                  </select>
                </label>

                {crawlerStatus.pid && crawlerStatus.running && (
                  <span style={{ fontSize: 12, color: "#6ce0c7", marginLeft: "auto" }}>
                    Process PID: <code>{crawlerStatus.pid}</code>
                  </span>
                )}
              </div>

              {/* Crawler Metrics */}
              <div id="metrics" style={{ margin: "20px 0" }}>
                <div>
                  <strong>{crawlerStatus.scanned.toLocaleString()}</strong>
                  URLs Scanned in Session
                </div>
                <div>
                  <strong>{intelStats ? intelStats.total_pages.toLocaleString() : "—"}</strong>
                  Active Pages Indexed into Intel
                </div>
                <div>
                  <strong>{crawlerStatus.wordpress_found.toLocaleString()}</strong>
                  WordPress Detections
                </div>
                <div>
                  <strong>{crawlerStatus.running ? `${crawlerStatus.speed} req/s` : "Idle"}</strong>
                  Scanning Speed
                </div>
              </div>

              {/* Live Streaming Activity Feed */}
              <div style={{ marginTop: 24 }}>
                <h4 style={{ fontSize: 14, color: "#8ab4cc", marginBottom: 8, display: "flex", justifyContent: "space-between" }}>
                  <span>Live Streaming Activity (Latest Crawled Pages)</span>
                  {crawlerStatus.running && <span className="pulse-indicator"><span className="pulse-dot"></span> Live Feed</span>}
                </h4>

                <table className="feed-table">
                  <thead>
                    <tr>
                      <th style={{ width: 80 }}>Status</th>
                      <th>URL / Title</th>
                      <th>Detected Tech</th>
                      <th style={{ width: 120 }}>Size / Time</th>
                    </tr>
                  </thead>
                  <tbody>
                    {recentFeed.map((item, idx) => (
                      <tr key={idx}>
                        <td>
                          <span className={`badge ${item.status_code === 200 ? "badge-status" : "badge-tech"}`}>
                            {item.status_code}
                          </span>
                        </td>
                        <td>
                          <div style={{ fontWeight: 600, color: "#eef6fc", fontSize: 13 }}>{item.title || item.host}</div>
                          <a href={item.url} target="_blank" rel="noopener noreferrer" style={{ color: "#78dccc", fontSize: 12 }}>
                            {item.url} ↗
                          </a>
                        </td>
                        <td>
                          <div style={{ display: "flex", gap: 4, flexWrap: "wrap" }}>
                            {item.signatures.length > 0 ? (
                              item.signatures.map((s) => (
                                <span key={s} className="badge badge-tech" style={{ fontSize: 10 }}>{s}</span>
                              ))
                            ) : (
                              <span style={{ color: "#546e7a", fontSize: 11 }}>—</span>
                            )}
                          </div>
                        </td>
                        <td style={{ fontSize: 12, color: "#70899e" }}>
                          {(item.content_length / 1024).toFixed(1)} KB<br />
                          {new Date(item.scanned_at).toLocaleTimeString()}
                        </td>
                      </tr>
                    ))}
                    {!recentFeed.length && (
                      <tr>
                        <td colSpan={4} style={{ textAlign: "center", color: "#6a8296", padding: 24 }}>
                          No crawl activity recorded yet. Click &ldquo;Start Crawler&rdquo; or &ldquo;Scan & Index&rdquo; above!
                        </td>
                      </tr>
                    )}
                  </tbody>
                </table>
              </div>
            </div>
          </section>
        )}

        {/* TAB 3: CATALOG DOMAINS */}
        {activeTab === "catalog" && (
          <section>
            <div className="controls">
              <h2 style={{ flex: 1 }}>Catalog Delegations (.org)</h2>
              <div style={{ display: "flex", gap: 8 }}>
                <button onClick={handleExportCatalog} title="Export filtered domains to text file">
                  ⬇ Export Domains
                </button>
                <button disabled={busy} onClick={() => setCatalogRefresh((v) => v + 1)}>
                  Refresh
                </button>
              </div>
            </div>

            {catalogError && <p role="alert">{catalogError}</p>}

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
                placeholder="Search hostname (e.g. wikipedia, archive, eff, mozilla)"
              />
              {host && (
                <button type="button" onClick={() => { setHost(""); setFilter(""); setPage(1); }} title="Clear search">
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
                {busy ? "Loading catalog…" : !catalogError ? `${sites.length} URLs shown (page ${page})` : ""}
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
                  <th style={{ width: 140, textAlign: "center" }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {sites.map((site) => (
                  <tr key={site.url}>
                    <td>
                      <button onClick={() => setSelectedObsUrl(site.url)} title="View observations">
                        {site.url}
                      </button>
                    </td>
                    <td>{site.host}</td>
                    <td style={{ textAlign: "center" }}>
                      <div style={{ display: "flex", gap: 8, justifyContent: "center" }}>
                        <button
                          onClick={() => {
                            setScanTargetUrl(site.url);
                            setActiveTab("scanner");
                          }}
                          style={{ padding: "3px 8px", fontSize: 12, border: "1px solid #283e52", borderRadius: 4 }}
                          title="Scan this site live"
                        >
                          Scan Live
                        </button>
                        <a
                          href={site.url}
                          target="_blank"
                          rel="noopener noreferrer"
                          style={{ color: "#81acb9", textDecoration: "none", fontSize: 14 }}
                          title="Open in new tab"
                        >
                          ↗
                        </a>
                      </div>
                    </td>
                  </tr>
                ))}
                {!busy && !catalogError && !sites.length && (
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
                {catalogTotalPages ? ` of ${catalogTotalPages.toLocaleString()}` : ""}
              </span>
              <button disabled={busy || !more} onClick={() => setPage((v) => v + 1)}>
                Next
              </button>
              {more && (!catalogTotalPages || page + 10 <= catalogTotalPages) && (
                <button disabled={busy} onClick={() => setPage((v) => v + 10)}>
                  +10
                </button>
              )}
              <form
                onSubmit={(e) => {
                  e.preventDefault();
                  const p = parseInt(jumpPage, 10);
                  if (!isNaN(p) && p >= 1 && (!catalogTotalPages || p <= catalogTotalPages)) {
                    setPage(p);
                    setJumpPage("");
                  }
                }}
                style={{ display: "inline-flex", gap: 6, margin: 0 }}
              >
                <input
                  type="number"
                  min={1}
                  max={catalogTotalPages || 10000000}
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

            {/* Observations Drawer */}
            {selectedObsUrl && (
              <aside aria-label="Source observations">
                <button onClick={() => setSelectedObsUrl("")} style={{ float: "right" }}>
                  Close
                </button>
                <h2>{selectedObsUrl}</h2>
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
          </section>
        )}

        {/* FULL SOURCE CODE VIEWER MODAL */}
        {viewSourceUrl && (
          <dialog
            open
            style={{
              position: "fixed",
              top: "50%",
              left: "50%",
              transform: "translate(-50%, -50%)",
              zIndex: 1000,
              boxShadow: "0 20px 40px rgba(0,0,0,0.7)",
            }}
          >
            <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 16 }}>
              <div>
                <h3 style={{ margin: 0, fontSize: 18, color: "#6ce0c7" }}>Source Code Inspector</h3>
                <small style={{ color: "#8ab4cc" }}>{viewSourceUrl}</small>
              </div>
              <div style={{ display: "flex", gap: 10 }}>
                {sourceData && (
                  <button
                    onClick={() => {
                      navigator.clipboard.writeText(sourceData.html);
                      setSourceCopied(true);
                      setTimeout(() => setSourceCopied(false), 2000);
                    }}
                    style={{ fontSize: 13 }}
                  >
                    {sourceCopied ? "✓ Copied!" : "Copy HTML"}
                  </button>
                )}
                <button onClick={() => setViewSourceUrl("")} style={{ fontSize: 13 }}>
                  ✕ Close
                </button>
              </div>
            </div>

            {sourceLoading && <p style={{ color: "#78dccc" }}>Loading HTML source...</p>}

            {sourceData && (
              <>
                <div style={{ display: "flex", gap: 12, alignItems: "center", marginBottom: 12, flexWrap: "wrap", fontSize: 13, color: "#8ab4cc" }}>
                  <span>Lines: <strong>{sourceData.line_count.toLocaleString()}</strong></span>
                  <span>Title: <strong>{sourceData.title || "—"}</strong></span>
                  <span>Status: <strong>HTTP {sourceData.status_code}</strong></span>
                  {sourceData.signatures.map((sig) => (
                    <span key={sig} className="badge badge-tech">{sig}</span>
                  ))}
                </div>
                <div
                  style={{
                    maxHeight: "60vh",
                    overflowY: "auto",
                    background: "#080c10",
                    border: "1px solid #1c2733",
                    borderRadius: 7,
                    padding: 12,
                    fontFamily: "ui-monospace,SFMono-Regular,Consolas,monospace",
                    fontSize: 12,
                    lineHeight: 1.5,
                  }}
                >
                  {sourceData.html.split("\n").map((line, idx) => (
                    <div key={idx} style={{ display: "flex", gap: 12 }}>
                      <span style={{ color: "#495c6e", minWidth: 40, textAlign: "right", userSelect: "none" }}>
                        {idx + 1}
                      </span>
                      <span style={{ color: "#c5d8e8", whiteSpace: "pre-wrap", wordBreak: "break-all" }}>
                        {intelQuery ? highlightMatches(line, intelQuery) : line}
                      </span>
                    </div>
                  ))}
                </div>
              </>
            )}
          </dialog>
        )}
      </main>
    </>
  );
}
