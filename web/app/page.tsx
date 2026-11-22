"use client";
import React, { useEffect, useState, useCallback, useMemo } from "react";

type Site = { url: string; host: string; evidence?: string[] };
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

type ResourceInfo = {
  cgroups: {
    cpu_quota: string;
    cpu_quota_cores: number;
    memory_max_mb: number;
    memory_high_mb: number;
    tasks_max: number;
    cpu_weight: number;
    nice: number;
    active: boolean;
  };
  telemetry: {
    running: boolean;
    pid: number;
    memory_mb: number;
    speed: number;
  };
  config: {
    concurrency: number;
    profile: string;
    cpu_quota?: string;
    memory_max_mb?: number;
  };
};

type ClusterNode = {
  node_id: string;
  ip_address?: string | null;
  status: string;
  speed: number;
  scanned_count: number;
  wp_count: number;
  last_heartbeat?: string | null;
};

type ClusterStatus = {
  nodes: ClusterNode[];
  total_nodes: number;
  active_nodes: number;
  cluster_speed: number;
  cluster_scanned: number;
  cluster_wp: number;
};

const WORKER_SPECS = [
  { id: "cnc-do-worker-1", vpc: "10.104.0.3", region: "SGP1", cores: "4 vCPU", ram: "8 GB" },
  { id: "cnc-do-worker-2", vpc: "10.104.0.4", region: "SGP1", cores: "4 vCPU", ram: "8 GB" },
  { id: "cnc-do-worker-3", vpc: "10.104.0.5", region: "SGP1", cores: "4 vCPU", ram: "8 GB" },
  { id: "cnc-do-worker-4", vpc: "10.104.0.6", region: "SGP1", cores: "4 vCPU", ram: "8 GB" },
  { id: "cnc-do-worker-5", vpc: "10.104.0.7", region: "SGP1", cores: "4 vCPU", ram: "8 GB" },
  { id: "cnc-do-worker-6", vpc: "10.104.0.8", region: "SGP1", cores: "4 vCPU", ram: "8 GB" },
  { id: "cnc-do-worker-7", vpc: "10.104.0.9", region: "SGP1", cores: "4 vCPU", ram: "8 GB" },
  { id: "cnc-do-worker-8", vpc: "10.104.0.10", region: "SGP1", cores: "4 vCPU", ram: "8 GB" },
];

export default function Page() {
  const [activeTab, setActiveTab] = useState<"intel" | "scanner" | "catalog">("intel");

  // Distributed Cluster Swarm State
  const [clusterStatus, setClusterStatus] = useState<ClusterStatus | null>(null);
  const [clusterBusy, setClusterBusy] = useState(false);

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
  const [expandedSnippets, setExpandedSnippets] = useState<Record<string, boolean>>({});
  const [copiedUrl, setCopiedUrl] = useState<string | null>(null);

  // Full Source View Modal
  const [viewSourceUrl, setViewSourceUrl] = useState("");
  const [sourceData, setSourceData] = useState<IntelSourceData | null>(null);
  const [sourceLoading, setSourceLoading] = useState(false);
  const [sourceCopied, setSourceCopied] = useState(false);
  const [sourceSearch, setSourceSearch] = useState("");
  const [wrapSourceLines, setWrapSourceLines] = useState(true);

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

  // Crawler Controls & Resource State
  const [crawlerConcurrency, setCrawlerConcurrency] = useState(600);
  const [crawlerLimit, setCrawlerLimit] = useState(0);
  const [crawlerActionBusy, setCrawlerActionBusy] = useState(false);
  const [recentFeed, setRecentFeed] = useState<CrawlerRecentItem[]>([]);
  const [feedSearch, setFeedSearch] = useState("");
  const [resourceInfo, setResourceInfo] = useState<ResourceInfo | null>(null);
  const [resourceBusy, setResourceBusy] = useState(false);
  const [selectedProfile, setSelectedProfile] = useState<string>("high");

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

  // Keyboard navigation for modal
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape" && viewSourceUrl) {
        setViewSourceUrl("");
      }
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [viewSourceUrl]);

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

  // Load Hardware Resources & Cgroups State
  const loadResources = useCallback(async () => {
    try {
      const res = await fetch("/api/catalog/scanner-resources");
      if (res.ok) {
        const data: ResourceInfo = await res.json();
        setResourceInfo(data);
        if (data.config) {
          if (data.config.concurrency) setCrawlerConcurrency(data.config.concurrency);
          if (data.config.profile) setSelectedProfile(data.config.profile);
        }
      }
    } catch {}
  }, []);

  useEffect(() => {
    void loadGeneralStats();
    void loadResources();
  }, [loadGeneralStats, loadResources, catalogRefresh]);

  const handleApplyProfile = async (profile: string, workers: number) => {
    setResourceBusy(true);
    setSelectedProfile(profile);
    setCrawlerConcurrency(workers);
    try {
      const res = await fetch("/api/catalog/scanner-resources", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ concurrency: workers, profile }),
      });
      if (res.ok) {
        await loadResources();
        await loadGeneralStats();
      }
    } catch {}
    finally {
      setResourceBusy(false);
    }
  };

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
      setSourceSearch("");
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
          with_evidence: "1",
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
  const handleLiveScan = async (e?: React.FormEvent, customUrl?: string) => {
    if (e) e.preventDefault();
    const target = customUrl || scanTargetUrl;
    if (!target) return;
    setScanTargetUrl(target);
    setScanBusy(true);
    setScanError("");
    setScanResult(null);
    try {
      const res = await fetch("/api/catalog/intel-scan", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ url: target }),
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

  const handleCopyUrl = (url: string) => {
    navigator.clipboard.writeText(url);
    setCopiedUrl(url);
    setTimeout(() => setCopiedUrl(null), 2000);
  };

  // Direct API .txt and JSON download from database
  const handleDownloadTxt = (params: Record<string, string>) => {
    const q = new URLSearchParams({ format: "txt", ...params });
    window.location.href = "/api/catalog/export?" + q.toString();
  };

  const handleExportIntel = (format: "txt" | "json" = "txt", field: "url" | "host" = "url") => {
    if (format === "txt") {
      handleDownloadTxt({
        q: intelQuery,
        signature: selectedSignature,
        field,
      });
      return;
    }
    if (!intelResults.length) return;
    const content = JSON.stringify(intelResults, null, 2);
    const blob = new Blob([content], { type: "application/json;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `intel-${(intelQuery || selectedSignature || "results").replace(/[^a-zA-Z0-9]/g, "-")}.json`;
    a.click();
    URL.revokeObjectURL(url);
  };

  // Export catalog directly from database
  const handleExportCatalog = (field: "url" | "host" = "url") => {
    handleDownloadTxt({
      host: filter,
      wordpress: wordpress ? "1" : "0",
      field,
    });
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

  // Filtered recent activity feed
  const filteredFeed = useMemo(() => {
    if (!feedSearch.trim()) return recentFeed;
    const s = feedSearch.toLowerCase();
    return recentFeed.filter(
      (item) => item.url.toLowerCase().includes(s) || item.title?.toLowerCase().includes(s) || item.signatures.some((sig) => sig.toLowerCase().includes(s))
    );
  }, [recentFeed, feedSearch]);

  // Matching lines count in modal viewer
  const modalMatchesCount = useMemo(() => {
    const q = (sourceSearch || intelQuery).trim().toLowerCase();
    if (!sourceData || !q) return 0;
    return sourceData.html.toLowerCase().split(q).length - 1;
  }, [sourceData, sourceSearch, intelQuery]);

  return (
    <>
      <header>
        <div className="brand-container">
          <div className="brand-logo">⚡</div>
          <div>
            <h1>Internet Map</h1>
            <span className="subtitle">PublicWWW HTML Source Intelligence &amp; 12.25M .org Catalog</span>
          </div>
        </div>

        <div className="header-status">
          {crawlerStatus.running ? (
            <span className="status-pill active">
              <span className="pulse-dot"></span>
              Crawler Active ({crawlerStatus.speed} req/s)
            </span>
          ) : (
            <span className="status-pill">⏸ Crawler Idle</span>
          )}
          {intelStats && (
            <span className="status-pill intel">
              ⚡ <strong>{intelStats.total_pages.toLocaleString()}</strong> HTML Pages ({((intelStats.total_bytes || 0) / (1024 * 1024)).toFixed(1)} MB)
            </span>
          )}
          <button
            onClick={() => {
              void loadGeneralStats();
              setCatalogRefresh((v) => v + 1);
            }}
            title="Refresh All Statistics"
            style={{ padding: "6px 10px", fontSize: 13 }}
            className="secondary"
          >
            ↻
          </button>
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
            {intelStats && <span className="tab-badge">{(intelStats.total_pages / 1000).toFixed(0)}k</span>}
          </button>
          <button
            className={`tab-btn ${activeTab === "scanner" ? "active" : ""}`}
            onClick={() => setActiveTab("scanner")}
          >
            🔍 Live Scanner &amp; Crawler Control
            {crawlerStatus.running && <span className="pulse-dot" style={{ width: 6, height: 6 }}></span>}
          </button>
          <button
            className={`tab-btn ${activeTab === "catalog" ? "active" : ""}`}
            onClick={() => setActiveTab("catalog")}
          >
            🌐 Catalog Domains
            {stats?.urls && <span className="tab-badge">{(stats.urls / 1000000).toFixed(1)}M</span>}
          </button>
        </div>

        {/* TAB 1: HTML SOURCE INTEL (PUBLICWWW STYLE) */}
        {activeTab === "intel" && (
          <section>
            <div style={{ marginBottom: 18 }}>
              <h2>Source Code Intelligence Engine</h2>
              <p style={{ color: "#8fa8be", margin: 0, fontSize: 14 }}>
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
              <div style={{ flex: "1 1 340px", display: "flex", position: "relative", alignItems: "center" }}>
                <input
                  type="search"
                  value={intelInput}
                  onChange={(e) => setIntelInput(e.target.value)}
                  placeholder="Search HTML source (e.g. gtag, adsbygoogle, wp-content, UA-1234, turnstile, meta generator)..."
                  style={{ fontSize: 15, padding: "12px 38px 12px 16px", width: "100%" }}
                />
                {intelInput && (
                  <button
                    type="button"
                    onClick={() => {
                      setIntelInput("");
                      setIntelQuery("");
                      setIntelPage(1);
                    }}
                    style={{
                      position: "absolute",
                      right: 10,
                      background: "none",
                      border: "none",
                      color: "#6b8296",
                      padding: 4,
                      fontSize: 16,
                    }}
                    title="Clear input"
                  >
                    ✕
                  </button>
                )}
              </div>

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

              {(intelQuery || selectedSignature) && (
                <button
                  type="button"
                  className="secondary"
                  onClick={() => {
                    setIntelInput("");
                    setIntelQuery("");
                    setSelectedSignature("");
                    setIntelPage(1);
                  }}
                  style={{ padding: "12px 16px" }}
                  title="Reset all filters"
                >
                  Reset
                </button>
              )}
            </form>

            {/* PublicWWW Preset Chips */}
            <div className="chips-container">
              <span style={{ fontSize: 12, color: "#6b8296", marginRight: 4 }}>Example queries:</span>
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
                <button
                  key={chip}
                  type="button"
                  className={`chip ${intelQuery === chip ? "active" : ""}`}
                  onClick={() => handleIntelPreset(chip)}
                >
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
                <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
                  {intelResults.length > 0 && (
                    <>
                      <button onClick={() => handleExportIntel("txt", "url")} style={{ fontSize: 12, padding: "5px 12px" }} title="Download all matching URLs as a plain text file">
                        ⬇ Download URLs (.txt)
                      </button>
                      <button onClick={() => handleExportIntel("txt", "host")} style={{ fontSize: 12, padding: "5px 12px" }} title="Download all matching hostnames as a plain text file">
                        ⬇ Download Hosts (.txt)
                      </button>
                      <button onClick={() => handleExportIntel("json")} style={{ fontSize: 12, padding: "5px 12px" }} title="Export loaded page items as JSON">
                        ⬇ Export JSON
                      </button>
                    </>
                  )}
                  <span style={{ fontSize: 13, color: "#6a8296", marginLeft: 4 }}>Page {intelPage}</span>
                </div>
              </div>
            )}

            {intelBusy && (
              <div style={{ padding: "40px 20px", textAlign: "center", background: "#0f1620", borderRadius: 10, border: "1px solid #1c2b3a" }}>
                <span className="pulse-indicator" style={{ fontSize: 15 }}>
                  <span className="pulse-dot"></span> Searching HTML source database…
                </span>
              </div>
            )}

            {/* Results Cards (PublicWWW Style) */}
            <div style={{ marginTop: 12 }}>
              {intelResults.map((item) => {
                const isExpanded = expandedSnippets[item.url] || false;
                const visibleSnippets = isExpanded ? item.snippets : item.snippets.slice(0, 2);

                return (
                  <div key={item.url} className="intel-card">
                    <div className="intel-card-header">
                      <div>
                        <h3 className="intel-title">{item.title || item.host}</h3>
                        <div style={{ display: "flex", gap: 10, alignItems: "center", flexWrap: "wrap" }}>
                          <a href={item.url} target="_blank" rel="noopener noreferrer" className="intel-url">
                            {item.url} ↗
                          </a>
                          <button
                            type="button"
                            onClick={() => handleCopyUrl(item.url)}
                            style={{ padding: "2px 7px", fontSize: 11, background: "#162330", border: "1px solid #283e54", borderRadius: 4 }}
                            title="Copy URL"
                          >
                            {copiedUrl === item.url ? "✓ Copied" : "Copy"}
                          </button>
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
                          className="secondary"
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
                    {visibleSnippets.map((snip, sIdx) => (
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

                    {/* Expand/Collapse extra snippets */}
                    {item.snippets.length > 2 && (
                      <button
                        type="button"
                        onClick={() =>
                          setExpandedSnippets((prev) => ({
                            ...prev,
                            [item.url]: !isExpanded,
                          }))
                        }
                        style={{ marginTop: 8, fontSize: 12, padding: "4px 10px", background: "none", border: "1px dashed #243547", color: "#78dccc" }}
                      >
                        {isExpanded ? "▲ Show fewer snippets" : `▼ Show all ${item.snippets.length} code snippets`}
                      </button>
                    )}
                  </div>
                );
              })}

              {!intelBusy && (intelQuery || selectedSignature) && intelResults.length === 0 && (
                <div style={{ padding: 40, textAlign: "center", background: "#0f1620", borderRadius: 10, border: "1px dashed #1c2b3a" }}>
                  <p style={{ color: "#8ab4cc", fontSize: 16 }}>No matching HTML source found.</p>
                  <p style={{ color: "#546e7a", fontSize: 13, marginTop: 6 }}>
                    Tip: Use the &ldquo;Live Scanner &amp; Crawler Control&rdquo; tab to fetch and index new domains into the intelligence database!
                  </p>
                </div>
              )}

              {!intelBusy && !intelQuery && !selectedSignature && (
                <div style={{ padding: 50, textAlign: "center", background: "#0f1620", borderRadius: 10, border: "1px solid #1a2838" }}>
                  <div style={{ fontSize: 32, marginBottom: 12 }}>⚡</div>
                  <h3 style={{ fontSize: 18, color: "#f0f6fc", margin: "0 0 8px" }}>PublicWWW HTML Intelligence Search</h3>
                  <p style={{ color: "#8fa8be", maxWidth: 560, margin: "0 auto 20px", fontSize: 14 }}>
                    Type any keyword, tracking ID, or code snippet in the box above, or click one of the preset chips like <code>gtag(&apos;config&apos;</code> or <code>adsbygoogle</code>.
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
              <h2>Live HTML Scanner &amp; Catalog Crawler Control</h2>
              <p style={{ color: "#8fa8be", margin: 0, fontSize: 14 }}>
                Control the high-throughput asynchronous catalog crawler or inspect any URL live to extract technology forensics.
              </p>
            </div>

            {/* On-Demand URL Inspector */}
            <div className="crawler-panel">
              <h3 style={{ fontSize: 16, marginTop: 0, marginBottom: 12, display: "flex", alignItems: "center", gap: 8 }}>
                <span>⚡ On-Demand URL Inspector</span>
              </h3>
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

              {/* Quick sample URLs */}
              <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap", marginTop: 10 }}>
                <span style={{ fontSize: 12, color: "#6b8296" }}>Quick test:</span>
                {[
                  "https://wordpress.org/",
                  "https://wikipedia.org/",
                  "https://eff.org/",
                  "https://news.ycombinator.com/",
                  "https://archive.org/",
                ].map((sample) => (
                  <button
                    key={sample}
                    type="button"
                    onClick={() => handleLiveScan(undefined, sample)}
                    disabled={scanBusy}
                    style={{ fontSize: 11, padding: "3px 8px", background: "#111a24", border: "1px solid #1f3042", borderRadius: 4 }}
                  >
                    {sample.replace("https://", "").replace("/", "")}
                  </button>
                ))}
              </div>

              {scanError && <p role="alert" style={{ color: "#e74c3c", marginTop: 12 }}>{scanError}</p>}

              {scanResult && (
                <div style={{ marginTop: 20, padding: 18, background: "#0a1017", borderRadius: 8, border: "1px solid #1c2b3a" }}>
                  <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
                    <h4 style={{ margin: 0, color: "#6ce0c7", fontSize: 15 }}>✓ Successfully Indexed into Intel</h4>
                    <span className="badge badge-status">HTTP {scanResult.status_code}</span>
                  </div>
                  <table style={{ marginTop: 12 }}>
                    <tbody>
                      <tr>
                        <td style={{ width: 140, color: "#70899e" }}>URL</td>
                        <td>
                          <a href={scanResult.url} target="_blank" rel="noopener noreferrer" style={{ color: "#78dccc" }}>
                            {scanResult.url} ↗
                          </a>
                        </td>
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
                  <div style={{ marginTop: 14, display: "flex", gap: 10 }}>
                    <button onClick={() => setViewSourceUrl(scanResult.url)} style={{ fontSize: 13 }} className="secondary">
                      Inspect Full HTML Source
                    </button>
                    <button
                      onClick={() => {
                        setIntelInput(scanResult.host);
                        setIntelQuery(scanResult.host);
                        setActiveTab("intel");
                      }}
                      style={{ fontSize: 13 }}
                      className="secondary"
                    >
                      Search in Intel
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

              {/* Server Resource Controller & Isolation Panel (systemd cgroups v2) */}
              <div style={{ background: "#0a111a", border: "1px solid #1a2c3d", borderRadius: 8, padding: "16px 18px", margin: "16px 0" }}>
                <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 12, flexWrap: "wrap", gap: 10 }}>
                  <div>
                    <h4 style={{ margin: "0 0 4px", fontSize: 15, display: "flex", alignItems: "center", gap: 8, color: "#6ce0c7" }}>
                      🛡️ Droplet Resource Controller &amp; Isolation
                    </h4>
                    <p style={{ margin: 0, fontSize: 13, color: "#8ab4cc" }}>
                      Hardware isolation enforced at Linux kernel level (cgroups v2). Leaves &gt;2.2 CPU cores for OS/CNC.
                    </p>
                  </div>
                  <span style={{ fontSize: 11, background: "rgba(108,224,199,0.12)", color: "#6ce0c7", padding: "4px 10px", borderRadius: 12, border: "1px solid #2e695d", fontWeight: 600 }}>
                    Mode: {selectedProfile.toUpperCase()} ({crawlerConcurrency} Workers)
                  </span>
                </div>

                {/* Telemetry Grid */}
                <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(160px, 1fr))", gap: 10, marginBottom: 14 }}>
                  <div style={{ background: "#070c12", padding: "10px 12px", borderRadius: 6, border: "1px solid #14212e" }}>
                    <span style={{ fontSize: 12, color: "#728fa5", display: "block" }}>CPU Quota Cap</span>
                    <strong style={{ fontSize: 14, color: "#e4edf5" }}>180% (1.8 cores max)</strong>
                    <span style={{ fontSize: 11, color: "#546d81", display: "block" }}>&gt;2.2 cores reserved</span>
                  </div>
                  <div style={{ background: "#070c12", padding: "10px 12px", borderRadius: 6, border: "1px solid #14212e" }}>
                    <span style={{ fontSize: 12, color: "#728fa5", display: "block" }}>RAM Memory Cap</span>
                    <strong style={{ fontSize: 14, color: "#e4edf5" }}>1,200 MB max</strong>
                    <span style={{ fontSize: 11, color: "#546d81", display: "block" }}>Protects from OOM</span>
                  </div>
                  <div style={{ background: "#070c12", padding: "10px 12px", borderRadius: 6, border: "1px solid #14212e" }}>
                    <span style={{ fontSize: 12, color: "#728fa5", display: "block" }}>Live Memory (RSS)</span>
                    <strong style={{ fontSize: 14, color: "#55efc4" }}>
                      {resourceInfo?.telemetry.memory_mb ? `${resourceInfo.telemetry.memory_mb} MB` : "—"}
                    </strong>
                    <span style={{ fontSize: 11, color: "#546d81", display: "block" }}>Real-time process footprint</span>
                  </div>
                  <div style={{ background: "#070c12", padding: "10px 12px", borderRadius: 6, border: "1px solid #14212e" }}>
                    <span style={{ fontSize: 12, color: "#728fa5", display: "block" }}>Service Priority</span>
                    <strong style={{ fontSize: 14, color: "#e4edf5" }}>Nice 5 / Weight 50</strong>
                    <span style={{ fontSize: 11, color: "#546d81", display: "block" }}>Yields CPU to queries</span>
                  </div>
                </div>

                {/* Profile Selector Buttons */}
                <div style={{ display: "flex", gap: 8, flexWrap: "wrap", alignItems: "center" }}>
                  <span style={{ fontSize: 12, color: "#8fa8be", fontWeight: 500 }}>Select Resource Profile:</span>
                  {[
                    { id: "eco", name: "🍃 Eco (150 Workers)", workers: 150 },
                    { id: "balanced", name: "⚖️ Balanced (350 Workers)", workers: 350 },
                    { id: "high", name: "🚀 High Speed (600 Workers)", workers: 600 },
                    { id: "turbo", name: "⚡ Turbo (1,000 Workers)", workers: 1000 },
                  ].map((p) => (
                    <button
                      key={p.id}
                      type="button"
                      className={selectedProfile === p.id ? "primary" : "secondary"}
                      onClick={() => handleApplyProfile(p.id, p.workers)}
                      disabled={resourceBusy}
                      style={{ fontSize: 12, padding: "6px 12px" }}
                    >
                      {p.name}
                    </button>
                  ))}
                </div>
              </div>

              {/* Live Crawler Configuration & Status */}
              <div style={{ display: "flex", gap: 20, alignItems: "center", margin: "16px 0", flexWrap: "wrap", background: "#090f17", padding: "12px 16px", borderRadius: 8, border: "1px solid #182635" }}>
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
                    <option value={350}>350 Workers (Balanced)</option>
                    <option value={600}>600 Workers (High Speed)</option>
                    <option value={1000}>1,000 Workers (Turbo)</option>
                  </select>
                </label>

                <label style={{ fontSize: 13, color: "#93a8b9" }}>
                  Batch Limit:
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
                <div className="metric-card">
                  <strong>{crawlerStatus.scanned.toLocaleString()}</strong>
                  <span>URLs Scanned in Session</span>
                </div>
                <div className="metric-card">
                  <strong>{intelStats ? intelStats.total_pages.toLocaleString() : "—"}</strong>
                  <span>Active Pages Indexed into Intel</span>
                </div>
                <div className="metric-card">
                  <strong>{crawlerStatus.wordpress_found.toLocaleString()}</strong>
                  <span>WordPress Detections</span>
                </div>
                <div className="metric-card">
                  <strong>{crawlerStatus.running ? `${crawlerStatus.speed} req/s` : "Idle"}</strong>
                  <span>Scanning Speed</span>
                </div>
              </div>

              {/* Live Streaming Activity Feed */}
              <div style={{ marginTop: 24 }}>
                <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 12, flexWrap: "wrap", gap: 10 }}>
                  <h4 style={{ fontSize: 14, color: "#8ab4cc", margin: 0, display: "flex", alignItems: "center", gap: 8 }}>
                    <span>Live Streaming Activity (Latest Crawled Pages)</span>
                    {crawlerStatus.running && <span className="pulse-indicator"><span className="pulse-dot"></span> Live</span>}
                  </h4>
                  <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
                    <button
                      type="button"
                      onClick={() => handleDownloadTxt({ scanned: "1" })}
                      style={{ fontSize: 11, padding: "5px 10px" }}
                      title="Download all scanned URLs from database as .txt"
                    >
                      ⬇ Download Scanned (.txt)
                    </button>
                    <button
                      type="button"
                      onClick={() => handleDownloadTxt({ scanned: "1", wordpress: "1" })}
                      style={{ fontSize: 11, padding: "5px 10px" }}
                      title="Download all discovered WordPress URLs from database as .txt"
                    >
                      ⬇ Download WordPress (.txt)
                    </button>
                    <input
                      type="search"
                      placeholder="Filter feed by URL, title, or tech..."
                      value={feedSearch}
                      onChange={(e) => setFeedSearch(e.target.value)}
                      style={{ padding: "6px 12px", fontSize: 12, width: 200 }}
                    />
                  </div>
                </div>

                <table className="feed-table">
                  <thead>
                    <tr>
                      <th style={{ width: 80 }}>Status</th>
                      <th>URL / Title</th>
                      <th>Detected Tech</th>
                      <th style={{ width: 140 }}>Size / Time</th>
                      <th style={{ width: 90, textAlign: "center" }}>Action</th>
                    </tr>
                  </thead>
                  <tbody>
                    {filteredFeed.map((item, idx) => (
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
                        <td style={{ textAlign: "center" }}>
                          <button
                            onClick={() => setViewSourceUrl(item.url)}
                            style={{ padding: "3px 8px", fontSize: 11 }}
                            className="secondary"
                          >
                            Source
                          </button>
                        </td>
                      </tr>
                    ))}
                    {!filteredFeed.length && (
                      <tr>
                        <td colSpan={5} style={{ textAlign: "center", color: "#6a8296", padding: 24 }}>
                          {feedSearch ? "No activity matching filter." : "No crawl activity recorded yet. Click «Start Crawler» or «Scan & Index» above!"}
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
              <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
                <button
                  type="button"
                  onClick={() => handleExportCatalog("url")}
                  title="Download matching URLs as plain text (.txt)"
                  className="secondary"
                >
                  ⬇ Download URLs (.txt)
                </button>
                <button
                  type="button"
                  onClick={() => handleExportCatalog("host")}
                  title="Download matching hostnames as plain text (.txt)"
                  className="secondary"
                >
                  ⬇ Download Hosts (.txt)
                </button>
                <button disabled={busy} onClick={() => setCatalogRefresh((v) => v + 1)}>
                  ↻ Refresh
                </button>
              </div>
            </div>

            {catalogError && <p role="alert" style={{ color: "#e74c3c" }}>{catalogError}</p>}

            <div id="metrics">
              <div className="metric-card">
                <strong>{stats?.urls ? stats.urls.toLocaleString() : "—"}</strong>
                <span>Total .org URLs in Catalog</span>
              </div>
              <div className="metric-card">
                <strong>{stats?.imports ? stats.imports.toLocaleString() : "—"}</strong>
                <span>Dataset Imports</span>
              </div>
              <div className="metric-card">
                <strong>{stats?.wordpress ? stats.wordpress.toLocaleString() : "—"}</strong>
                <span>WordPress Evidence Records</span>
              </div>
            </div>

            <form
              onSubmit={(e) => {
                e.preventDefault();
                setPage(1);
                setFilter(host);
              }}
              style={{ margin: "20px 0" }}
            >
              <input
                type="search"
                aria-label="Search hostname"
                value={host}
                onChange={(e) => setHost(e.target.value)}
                placeholder="Search hostname (e.g. wikipedia, archive, eff, mozilla)"
              />
              {host && (
                <button type="button" onClick={() => { setHost(""); setFilter(""); setPage(1); }} title="Clear search" className="secondary">
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
                WordPress evidence only
              </label>
              <button className="primary" disabled={busy}>
                Search
              </button>
            </form>

            <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginTop: 16 }}>
              <p role="status" aria-live="polite" style={{ margin: 0, fontSize: 13, color: "#8fa8be" }}>
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
                    background: "#111a24",
                    color: "#e4edf5",
                    border: "1px solid #273747",
                    borderRadius: 7,
                    padding: "5px 10px",
                    fontSize: 13,
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
                  <th>Evidence</th>
                  <th style={{ width: 180, textAlign: "center" }}>Actions</th>
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
                    <td style={{ color: "#c5d8e8", fontSize: 13 }}>{site.host}</td>
                    <td>
                      {site.evidence && site.evidence.length > 0 ? (
                        <div style={{ display: "flex", gap: 4, flexWrap: "wrap" }}>
                          {site.evidence.map((ev, i) => (
                            <span key={i} className="badge badge-tech" style={{ fontSize: 10 }}>
                              {ev.replace("meta:", "").replace("resource:", "")}
                            </span>
                          ))}
                        </div>
                      ) : (
                        <span style={{ color: "#546e7a", fontSize: 11 }}>—</span>
                      )}
                    </td>
                    <td style={{ textAlign: "center" }}>
                      <div style={{ display: "flex", gap: 6, justifyContent: "center" }}>
                        <button
                          onClick={() => {
                            setScanTargetUrl(site.url);
                            setActiveTab("scanner");
                          }}
                          style={{ padding: "3px 8px", fontSize: 11 }}
                          className="secondary"
                          title="Scan this site live"
                        >
                          Scan Live
                        </button>
                        <button
                          onClick={() => {
                            setIntelInput(site.host);
                            setIntelQuery(site.host);
                            setActiveTab("intel");
                          }}
                          style={{ padding: "3px 8px", fontSize: 11 }}
                          className="secondary"
                          title="Search in HTML Intel"
                        >
                          Intel
                        </button>
                        <a
                          href={site.url}
                          target="_blank"
                          rel="noopener noreferrer"
                          style={{ color: "#81acb9", textDecoration: "none", fontSize: 13, padding: "3px 6px" }}
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
                    <td colSpan={4} style={{ textAlign: "center", padding: 30, color: "#6a8296" }}>
                      No matching URLs found.
                    </td>
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
              <span style={{ fontSize: 13, color: "#8fa8be" }}>
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
                <button onClick={() => setSelectedObsUrl("")} style={{ float: "right" }} className="secondary">
                  ✕ Close
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
                            className="badge badge-tech"
                            style={{ marginRight: 6, marginBottom: 4 }}
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

        {/* FULL SOURCE CODE VIEWER MODAL WITH IN-PAGE SEARCH */}
        {viewSourceUrl && (
          <>
            <div className="modal-backdrop" onClick={() => setViewSourceUrl("")} />
            <div className="source-modal">
              <div className="source-modal-header">
                <div>
                  <h3 style={{ margin: 0, fontSize: 17, color: "#6ce0c7", display: "flex", alignItems: "center", gap: 8 }}>
                    <span>Source Code Inspector</span>
                  </h3>
                  <small style={{ color: "#8ab4cc", fontFamily: "monospace" }}>{viewSourceUrl}</small>
                </div>
                <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
                  {sourceData && (
                    <button
                      onClick={() => {
                        navigator.clipboard.writeText(sourceData.html);
                        setSourceCopied(true);
                        setTimeout(() => setSourceCopied(false), 2000);
                      }}
                      style={{ fontSize: 12, padding: "6px 12px" }}
                      className="secondary"
                    >
                      {sourceCopied ? "✓ Copied!" : "Copy HTML"}
                    </button>
                  )}
                  <button onClick={() => setViewSourceUrl("")} style={{ fontSize: 13, padding: "6px 12px" }}>
                    ✕ Close
                  </button>
                </div>
              </div>

              {sourceLoading && (
                <div style={{ padding: 40, textAlign: "center" }}>
                  <span className="pulse-indicator">
                    <span className="pulse-dot"></span> Loading HTML source...
                  </span>
                </div>
              )}

              {sourceData && (
                <div className="source-modal-body">
                  <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 14, flexWrap: "wrap", gap: 10 }}>
                    <div style={{ display: "flex", gap: 12, alignItems: "center", flexWrap: "wrap", fontSize: 12, color: "#8ab4cc" }}>
                      <span>Lines: <strong>{sourceData.line_count.toLocaleString()}</strong></span>
                      <span>Title: <strong>{sourceData.title || "—"}</strong></span>
                      <span className="badge badge-status">HTTP {sourceData.status_code}</span>
                      {sourceData.signatures.map((sig) => (
                        <span key={sig} className="badge badge-tech">{sig}</span>
                      ))}
                    </div>

                    {/* In-Modal Search and Line Wrap Controls */}
                    <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
                      <input
                        type="search"
                        placeholder="Search in HTML source..."
                        value={sourceSearch}
                        onChange={(e) => setSourceSearch(e.target.value)}
                        style={{ padding: "4px 10px", fontSize: 12, width: 180 }}
                      />
                      {modalMatchesCount > 0 && (
                        <span style={{ fontSize: 11, color: "#f39c12", fontWeight: 600 }}>
                          {modalMatchesCount} match{modalMatchesCount === 1 ? "" : "es"}
                        </span>
                      )}
                      <button
                        type="button"
                        onClick={() => setWrapSourceLines((v) => !v)}
                        style={{ fontSize: 11, padding: "4px 8px" }}
                        className="secondary"
                        title="Toggle line wrapping"
                      >
                        {wrapSourceLines ? "No Wrap" : "Wrap"}
                      </button>
                    </div>
                  </div>

                  <div
                    style={{
                      maxHeight: "58vh",
                      overflowY: "auto",
                      background: "#070c12",
                      border: "1px solid #162433",
                      borderRadius: 8,
                      padding: "10px 14px",
                      fontFamily: "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace",
                      fontSize: 12,
                      lineHeight: 1.55,
                    }}
                  >
                    {sourceData.html.split("\n").map((line, idx) => {
                      const activeFilter = (sourceSearch || intelQuery).trim();
                      const isMatch = activeFilter && line.toLowerCase().includes(activeFilter.toLowerCase());

                      return (
                        <div
                          key={idx}
                          style={{
                            display: "flex",
                            gap: 14,
                            background: isMatch ? "rgba(243, 156, 18, 0.15)" : undefined,
                            borderLeft: isMatch ? "2px solid #f39c12" : "2px solid transparent",
                            padding: "1px 4px",
                          }}
                        >
                          <span style={{ color: "#495c6e", minWidth: 42, textAlign: "right", userSelect: "none" }}>
                            {idx + 1}
                          </span>
                          <span
                            style={{
                              color: "#c5d8e8",
                              whiteSpace: wrapSourceLines ? "pre-wrap" : "pre",
                              wordBreak: wrapSourceLines ? "break-all" : "normal",
                            }}
                          >
                            {activeFilter ? highlightMatches(line, activeFilter) : line}
                          </span>
                        </div>
                      );
                    })}
                  </div>
                </div>
              )}
            </div>
          </>
        )}
      </main>
    </>
  );
}
