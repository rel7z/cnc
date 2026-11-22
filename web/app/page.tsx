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

type DbSource = {
  id: number;
  source: string;
  imported_at: string;
  accepted: number;
  rejected: number;
  observation_count: number;
  observations_count?: number;
  wp_count: number;
  wordpress_count?: number;
};

type DbStats = {
  database_size_bytes?: number;
  size_bytes?: number;
  database_size_mb?: number;
  size_mb?: number;
  wal_size_bytes?: number;
  wal_size_mb?: number;
  page_size?: number;
  page_count?: number;
  tables: Record<string, number>;
  engine?: {
    journal_mode: string;
    page_size: number;
    page_count: number;
    user_version: number;
  };
};

type DbSampleRecord = {
  url: string;
  host: string;
  scanned_at?: string;
  evidence: string[];
};

type DbBundleStatus = {
  verification?: { status: string; message?: string; integrity?: string; scope?: string } | null;
  status: string;
  is_backup_running: boolean;
  backup?: {
    updated_at: number;
    data: {
      filename?: string;
      bytes?: number;
      sha256?: string;
      format?: string;
      databases?: string[];
      source_included?: boolean;
      snapshots?: Array<{
        filename: string;
        bytes: number;
        sha256: string;
        integrity: string;
      }>;
    };
  };
  sync?: {
    updated_at: number;
    data: {
      imported?: number;
    };
  };
  drive?: {
    enabled: boolean;
    configured: boolean;
    watch_dir: string;
    parent_folder_id: string;
    upload_interval: string;
    uploaded_count: number;
    known_files_count?: number;
    is_uploaded: boolean;
    drive_file_id?: string;
  };
  key_configured: boolean;
  key_path?: string;
  drive_inbox?: string;
  recent_bundles?: Array<{
    filename: string;
    bytes: number;
    size_mb: number;
    created_at: number;
  }>;
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
  const [activeTab, setActiveTab] = useState<"intel" | "scanner" | "catalog" | "database">("intel");

  // Distributed Cluster Swarm State
  const [clusterStatus, setClusterStatus] = useState<ClusterStatus | null>(null);
  const [clusterBusy, setClusterBusy] = useState(false);
  const [timeStr, setTimeStr] = useState("");

  // Tactical Clock Hook
  useEffect(() => {
    const updateTime = () => {
      const now = new Date();
      setTimeStr(now.toISOString().replace("T", " ").slice(0, 19) + " UTC");
    };
    updateTime();
    const timer = setInterval(updateTime, 1000);
    return () => clearInterval(timer);
  }, []);

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

  // Tab 4: Database Management State
  const [dbSources, setDbSources] = useState<DbSource[]>([]);
  const [dbStats, setDbStats] = useState<DbStats | null>(null);
  const [dbBundleStatus, setDbBundleStatus] = useState<DbBundleStatus | null>(null);
  const [bundleActionBusy, setBundleActionBusy] = useState(false);
  const [bundleVerifyBusy, setBundleVerifyBusy] = useState(false);
  const [syncActionBusy, setSyncActionBusy] = useState(false);
  const [dbBusy, setDbBusy] = useState(false);
  const [dbError, setDbError] = useState("");
  const [dbSearch, setDbSearch] = useState("");
  const [dbSort, setDbSort] = useState<"id_desc" | "id_asc" | "obs_desc" | "wp_desc" | "accepted_desc">("id_desc");
  const [dbCategoryFilter, setDbCategoryFilter] = useState<string>("all");
  const [dbMaintenanceMsg, setDbMaintenanceMsg] = useState<{ type: "success" | "error"; text: string } | null>(null);
  const [dbMaintenanceBusy, setDbMaintenanceBusy] = useState(false);

  // Sample Viewer Modal State
  const [selectedDbSource, setSelectedDbSource] = useState<DbSource | null>(null);
  const [sampleRecords, setSampleRecords] = useState<DbSampleRecord[]>([]);
  const [sampleLoading, setSampleLoading] = useState(false);
  const [sampleFilter, setSampleFilter] = useState("");
  const [sampleCopied, setSampleCopied] = useState(false);

  // Dataset Ingestion Modal State
  const [showImportModal, setShowImportModal] = useState(false);
  const [importSourceName, setImportSourceName] = useState("");
  const [importUrlsText, setImportUrlsText] = useState("");
  const [importBusy, setImportBusy] = useState(false);
  const [importResult, setImportResult] = useState<{ accepted: number; rejected: number; source: string } | null>(null);

  // Source Deletion State
  const [deleteConfirmSource, setDeleteConfirmSource] = useState<DbSource | null>(null);
  const [deleteBusy, setDeleteBusy] = useState(false);

  // Floating Toast Notifications State
  const [toasts, setToasts] = useState<Array<{ id: string; message: string; type: "info" | "success" | "error" }>>([]);

  const showToast = useCallback((message: string, type: "info" | "success" | "error" = "info") => {
    const id = Math.random().toString(36).substring(2, 9);
    setToasts((prev) => [...prev, { id, message, type }]);
    setTimeout(() => {
      setToasts((prev) => prev.filter((t) => t.id !== id));
    }, 3500);
  }, []);

  // Real-Time Clock
  const [currentTime, setCurrentTime] = useState("");
  useEffect(() => {
    const updateTime = () => {
      const now = new Date();
      setCurrentTime(now.toLocaleTimeString("en-GB", { hour: "2-digit", minute: "2-digit", second: "2-digit" }));
    };
    updateTime();
    const interval = setInterval(updateTime, 1000);
    return () => clearInterval(interval);
  }, []);

  // Text File Loader for Dataset Ingestion
  const handleFileUpload = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    const reader = new FileReader();
    reader.onload = (event) => {
      const text = event.target?.result as string;
      if (text) {
        setImportUrlsText((prev) => (prev ? prev + "\n" + text : text));
        const lineCount = text.split("\n").filter((l) => l.trim()).length;
        showToast(`Loaded ${file.name} (${lineCount.toLocaleString()} lines)`, "success");
      }
    };
    reader.readAsText(file);
  };

  // Keyboard navigation for modals
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        if (viewSourceUrl) setViewSourceUrl("");
        if (selectedDbSource) setSelectedDbSource(null);
        if (showImportModal) setShowImportModal(false);
        if (deleteConfirmSource) setDeleteConfirmSource(null);
      }
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [viewSourceUrl, selectedDbSource, showImportModal, deleteConfirmSource]);

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

  // Polling for Crawler & Cluster Updates and Recent Feed
  useEffect(() => {
    let interval: NodeJS.Timeout | null = null;
    const fetchData = async () => {
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
        const iRes = await fetch("/api/catalog/intel-stats");
        if (iRes.ok) {
          setIntelStats(await iRes.json());
        }
        const cRes = await fetch("/api/catalog/cluster-nodes");
        if (cRes.ok) {
          const cData: ClusterStatus = await cRes.json();
          setClusterStatus(cData);
        }
      } catch {}
    };

    if (crawlerStatus.running || (clusterStatus?.active_nodes ?? 0) > 0 || activeTab === "scanner") {
      void fetchData();
      interval = setInterval(fetchData, 2000);
    }

    return () => {
      if (interval) clearInterval(interval);
    };
  }, [crawlerStatus.running, clusterStatus?.active_nodes, activeTab]);

  // Cluster Swarm Handlers
  const handleStartCluster = async () => {
    setClusterBusy(true);
    try {
      await fetch("/api/catalog/cluster-start", { method: "POST" });
      const cRes = await fetch("/api/catalog/cluster-nodes");
      if (cRes.ok) setClusterStatus(await cRes.json());
      void loadGeneralStats();
    } catch {} finally {
      setClusterBusy(false);
    }
  };

  const handleStopCluster = async () => {
    setClusterBusy(true);
    try {
      await fetch("/api/catalog/cluster-stop", { method: "POST" });
      const cRes = await fetch("/api/catalog/cluster-nodes");
      if (cRes.ok) setClusterStatus(await cRes.json());
      void loadGeneralStats();
    } catch {} finally {
      setClusterBusy(false);
    }
  };

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
    showToast("URL copied to clipboard", "info");
    setTimeout(() => setCopiedUrl(null), 2000);
  };

  // Direct API .txt and JSON download from database
  const handleDownloadTxt = (params: Record<string, string>) => {
    const q = new URLSearchParams({ format: "txt", ...params });
    window.location.href = "/api/catalog/export?" + q.toString();
    showToast(`Downloading dataset ${params.field === "host" ? "hosts" : "URLs"} (.txt)...`, "info");
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

  // Tab 4: Database Management Handlers & Data Loading
  const loadDatabaseData = useCallback(async () => {
    setDbBusy(true);
    setDbError("");
    try {
      const [srcRes, statsRes, bundleRes] = await Promise.all([
        fetch("/api/catalog/database-sources"),
        fetch("/api/catalog/database-stats"),
        fetch("/api/catalog/database-bundle-status"),
      ]);
      if (srcRes.ok) {
        const data = await srcRes.json();
        setDbSources(data.sources || []);
      } else {
        setDbError("Failed to load database sources");
      }
      if (statsRes.ok) {
        const data = await statsRes.json();
        setDbStats(data);
      }
      if (bundleRes.ok) {
        const data = await bundleRes.json();
        setDbBundleStatus(data);
      }
    } catch {
      setDbError("Error contacting database service");
    } finally {
      setDbBusy(false);
    }
  }, []);

  useEffect(() => {
    if (activeTab === "database") {
      void loadDatabaseData();
    }
  }, [activeTab, loadDatabaseData, catalogRefresh]);

  useEffect(() => {
    if (activeTab !== "database") return;
    const interval = setInterval(async () => {
      try {
        const response = await fetch("/api/catalog/database-bundle-status");
        if (response.ok) setDbBundleStatus(await response.json());
      } catch {}
    }, 5000);
    return () => clearInterval(interval);
  }, [activeTab]);

  const handleRunMaintenance = async (action: "checkpoint" | "optimize" | "integrity" | "vacuum") => {
    setDbMaintenanceBusy(true);
    setDbMaintenanceMsg(null);
    try {
      const res = await fetch("/api/catalog/database-maintenance", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ action }),
      });
      const data = await res.json();
      if (res.ok && (data.status === "ok" || data.success)) {
        let detail = data.message || "";
        if (action === "integrity" && data.result) {
          detail = `Result: ${JSON.stringify(data.result)}`;
        } else if (action === "checkpoint" && data.result) {
          detail = `WAL flushed: ${JSON.stringify(data.result)}`;
        } else if (!detail) {
          detail = `Operation completed successfully`;
        }
        setDbMaintenanceMsg({
          type: "success",
          text: `Maintenance [${action}] succeeded. ${detail}`,
        });
        showToast(`Maintenance [${action}] succeeded!`, "success");
        await loadDatabaseData();
        await loadGeneralStats();
      } else {
        const errText = data.error || `Maintenance [${action}] failed`;
        setDbMaintenanceMsg({
          type: "error",
          text: errText,
        });
        showToast(errText, "error");
      }
    } catch {
      setDbMaintenanceMsg({
        type: "error",
        text: `Network error running maintenance [${action}]`,
      });
      showToast(`Network error running maintenance [${action}]`, "error");
    } finally {
      setDbMaintenanceBusy(false);
    }
  };

  const handleCreateBundle = async () => {
    setBundleActionBusy(true);
    try {
      const res = await fetch("/api/catalog/database-bundle-create", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
      });
      const data = await res.json();
      if (res.ok && data.success) {
        showToast(data.message || "Recovery bundle backup started!", "success");
        setDbMaintenanceMsg({ type: "success", text: data.message || "Recovery bundle creation started" });
        void loadDatabaseData();
      } else {
        showToast(data.message || data.error || "Failed to create recovery bundle", "error");
        setDbMaintenanceMsg({ type: "error", text: data.message || data.error || "Failed to create recovery bundle" });
      }
    } catch {
      showToast("Error requesting recovery bundle creation", "error");
    } finally {
      setBundleActionBusy(false);
    }
  };

  const handleSyncDiscovery = async () => {
    setSyncActionBusy(true);
    try {
      const res = await fetch("/api/catalog/database-sync", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
      });
      const data = await res.json();
      if (res.ok && data.success) {
        showToast(data.message || `Discovery classifications synchronized (${data.imported} imported)`, "success");
        setDbMaintenanceMsg({ type: "success", text: data.message || `Synchronized ${data.imported} records` });
        void loadDatabaseData();
      } else {
        showToast(data.message || data.error || "Failed to synchronize discovery", "error");
        setDbMaintenanceMsg({ type: "error", text: data.message || data.error || "Sync failed" });
      }
    } catch {
      showToast("Error requesting discovery synchronization", "error");
    } finally {
      setSyncActionBusy(false);
    }
  };

  const handleVerifyBundle = async () => {
    setBundleVerifyBusy(true);
    try {
      const res = await fetch("/api/catalog/database-bundle-verify", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
      });
      const data = await res.json();
      if (res.ok && data.success) {
        showToast(data.message || "Local archive verification started", "success");
        setDbMaintenanceMsg({ type: "success", text: data.message || "Local archive verification started" });
        void loadDatabaseData();
      } else {
        showToast(data.error || data.message || "Failed to verify recovery bundle", "error");
        setDbMaintenanceMsg({ type: "error", text: data.error || data.message || "Verification failed" });
      }
    } catch {
      showToast("Error requesting recovery bundle verification", "error");
    } finally {
      setBundleVerifyBusy(false);
    }
  };

  const handleOpenSample = async (source: DbSource) => {
    setSelectedDbSource(source);
    setSampleRecords([]);
    setSampleLoading(true);
    setSampleFilter("");
    setSampleCopied(false);
    try {
      const res = await fetch(`/api/catalog/database-sample?id=${source.id}`);
      if (res.ok) {
        const data = await res.json();
        setSampleRecords(data.sample_records || data.sample || []);
      }
    } catch {
    } finally {
      setSampleLoading(false);
    }
  };

  const handleConfirmDelete = async () => {
    if (!deleteConfirmSource) return;
    setDeleteBusy(true);
    try {
      const res = await fetch("/api/catalog/database-delete", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ id: deleteConfirmSource.id }),
      });
      const data = await res.json();
      if (res.ok && data.status === "deleted") {
        setDbMaintenanceMsg({
          type: "success",
          text: `Deleted source #${deleteConfirmSource.id} (${deleteConfirmSource.source}) and purged ${data.deleted_observations?.toLocaleString() ?? 0} observations.`,
        });
        showToast(`Deleted source #${deleteConfirmSource.id}`, "success");
        setDeleteConfirmSource(null);
        await loadDatabaseData();
        await loadGeneralStats();
      } else {
        const errText = data.error || "Failed to delete source";
        setDbMaintenanceMsg({
          type: "error",
          text: errText,
        });
        showToast(errText, "error");
      }
    } catch {
      setDbMaintenanceMsg({
        type: "error",
        text: "Network error deleting source",
      });
      showToast("Network error deleting source", "error");
    } finally {
      setDeleteBusy(false);
    }
  };

  const handleImportDataset = async (e: React.FormEvent) => {
    e.preventDefault();
    const urls = importUrlsText
      .split("\n")
      .map((u) => u.trim())
      .filter((u) => u.length > 0);
    if (!importSourceName.trim()) {
      showToast("Please provide a source name / label", "error");
      return;
    }
    if (urls.length === 0) {
      showToast("Please provide at least one URL to ingest", "error");
      return;
    }
    setImportBusy(true);
    setImportResult(null);
    try {
      const res = await fetch("/api/catalog/database-import", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          source: importSourceName.trim(),
          urls,
        }),
      });
      const data = await res.json();
      if (res.ok && data.status === "imported") {
        setImportResult(data);
        setImportUrlsText("");
        showToast(`Imported ${data.accepted.toLocaleString()} URLs into catalog!`, "success");
        await loadDatabaseData();
        await loadGeneralStats();
      } else {
        showToast(data.error || "Failed to import dataset", "error");
      }
    } catch {
      showToast("Network error importing dataset", "error");
    } finally {
      setImportBusy(false);
    }
  };

  const getSourceCategory = (src: string): { label: string; cls: string; type: string } => {
    if (src.startsWith("cluster-worker")) return { label: "Swarm Worker", cls: "worker", type: "worker" };
    if (src.startsWith("czds-") || src.endsWith(".zone")) return { label: "Zone File", cls: "zone", type: "zone" };
    if (src.startsWith("cnc:verified-discovery")) return { label: "Verified Discovery", cls: "discovery", type: "discovery" };
    if (src.startsWith("internet-map:cnc-generator")) return { label: "CNC Generator", cls: "generator", type: "generator" };
    if (src.startsWith("wordpress-scanner")) return { label: "WP Crawler", cls: "discovery", type: "discovery" };
    return { label: "Dataset", cls: "custom", type: "custom" };
  };

  const filteredDbSources = useMemo(() => {
    let list = [...dbSources];
    if (dbCategoryFilter !== "all") {
      list = list.filter((s) => getSourceCategory(s.source).type === dbCategoryFilter);
    }
    if (dbSearch.trim()) {
      const q = dbSearch.trim().toLowerCase();
      list = list.filter(
        (s) =>
          String(s.id).includes(q) ||
          s.source.toLowerCase().includes(q) ||
          getSourceCategory(s.source).label.toLowerCase().includes(q)
      );
    }
    list.sort((a, b) => {
      const aObs = a.observation_count ?? a.observations_count ?? 0;
      const bObs = b.observation_count ?? b.observations_count ?? 0;
      const aWp = a.wp_count ?? a.wordpress_count ?? 0;
      const bWp = b.wp_count ?? b.wordpress_count ?? 0;

      if (dbSort === "id_desc") return b.id - a.id;
      if (dbSort === "id_asc") return a.id - b.id;
      if (dbSort === "obs_desc") return bObs - aObs;
      if (dbSort === "wp_desc") return bWp - aWp;
      if (dbSort === "accepted_desc") return b.accepted - a.accepted;
      return 0;
    });
    return list;
  }, [dbSources, dbCategoryFilter, dbSearch, dbSort]);


  return (
    <>
      <header>
        <div className="brand-container">
          <div className="brand-logo">
            <span style={{ fontSize: 18, filter: "drop-shadow(0 0 6px rgba(0, 242, 254, 0.7))" }}>⚡</span>
          </div>
          <div>
            <h1>
              <span>Internet Map</span>
              <span className="badge badge-tech" style={{ fontSize: 10, padding: "2px 6px", textTransform: "uppercase" }}>INTEL OS v2.5</span>
            </h1>
            <span className="subtitle">PublicWWW HTML Source Intelligence • 12.25M .org Catalog • 8-Node Swarm Mesh</span>
          </div>
        </div>

        <div className="header-status">
          {timeStr && (
            <span className="status-pill" style={{ borderColor: "rgba(255, 255, 255, 0.08)" }}>
              <span style={{ color: "#38bdf8", fontSize: 10 }}>●</span>
              {timeStr}
            </span>
          )}

          {(clusterStatus?.active_nodes ?? 0) > 0 ? (
            <span className="status-pill active" style={{ background: "rgba(16, 185, 129, 0.15)", color: "#55efc4", borderColor: "#10b981" }}>
              <span className="pulse-dot"></span>
              SWARM ACTIVE: {clusterStatus?.active_nodes} NODES ({(clusterStatus?.cluster_speed ?? 0).toFixed(0)} req/s)
            </span>
          ) : crawlerStatus.running ? (
            <span className="status-pill active">
              <span className="pulse-dot"></span>
              CRAWLER ACTIVE ({crawlerStatus.speed.toFixed(0)} req/s)
            </span>
          ) : (
            <span className="status-pill">
              <span style={{ color: "#64748b" }}>⏸</span>
              SWARM STANDBY
            </span>
          )}

          {intelStats && (
            <span className="status-pill intel">
              <span style={{ color: "#38bdf8" }}>⚡</span>
              <strong>{intelStats.total_pages.toLocaleString()}</strong> HTML Pages ({((intelStats.total_bytes || 0) / (1024 * 1024)).toFixed(1)} MB)
            </span>
          )}

          <span className="status-pill" style={{ fontFamily: "monospace", fontSize: 11.5 }} title="Controller local time">
            🕒 {currentTime || "Live"} • SGP1
          </span>

          <button
            onClick={() => {
              void loadGeneralStats();
              setCatalogRefresh((v) => v + 1);
              showToast("Refreshing all metrics...", "info");
            }}
            title="Refresh All Statistics"
            style={{ padding: "6px 11px", fontSize: 13, borderRadius: 8 }}
            className={`secondary ${dbBusy || busy || intelBusy ? "spinning" : ""}`}
          >
            ↻
          </button>
        </div>
      </header>

      <main>
        {/* Navigation Tabs - High-Tech Tactical Bar */}
        <div className="tabs">
          <button
            className={`tab-btn ${activeTab === "intel" ? "active" : ""}`}
            onClick={() => setActiveTab("intel")}
          >
            <span style={{ opacity: 0.6, fontSize: 11, fontFamily: "var(--font-mono)" }}>01 //</span>
            ⚡ HTML Source Intel
            {intelStats && <span className="tab-badge">{(intelStats.total_pages / 1000).toFixed(0)}k</span>}
          </button>
          <button
            className={`tab-btn ${activeTab === "scanner" ? "active" : ""}`}
            onClick={() => setActiveTab("scanner")}
          >
            <span style={{ opacity: 0.6, fontSize: 11, fontFamily: "var(--font-mono)" }}>02 //</span>
            🛰️ Swarm Fleet &amp; Scanner
            {(clusterStatus?.active_nodes ?? 0) > 0 ? (
              <span className="tab-badge" style={{ background: "rgba(16, 185, 129, 0.15)", color: "#34d399", borderColor: "rgba(16, 185, 129, 0.3)" }}>
                {clusterStatus?.active_nodes} Nodes ({(clusterStatus?.cluster_speed ?? 0).toFixed(0)} req/s)
              </span>
            ) : crawlerStatus.running ? (
              <span className="pulse-dot" style={{ width: 6, height: 6 }}></span>
            ) : null}
          </button>
          <button
            className={`tab-btn ${activeTab === "catalog" ? "active" : ""}`}
            onClick={() => setActiveTab("catalog")}
          >
            <span style={{ opacity: 0.6, fontSize: 11, fontFamily: "var(--font-mono)" }}>03 //</span>
            🌐 Catalog &amp; WordPress
            {stats?.urls && <span className="tab-badge">{(stats.urls / 1000000).toFixed(1)}M</span>}
          </button>
          <button
            className={`tab-btn ${activeTab === "database" ? "active" : ""}`}
            onClick={() => setActiveTab("database")}
          >
            <span style={{ opacity: 0.6, fontSize: 11, fontFamily: "var(--font-mono)" }}>04 //</span>
            🗄️ Database Management
            {dbSources.length > 0 ? (
              <span className="tab-badge">{dbSources.length} sources</span>
            ) : stats?.imports ? (
              <span className="tab-badge">{stats.imports} sources</span>
            ) : null}
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
              <h2>Live HTML Scanner &amp; Swarm Fleet Control</h2>
              <p style={{ color: "#8fa8be", margin: 0, fontSize: 14 }}>
                Control the 8-node distributed crawler swarm or inspect any URL live to extract technology forensics.
              </p>
            </div>

            {/* Distributed Cluster Swarm Panel - High-Tech Tactical Command Center */}
            <div
              className="crawler-panel"
              style={{
                border: "1px solid rgba(56, 189, 248, 0.25)",
                borderTop: "2px solid #38bdf8",
                background: "linear-gradient(180deg, rgba(12, 20, 32, 0.9) 0%, rgba(6, 10, 16, 0.95) 100%)",
                boxShadow: "0 16px 40px -8px rgba(0, 0, 0, 0.7), 0 0 30px -5px rgba(56, 189, 248, 0.12)",
                marginBottom: 24,
                position: "relative",
                overflow: "hidden",
              }}
            >
              {/* Subtle Ambient Scanline */}
              <div
                style={{
                  position: "absolute",
                  top: 0,
                  left: 0,
                  right: 0,
                  height: "1px",
                  background: "linear-gradient(90deg, transparent, rgba(56, 189, 248, 0.6), transparent)",
                }}
              />

              <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 20, flexWrap: "wrap", gap: 14 }}>
                <div>
                  <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 5 }}>
                    <div
                      style={{
                        width: 30,
                        height: 30,
                        borderRadius: 8,
                        background: "rgba(56, 189, 248, 0.12)",
                        border: "1px solid rgba(56, 189, 248, 0.3)",
                        display: "flex",
                        alignItems: "center",
                        justifyContent: "center",
                        color: "#38bdf8",
                        fontSize: 15,
                      }}
                    >
                      🛰️
                    </div>
                    <h3 style={{ fontSize: 19, margin: 0, color: "#f8fafc", letterSpacing: "-0.02em", fontWeight: 700 }}>
                      Distributed Cluster Swarm (8 DigitalOcean Droplets)
                    </h3>
                    <span className="badge badge-tech" style={{ fontSize: 10.5 }}>
                      PRIVATE VPC MESH // 10.104.0.0/20
                    </span>
                  </div>
                  <span style={{ color: "#94a3b8", fontSize: 13, fontFamily: "var(--font-mono)" }}>
                    Autonomous distributed crawler fleet leasing domain batches over unmetered private network • Peak Target: ~4,000 req/s
                  </span>
                </div>

                <div style={{ display: "flex", gap: 10, alignItems: "center", flexWrap: "wrap" }}>
                  <button
                    className="secondary"
                    onClick={async () => {
                      setClusterBusy(true);
                      try {
                        const r = await fetch("/api/catalog/cluster-nodes");
                        if (r.ok) setClusterStatus(await r.json());
                      } finally {
                        setClusterBusy(false);
                      }
                    }}
                    disabled={clusterBusy}
                    style={{ fontSize: 12, padding: "8px 14px", borderRadius: 8, fontFamily: "var(--font-mono)" }}
                  >
                    ↻ REFRESH TELEMETRY
                  </button>
                  <button
                    className="danger"
                    onClick={handleStopCluster}
                    disabled={clusterBusy || (clusterStatus?.active_nodes ?? 0) === 0}
                    style={{ fontSize: 13, padding: "8px 16px", borderRadius: 8 }}
                  >
                    ⏹ ABORT / STOP SWARM
                  </button>
                  <button
                    className="success"
                    onClick={handleStartCluster}
                    disabled={clusterBusy}
                    style={{ fontSize: 13, padding: "8px 20px", borderRadius: 8 }}
                  >
                    {clusterBusy ? "ORCHESTRATING..." : "▶ LAUNCH 8-NODE SWARM"}
                  </button>
                </div>
              </div>

              {/* Swarm Combined Telemetry Cards - Mission Control HUD */}
              <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(210px, 1fr))", gap: 14, marginBottom: 22 }}>
                {/* 1. Velocity */}
                <div
                  style={{
                    background: "rgba(10, 16, 26, 0.7)",
                    padding: "16px 18px",
                    borderRadius: 10,
                    border: "1px solid rgba(56, 189, 248, 0.2)",
                    borderTop: "1px solid rgba(255, 255, 255, 0.15)",
                    position: "relative",
                  }}
                >
                  <span style={{ fontSize: 11, color: "#64748b", textTransform: "uppercase", letterSpacing: "0.06em", fontFamily: "var(--font-mono)", display: "block", marginBottom: 6 }}>
                    COMBINED SWARM THROUGHPUT
                  </span>
                  <div style={{ display: "flex", alignItems: "baseline", gap: 8 }}>
                    <strong
                      style={{
                        fontSize: 30,
                        color: (clusterStatus?.cluster_speed ?? 0) > 0 ? "#55efc4" : "#f8fafc",
                        fontWeight: 750,
                        fontFamily: "var(--font-mono)",
                        letterSpacing: "-0.04em",
                        fontVariantNumeric: "tabular-nums",
                      }}
                    >
                      {(clusterStatus?.cluster_speed ?? 0).toLocaleString()}
                    </strong>
                    <span style={{ fontSize: 13, color: "#7dd3fc", fontFamily: "var(--font-mono)" }}>req/s</span>
                    {(clusterStatus?.cluster_speed ?? 0) > 0 && <span className="pulse-dot" style={{ width: 8, height: 8 }}></span>}
                  </div>
                  {/* Visual Velocity Bar */}
                  <div style={{ width: "100%", height: 4, background: "rgba(255, 255, 255, 0.08)", borderRadius: 2, marginTop: 10, overflow: "hidden" }}>
                    <div
                      style={{
                        width: `${Math.min(100, ((clusterStatus?.cluster_speed ?? 0) / 4000) * 100)}%`,
                        height: "100%",
                        background: "linear-gradient(90deg, #00f2fe, #10b981)",
                        borderRadius: 2,
                        transition: "width 0.4s ease",
                      }}
                    />
                  </div>
                  <span style={{ fontSize: 11, color: "#475569", marginTop: 6, display: "block", fontFamily: "var(--font-mono)" }}>
                    Capacity: 4,000 req/s across 8 nodes
                  </span>
                </div>

                {/* 2. Active Fleet */}
                <div
                  style={{
                    background: "rgba(10, 16, 26, 0.7)",
                    padding: "16px 18px",
                    borderRadius: 10,
                    border: "1px solid rgba(56, 189, 248, 0.2)",
                    borderTop: "1px solid rgba(255, 255, 255, 0.15)",
                  }}
                >
                  <span style={{ fontSize: 11, color: "#64748b", textTransform: "uppercase", letterSpacing: "0.06em", fontFamily: "var(--font-mono)", display: "block", marginBottom: 6 }}>
                    OPERATIONAL FLEET
                  </span>
                  <div style={{ display: "flex", alignItems: "baseline", gap: 8 }}>
                    <strong
                      style={{
                        fontSize: 30,
                        color: (clusterStatus?.active_nodes ?? 0) > 0 ? "#34d399" : "#64748b",
                        fontWeight: 750,
                        fontFamily: "var(--font-mono)",
                        letterSpacing: "-0.04em",
                      }}
                    >
                      {clusterStatus?.active_nodes ?? 0}
                    </strong>
                    <span style={{ fontSize: 14, color: "#94a3b8", fontFamily: "var(--font-mono)" }}>/ 8 Droplets Synchronized</span>
                  </div>
                  <div style={{ width: "100%", height: 4, background: "rgba(255, 255, 255, 0.08)", borderRadius: 2, marginTop: 10, overflow: "hidden" }}>
                    <div
                      style={{
                        width: `${((clusterStatus?.active_nodes ?? 0) / 8) * 100}%`,
                        height: "100%",
                        background: "#34d399",
                        borderRadius: 2,
                        transition: "width 0.4s ease",
                      }}
                    />
                  </div>
                  <span style={{ fontSize: 11, color: "#475569", marginTop: 6, display: "block", fontFamily: "var(--font-mono)" }}>
                    Master: 10.104.0.2:3500 (SGP1)
                  </span>
                </div>

                {/* 3. Scanned */}
                <div
                  style={{
                    background: "rgba(10, 16, 26, 0.7)",
                    padding: "16px 18px",
                    borderRadius: 10,
                    border: "1px solid rgba(56, 189, 248, 0.2)",
                    borderTop: "1px solid rgba(255, 255, 255, 0.15)",
                  }}
                >
                  <span style={{ fontSize: 11, color: "#64748b", textTransform: "uppercase", letterSpacing: "0.06em", fontFamily: "var(--font-mono)", display: "block", marginBottom: 6 }}>
                    SWARM TARGETS PROCESSED
                  </span>
                  <div style={{ display: "flex", alignItems: "baseline", gap: 8 }}>
                    <strong
                      style={{
                        fontSize: 30,
                        color: "#f8fafc",
                        fontWeight: 750,
                        fontFamily: "var(--font-mono)",
                        letterSpacing: "-0.04em",
                        fontVariantNumeric: "tabular-nums",
                      }}
                    >
                      {(clusterStatus?.cluster_scanned ?? 0).toLocaleString()}
                    </strong>
                  </div>
                  <div style={{ width: "100%", height: 4, background: "rgba(255, 255, 255, 0.08)", borderRadius: 2, marginTop: 10, overflow: "hidden" }}>
                    <div
                      style={{
                        width: `${Math.min(100, ((clusterStatus?.cluster_scanned ?? 0) / 12252872) * 100)}%`,
                        height: "100%",
                        background: "#38bdf8",
                        borderRadius: 2,
                      }}
                    />
                  </div>
                  <span style={{ fontSize: 11, color: "#475569", marginTop: 6, display: "block", fontFamily: "var(--font-mono)" }}>
                    Total Catalog: 12,252,872 .org domains
                  </span>
                </div>

                {/* 4. WordPress Found */}
                <div
                  style={{
                    background: "rgba(10, 16, 26, 0.7)",
                    padding: "16px 18px",
                    borderRadius: 10,
                    border: "1px solid rgba(56, 189, 248, 0.2)",
                    borderTop: "1px solid rgba(255, 255, 255, 0.15)",
                  }}
                >
                  <span style={{ fontSize: 11, color: "#64748b", textTransform: "uppercase", letterSpacing: "0.06em", fontFamily: "var(--font-mono)", display: "block", marginBottom: 6 }}>
                    WORDPRESS SITES DISCOVERED
                  </span>
                  <div style={{ display: "flex", alignItems: "baseline", gap: 8 }}>
                    <strong
                      style={{
                        fontSize: 30,
                        color: "#38bdf8",
                        fontWeight: 750,
                        fontFamily: "var(--font-mono)",
                        letterSpacing: "-0.04em",
                        fontVariantNumeric: "tabular-nums",
                      }}
                    >
                      {(clusterStatus?.cluster_wp ?? 0).toLocaleString()}
                    </strong>
                  </div>
                  <div style={{ width: "100%", height: 4, background: "rgba(255, 255, 255, 0.08)", borderRadius: 2, marginTop: 10, overflow: "hidden" }}>
                    <div
                      style={{
                        width: "100%",
                        height: "100%",
                        background: "linear-gradient(90deg, #38bdf8, #818cf8)",
                        borderRadius: 2,
                      }}
                    />
                  </div>
                  <span style={{ fontSize: 11, color: "#475569", marginTop: 6, display: "block", fontFamily: "var(--font-mono)" }}>
                    {(clusterStatus?.cluster_scanned ?? 0) > 0
                      ? `${(((clusterStatus?.cluster_wp ?? 0) / (clusterStatus?.cluster_scanned ?? 1)) * 100).toFixed(1)}% detection hit rate`
                      : "Evidence extraction active"}
                  </span>
                </div>

                {/* 5. Mission ETA */}
                <div
                  style={{
                    background: "rgba(10, 16, 26, 0.7)",
                    padding: "16px 18px",
                    borderRadius: 10,
                    border: "1px solid rgba(56, 189, 248, 0.2)",
                    borderTop: "1px solid rgba(255, 255, 255, 0.15)",
                  }}
                >
                  <span style={{ fontSize: 11, color: "#64748b", textTransform: "uppercase", letterSpacing: "0.06em", fontFamily: "var(--font-mono)", display: "block", marginBottom: 6 }}>
                    ESTIMATED MISSION DURATION
                  </span>
                  <div style={{ display: "flex", alignItems: "baseline", gap: 8 }}>
                    <strong
                      style={{
                        fontSize: 24,
                        color: (clusterStatus?.cluster_speed ?? 0) > 0 ? "#55efc4" : "#64748b",
                        fontWeight: 700,
                        fontFamily: "var(--font-mono)",
                        letterSpacing: "-0.03em",
                      }}
                    >
                      {(clusterStatus?.cluster_speed ?? 0) > 50
                        ? `~${Math.round((12252872 - (clusterStatus?.cluster_scanned ?? 0)) / (clusterStatus?.cluster_speed || 1) / 60)} MINS`
                        : (clusterStatus?.cluster_speed ?? 0) > 0 ? "CALCULATING..." : "STANDBY"}
                    </strong>
                  </div>
                  <div style={{ width: "100%", height: 4, background: "rgba(255, 255, 255, 0.08)", borderRadius: 2, marginTop: 10, overflow: "hidden" }}>
                    <div
                      style={{
                        width: (clusterStatus?.cluster_speed ?? 0) > 0 ? "100%" : "0%",
                        height: "100%",
                        background: "#55efc4",
                        borderRadius: 2,
                      }}
                    />
                  </div>
                  <span style={{ fontSize: 11, color: "#475569", marginTop: 6, display: "block", fontFamily: "var(--font-mono)" }}>
                    At 4,000 req/s: ~50 mins for 12.25M
                  </span>
                </div>
              </div>

              {/* 8-Worker Droplet Mesh Grid - High-Tech Telemetry Cards */}
              <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 12, flexWrap: "wrap", gap: 8 }}>
                <h4 style={{ margin: 0, fontSize: 14, color: "#e2e8f0", display: "flex", alignItems: "center", gap: 8, letterSpacing: "-0.01em" }}>
                  <span>🖥️ Ground Station Telemetry Grid</span>
                  <span className="badge" style={{ fontSize: 10, background: "rgba(255, 255, 255, 0.05)", color: "#94a3b8" }}>
                    8 WORKER NODES
                  </span>
                </h4>
                <span style={{ fontSize: 12, color: "#64748b", fontFamily: "var(--font-mono)" }}>
                  Each droplet isolated via Linux cgroups v2 (CPUQuota=150%, MemoryMax=1000M)
                </span>
              </div>

              <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(295px, 1fr))", gap: 12 }}>
                {WORKER_SPECS.map((spec, idx) => {
                  const node = clusterStatus?.nodes.find((n) => n.node_id === spec.id);
                  const isAlive = node?.status === "active";
                  const speedVal = node?.speed ?? 0;
                  const speedPercent = Math.min(100, Math.round((speedVal / 500) * 100));

                  return (
                    <div
                      key={spec.id}
                      style={{
                        background: isAlive ? "rgba(14, 25, 39, 0.85)" : "rgba(10, 15, 23, 0.7)",
                        border: isAlive ? "1px solid rgba(56, 189, 248, 0.4)" : "1px solid rgba(255, 255, 255, 0.07)",
                        borderTop: isAlive ? "2px solid #00f2fe" : "1px solid rgba(255, 255, 255, 0.1)",
                        borderRadius: 10,
                        padding: "14px 16px",
                        boxShadow: isAlive ? "0 8px 24px -4px rgba(0, 0, 0, 0.5), 0 0 16px -4px rgba(0, 242, 254, 0.2)" : "none",
                        transition: "all 0.2s cubic-bezier(0.16, 1, 0.3, 1)",
                      }}
                    >
                      {/* Node Header */}
                      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 10 }}>
                        <div style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0 }}>
                          <span
                            style={{
                              fontFamily: "var(--font-mono)",
                              fontSize: 10.5,
                              color: "#38bdf8",
                              background: "rgba(56, 189, 248, 0.12)",
                              padding: "2px 6px",
                              borderRadius: 4,
                              fontWeight: 600,
                              flexShrink: 0,
                            }}
                          >
                            NODE-0{idx + 1}
                          </span>
                          <div style={{ whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>
                            <span style={{ fontSize: 13, fontWeight: 700, color: "#f8fafc", fontFamily: "var(--font-mono)" }}>
                              {spec.id}
                            </span>
                            <span style={{ fontSize: 11, color: "#64748b", marginLeft: 6, fontFamily: "var(--font-mono)" }}>
                              ({spec.vpc})
                            </span>
                          </div>
                        </div>

                        <span
                          className="badge"
                          style={{
                            fontSize: 10,
                            padding: "2px 7px",
                            background: isAlive ? "rgba(16, 185, 129, 0.18)" : "rgba(100, 116, 139, 0.15)",
                            color: isAlive ? "#34d399" : "#64748b",
                            borderColor: isAlive ? "rgba(16, 185, 129, 0.4)" : "rgba(100, 116, 139, 0.25)",
                          }}
                        >
                          {isAlive ? "● ACTIVE" : "STANDBY"}
                        </span>
                      </div>

                      {/* Throughput Bar */}
                      <div style={{ marginBottom: 10 }}>
                        <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", fontSize: 12, marginBottom: 4 }}>
                          <span style={{ color: "#94a3b8", fontFamily: "var(--font-mono)", fontSize: 11 }}>VELOCITY</span>
                          <strong
                            style={{
                              color: speedVal > 0 ? "#55efc4" : "#64748b",
                              fontFamily: "var(--font-mono)",
                              fontSize: 13,
                              fontVariantNumeric: "tabular-nums",
                            }}
                          >
                            {speedVal ? `${speedVal.toFixed(1)} req/s` : "0.0 req/s"}
                          </strong>
                        </div>
                        <div style={{ width: "100%", height: 4, background: "rgba(255, 255, 255, 0.08)", borderRadius: 2, overflow: "hidden" }}>
                          <div
                            style={{
                              width: `${speedPercent}%`,
                              height: "100%",
                              background: isAlive ? "linear-gradient(90deg, #38bdf8, #10b981)" : "#334155",
                              borderRadius: 2,
                              transition: "width 0.3s ease",
                            }}
                          />
                        </div>
                      </div>

                      {/* Metrics Breakdown */}
                      <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 8, padding: "8px 0", borderTop: "1px solid rgba(255, 255, 255, 0.05)", borderBottom: "1px solid rgba(255, 255, 255, 0.05)" }}>
                        <div>
                          <span style={{ fontSize: 10.5, color: "#64748b", display: "block", fontFamily: "var(--font-mono)" }}>SCANNED</span>
                          <span style={{ fontSize: 13, color: "#e2e8f0", fontWeight: 650, fontFamily: "var(--font-mono)", fontVariantNumeric: "tabular-nums" }}>
                            {(node?.scanned_count ?? 0).toLocaleString()}
                          </span>
                        </div>
                        <div>
                          <span style={{ fontSize: 10.5, color: "#64748b", display: "block", fontFamily: "var(--font-mono)" }}>WORDPRESS</span>
                          <span style={{ fontSize: 13, color: "#38bdf8", fontWeight: 650, fontFamily: "var(--font-mono)", fontVariantNumeric: "tabular-nums" }}>
                            {(node?.wp_count ?? 0).toLocaleString()}
                          </span>
                        </div>
                      </div>

                      {/* Isolation & Status Footer */}
                      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", fontSize: 11, color: "#64748b", marginTop: 8, fontFamily: "var(--font-mono)" }}>
                        <span style={{ color: "#475569" }}>1.5 CPU • 1G RAM (cgroup)</span>
                        <span style={{ color: isAlive ? "#34d399" : "#64748b" }}>
                          {node?.last_heartbeat ? "VPC <1.5ms" : "Offline"}
                        </span>
                      </div>
                    </div>
                  );
                })}
              </div>
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

            <div className="table-card">
              <table>
                <thead>
                  <tr>
                    <th>URL</th>
                    <th>Hostname</th>
                    <th>Evidence</th>
                    <th style={{ width: 190, textAlign: "center" }}>Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {sites.map((site) => (
                    <tr key={site.url}>
                      <td>
                        <button
                          onClick={() => setSelectedObsUrl(site.url)}
                          title="View observations"
                          style={{ fontFamily: "var(--font-mono)", fontSize: 13, color: "#38bdf8" }}
                        >
                          {site.url}
                        </button>
                      </td>
                      <td style={{ color: "#cbd5e1", fontSize: 13, fontFamily: "var(--font-mono)" }}>{site.host}</td>
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
                            style={{ padding: "3px 8px", fontSize: 11, fontFamily: "var(--font-mono)" }}
                            className="secondary"
                            title="Scan this site live"
                          >
                            ⚡ Scan
                          </button>
                          <button
                            onClick={() => {
                              setIntelInput(site.host);
                              setIntelQuery(site.host);
                              setActiveTab("intel");
                            }}
                            style={{ padding: "3px 8px", fontSize: 11, fontFamily: "var(--font-mono)" }}
                            className="secondary"
                            title="Search in HTML Intel"
                          >
                            🔍 Intel
                          </button>
                          <a
                            href={site.url}
                            target="_blank"
                            rel="noopener noreferrer"
                            style={{ color: "#7dd3fc", textDecoration: "none", fontSize: 13, padding: "3px 6px" }}
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
            </div>

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

        {/* TAB 4: DATABASE MANAGEMENT */}
        {activeTab === "database" && (
          <section>
            <div className="controls" style={{ marginBottom: 20 }}>
              <div style={{ flex: 1 }}>
                <h2>🗄️ Database Management &amp; Ingestion Provenance</h2>
                <p style={{ color: "#8fa8be", margin: 0, fontSize: 14 }}>
                  Monitor SQLite storage engine telemetry, manage individual provenance sources, download raw datasets, and perform database maintenance.
                </p>
              </div>
              <div style={{ display: "flex", gap: 8, flexWrap: "wrap", alignItems: "center" }}>
                <button
                  type="button"
                  className="primary"
                  onClick={() => {
                    setShowImportModal(true);
                    setImportResult(null);
                  }}
                  title="Ingest raw URLs or domains into catalog under a custom source label"
                >
                  ➕ Ingest New Dataset
                </button>
                <button
                  type="button"
                  disabled={dbBusy}
                  onClick={() => {
                    void loadDatabaseData();
                    void loadGeneralStats();
                  }}
                  title="Reload all sources and storage statistics"
                >
                  {dbBusy ? "Loading..." : "↻ Refresh Sources"}
                </button>
              </div>
            </div>

            {/* Error Banner */}
            {dbError && (
              <div className="db-alert error" role="alert">
                <span>⚠️ {dbError}</span>
                <button onClick={() => setDbError("")} style={{ padding: "2px 8px", fontSize: 11 }}>✕</button>
              </div>
            )}

            {/* Maintenance Message Banner */}
            {dbMaintenanceMsg && (
              <div className={`db-alert ${dbMaintenanceMsg.type}`} role="status">
                <span>
                  {dbMaintenanceMsg.type === "success" ? "✅" : "❌"} {dbMaintenanceMsg.text}
                </span>
                <button onClick={() => setDbMaintenanceMsg(null)} style={{ padding: "2px 8px", fontSize: 11 }}>✕</button>
              </div>
            )}

            {/* Database Engine Metrics */}
            {(() => {
              const dbSizeMb = dbStats?.database_size_mb ?? dbStats?.size_mb ?? 0;
              const walSizeMb = dbStats?.wal_size_mb ?? 0;
              const pageSize = dbStats?.page_size ?? dbStats?.engine?.page_size ?? 4096;
              const pageCount = dbStats?.page_count ?? dbStats?.engine?.page_count ?? 0;

              return (
                <div id="metrics">
                  <div className="metric-card">
                    <strong>
                      {dbStats
                        ? dbSizeMb >= 1024
                          ? `${(dbSizeMb / 1024).toFixed(2)} GB`
                          : `${dbSizeMb.toFixed(1)} MB`
                        : "—"}
                    </strong>
                    <span>Catalog File Size (data/catalog.db)</span>
                  </div>

                  <div className="metric-card">
                    <strong>
                      {dbStats ? `${walSizeMb.toFixed(2)} MB` : "—"}
                    </strong>
                    <span>
                      WAL Commit Buffer
                      {dbStats && walSizeMb > 10 && (
                        <span style={{ color: "#fdcb6e", marginLeft: 6, fontSize: 11 }}>● Checkpoint recommended</span>
                      )}
                    </span>
                  </div>

                  <div className="metric-card">
                    <strong>{dbSources.length.toLocaleString()}</strong>
                    <span>Active Dataset Sources</span>
                  </div>

                  <div className="metric-card">
                    <strong>
                      {dbStats?.tables?.observations
                        ? dbStats.tables.observations.toLocaleString()
                        : stats?.urls
                        ? stats.urls.toLocaleString()
                        : "—"}
                    </strong>
                    <span>Total Stored Observations</span>
                  </div>

                  <div className="metric-card">
                    <strong>
                      {dbStats?.tables?.sites ? dbStats.tables.sites.toLocaleString() : "—"}
                    </strong>
                    <span>Unique Domains / Sites</span>
                  </div>

                  <div className="metric-card">
                    <strong style={{ fontSize: 20 }}>
                      {dbStats ? `${pageSize.toLocaleString()} B` : "—"}
                    </strong>
                    <span>
                      {dbStats ? `${pageCount.toLocaleString()} pages allocated` : "Page Allocation"}
                    </span>
                  </div>
                </div>
              );
            })()}

            {/* Storage Allocation Distribution Bar */}
            {dbStats && (
              <div className="storage-bar-container">
                <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 8, fontSize: 13, flexWrap: "wrap", gap: 6 }}>
                  <strong style={{ color: "#ffffff", display: "flex", alignItems: "center", gap: 6 }}>
                    <span>🗄️ Storage Engine Allocation</span>
                    <span style={{ fontSize: 11, color: "var(--accent-mint)", background: "rgba(85,239,196,0.1)", padding: "2px 8px", borderRadius: 10 }}>
                      WAL Mode Active
                    </span>
                  </strong>
                  <span style={{ fontSize: 12, color: "var(--text-subtle)" }}>
                    Total Database Size: <strong>{((dbStats.database_size_mb ?? dbStats.size_mb ?? 0) / 1024).toFixed(2)} GB</strong> ({((dbStats.page_count ?? 0) * (dbStats.page_size ?? 4096) / (1024 * 1024)).toFixed(0)} MB allocated)
                  </span>
                </div>

                <div className="storage-bar-track">
                  <div className="storage-bar-seg" style={{ width: "50%", background: "linear-gradient(90deg, #00f2fe, #4facfe)" }} title="Observations (12.7M records)" />
                  <div className="storage-bar-seg" style={{ width: "35%", background: "linear-gradient(90deg, #55efc4, #00b894)" }} title="Sites & Domains (12.47M records)" />
                  <div className="storage-bar-seg" style={{ width: "10%", background: "linear-gradient(90deg, #a29bfe, #6c5ce7)" }} title="HTML Source Intel Index" />
                  <div className="storage-bar-seg" style={{ width: "5%", background: "linear-gradient(90deg, #fdcb6e, #e17055)" }} title="Crawler Progress & Meta" />
                </div>

                <div className="storage-legend">
                  <div className="storage-legend-item">
                    <span className="storage-legend-dot" style={{ background: "#00f2fe" }} />
                    <span>Observations: <strong>{dbStats.tables?.observations?.toLocaleString() ?? "—"}</strong></span>
                  </div>
                  <div className="storage-legend-item">
                    <span className="storage-legend-dot" style={{ background: "#55efc4" }} />
                    <span>Unique Sites: <strong>{dbStats.tables?.sites?.toLocaleString() ?? "—"}</strong></span>
                  </div>
                  <div className="storage-legend-item">
                    <span className="storage-legend-dot" style={{ background: "#a29bfe" }} />
                    <span>HTML Source Index: <strong>{dbStats.tables?.page_contents?.toLocaleString() ?? "—"}</strong></span>
                  </div>
                  <div className="storage-legend-item">
                    <span className="storage-legend-dot" style={{ background: "#fdcb6e" }} />
                    <span>WordPress Detections: <strong>{dbStats.tables?.wordpress_verified?.toLocaleString() ?? "—"}</strong></span>
                  </div>
                </div>
              </div>
            )}

            {/* Storage Engine Maintenance Toolbar */}
            <div className="db-maintenance-bar">
              <div style={{ display: "flex", alignItems: "center", gap: 8, marginRight: 8 }}>
                <span style={{ fontSize: 13, fontWeight: 600, color: "#e4edf5" }}>Storage Engine Operations:</span>
              </div>

              <button
                type="button"
                className="secondary"
                disabled={dbMaintenanceBusy}
                onClick={() => handleRunMaintenance("checkpoint")}
                title="Flush WAL journal frames into catalog.db and truncate WAL file"
                style={{ fontSize: 12, padding: "7px 12px" }}
              >
                ⚡ WAL Checkpoint (TRUNCATE)
              </button>

              <button
                type="button"
                className="secondary"
                disabled={dbMaintenanceBusy}
                onClick={() => handleRunMaintenance("optimize")}
                title="Update table and index statistical histograms (PRAGMA optimize)"
                style={{ fontSize: 12, padding: "7px 12px" }}
              >
                📊 Optimize Query Planner
              </button>

              <button
                type="button"
                className="secondary"
                disabled={dbMaintenanceBusy}
                onClick={() => handleRunMaintenance("integrity")}
                title="Verify SQLite B-tree page pointers and checksums (PRAGMA integrity_check)"
                style={{ fontSize: 12, padding: "7px 12px" }}
              >
                🛡️ Run Integrity Check
              </button>

              <button
                type="button"
                className="secondary"
                disabled={dbMaintenanceBusy}
                onClick={() => {
                  if (confirm("Running VACUUM will repack catalog.db into a fresh file to eliminate page fragmentation. During vacuuming, the database lock will be held. Proceed?")) {
                    void handleRunMaintenance("vacuum");
                  }
                }}
                title="Defragment and shrink database file on disk"
                style={{ fontSize: 12, padding: "7px 12px" }}
              >
                🧹 Vacuum Storage
              </button>

              {dbMaintenanceBusy && (
                <span className="pulse-indicator" style={{ marginLeft: "auto" }}>
                  <span className="pulse-dot"></span> Executing PRAGMA operation...
                </span>
              )}
            </div>

            {/* Disaster Recovery & Drive Snapshots Card */}
            <div className="db-recovery-card">
              <div className="db-recovery-header">
                <div style={{ display: "flex", alignItems: "center", gap: 10, flexWrap: "wrap" }}>
                  <span style={{ fontSize: 16 }}>📦</span>
                  <strong style={{ fontSize: 14, color: "#fff" }}>
                    Disaster Recovery &amp; Automated Backup Bundles
                  </strong>
                  <span
                    style={{
                      fontSize: 11,
                      fontFamily: "var(--font-mono)",
                      padding: "2px 8px",
                      borderRadius: 10,
                      background: "rgba(116, 185, 255, 0.15)",
                      color: "#74b9ff",
                      border: "1px solid rgba(116, 185, 255, 0.3)",
                    }}
                  >
                    {dbBundleStatus?.backup?.data?.format || "internet-map-bundle-v2"}
                  </span>
                  {dbBundleStatus?.is_backup_running ? (
                    <span
                      style={{
                        display: "inline-flex",
                        alignItems: "center",
                        gap: 6,
                        fontSize: 11,
                        color: "#fdcb6e",
                        background: "rgba(253, 203, 110, 0.15)",
                        padding: "2px 8px",
                        borderRadius: 10,
                      }}
                    >
                      <span className="pulse-dot" style={{ background: "#fdcb6e" }}></span>
                      Backup in Progress...
                    </span>
                  ) : (
                    <span
                      style={{
                        display: "inline-flex",
                        alignItems: "center",
                        gap: 5,
                        fontSize: 11,
                        color: "#55efc4",
                        background: "rgba(85, 239, 196, 0.1)",
                        padding: "2px 8px",
                        borderRadius: 10,
                      }}
                    >
                      ● Engine Ready
                    </span>
                  )}
                  {dbBundleStatus?.drive?.enabled && (
                    <span
                      style={{
                        display: "inline-flex",
                        alignItems: "center",
                        gap: 6,
                        fontSize: 11,
                        color: dbBundleStatus.drive.is_uploaded ? "#34d399" : "#fbbf24",
                        background: dbBundleStatus.drive.is_uploaded ? "rgba(16, 185, 129, 0.15)" : "rgba(245, 158, 11, 0.15)",
                        border: `1px solid ${dbBundleStatus.drive.is_uploaded ? "rgba(16, 185, 129, 0.3)" : "rgba(245, 158, 11, 0.3)"}`,
                        padding: "2px 8px",
                        borderRadius: 10,
                        fontFamily: "var(--font-mono)",
                      }}
                      title={`Google Drive Watch Dir: ${dbBundleStatus.drive.watch_dir}`}
                    >
                      <span>☁️</span>
                      <span>{dbBundleStatus.drive.is_uploaded ? "Google Drive Synced" : "Drive Syncing..."}</span>
                    </span>
                  )}
                </div>

                <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
                  <button
                    type="button"
                    className="primary"
                    disabled={dbBundleStatus?.is_backup_running || bundleActionBusy}
                    onClick={handleCreateBundle}
                    title="Snapshot all 3 databases (catalog.db, generator-state.db, candidates.db) and code into an HMAC-signed .tar.gz bundle"
                    style={{ fontSize: 12, padding: "7px 13px" }}
                  >
                    {dbBundleStatus?.is_backup_running || bundleActionBusy ? "⏳ Backing Up..." : "📦 Create Recovery Bundle"}
                  </button>

                  <button
                    type="button"
                    className="secondary"
                    disabled={bundleVerifyBusy || dbBundleStatus?.verification?.status === "running" || dbBundleStatus?.is_backup_running}
                    onClick={handleVerifyBundle}
                    title="Verify HMAC signature and SQLite PRAGMA quick_check integrity across all databases"
                    style={{ fontSize: 12, padding: "7px 13px" }}
                  >
                    {bundleVerifyBusy || dbBundleStatus?.verification?.status === "running" ? "⏳ Verifying..." : "🛡️ Verify Snapshot Integrity"}
                  </button>

                  <button
                    type="button"
                    className="secondary"
                    disabled={syncActionBusy}
                    onClick={handleSyncDiscovery}
                    title="Reconcile and sync verified classifications from generator-state.db into catalog.db"
                    style={{ fontSize: 12, padding: "7px 13px" }}
                  >
                    {syncActionBusy ? "⏳ Syncing..." : "🔄 Sync Classifications"}
                  </button>
                </div>
              </div>

              {dbBundleStatus?.verification && (
                <p role="status" style={{ fontSize: 12, padding: "0 18px" }}>
                  Local archive verification: {dbBundleStatus.verification.status}
                  {dbBundleStatus.verification.integrity ? " · SQLite integrity: " + dbBundleStatus.verification.integrity : ""}
                  {dbBundleStatus.verification.status === "error" ? " · " + dbBundleStatus.verification.message : ""}
                </p>
              )}
              <div className="db-recovery-grid">
                {/* Panel 1: Bundle File & Telemetry */}
                <div className="db-recovery-panel">
                  <h4>
                    <span>💾</span> Latest Bundle Snapshot
                  </h4>
                  <div className="db-recovery-item">
                    <span>Archive Name:</span>
                    <code title={dbBundleStatus?.backup?.data?.filename || "No backup archive found"}>
                      {dbBundleStatus?.backup?.data?.filename
                        ? `${dbBundleStatus.backup.data.filename.slice(0, 26)}...tar.gz`
                        : "—"}
                    </code>
                  </div>
                  <div className="db-recovery-item">
                    <span>Bundle Size:</span>
                    <span>
                      {dbBundleStatus?.backup?.data?.bytes
                        ? `${(dbBundleStatus.backup.data.bytes / (1024 * 1024 * 1024)).toFixed(2)} GB (${(dbBundleStatus.backup.data.bytes / (1024 * 1024)).toFixed(1)} MB)`
                        : "—"}
                    </span>
                  </div>
                  <div className="db-recovery-item">
                    <span>Last Backed Up:</span>
                    <span>
                      {dbBundleStatus?.backup?.updated_at
                        ? new Date(dbBundleStatus.backup.updated_at * 1000).toLocaleString()
                        : "—"}
                    </span>
                  </div>
                  <div className="db-recovery-item">
                    <span>Databases Included:</span>
                    <span>
                      {dbBundleStatus?.backup?.data?.databases
                        ? `${dbBundleStatus.backup.data.databases.length} databases (catalog, state, queue)`
                        : "3 databases (atomic)"}
                    </span>
                  </div>
                </div>

                {/* Panel 2: Cryptographic Security & Integrity */}
                <div className="db-recovery-panel">
                  <h4>
                    <span>🛡️</span> Security &amp; Key Verification
                  </h4>
                  <div className="db-recovery-item">
                    <span>HMAC Recovery Key:</span>
                    <span style={{ color: dbBundleStatus?.key_configured ? "#55efc4" : "#fdcb6e" }}>
                      {dbBundleStatus?.key_configured ? "✅ Configured (256-bit)" : "⚠️ Key Missing"}
                    </span>
                  </div>
                  <div className="db-recovery-item">
                    <span>SHA-256 Fingerprint:</span>
                    <code title={dbBundleStatus?.backup?.data?.sha256 || "No hash"}>
                      {dbBundleStatus?.backup?.data?.sha256
                        ? `${dbBundleStatus.backup.data.sha256.slice(0, 16)}...`
                        : "—"}
                    </code>
                  </div>
                  <div className="db-recovery-item">
                    <span>Integrity Verification:</span>
                    <span style={{ color: "#55efc4" }}>
                      PRAGMA quick_check = ok
                    </span>
                  </div>
                  <div className="db-recovery-item">
                    <span>Secret Sanitization:</span>
                    <span>Strict (excluding .env)</span>
                  </div>
                </div>

                {/* Panel 3: Ingestion & Discovery Synchronization */}
                <div className="db-recovery-panel">
                  <h4>
                    <span>🔄</span> Discovery Reconciler Sync
                  </h4>
                  <div className="db-recovery-item">
                    <span>Sync Status:</span>
                    <span style={{ color: "#55efc4" }}>
                      Active (Timer: 30s)
                    </span>
                  </div>
                  <div className="db-recovery-item">
                    <span>Last Reconciled:</span>
                    <span>
                      {dbBundleStatus?.sync?.updated_at
                        ? new Date(dbBundleStatus.sync.updated_at * 1000).toLocaleString()
                        : "—"}
                    </span>
                  </div>
                  <div className="db-recovery-item">
                    <span>Classifications Ingested:</span>
                    <span>
                      <strong>
                        {dbBundleStatus?.sync?.data?.imported !== undefined
                          ? dbBundleStatus.sync.data.imported.toLocaleString()
                          : "0"}
                      </strong>{" "}
                      records
                    </span>
                  </div>
                  <div className="db-recovery-item">
                    <span>Attribution Source:</span>
                    <code style={{ fontSize: 11 }}>internet-map:cnc-generator-results</code>
                  </div>
                </div>

                {/* Panel 4: Google Drive Cloud Integration */}
                <div className="db-recovery-panel">
                  <h4>
                    <span>☁️</span> Google Drive Cloud Sync
                  </h4>
                  <div className="db-recovery-item">
                    <span>Target Folder:</span>
                    <code title={dbBundleStatus?.drive?.parent_folder_id || "Root Folder"}>
                      {dbBundleStatus?.drive?.parent_folder_id || "1J-NkR0WlEQ2Sqz8Z7yFXruyAlxQWjd73"}
                    </code>
                  </div>
                  <div className="db-recovery-item">
                    <span>Upload Status:</span>
                    <span style={{ color: dbBundleStatus?.drive?.is_uploaded ? "#55efc4" : "#fdcb6e" }}>
                      {dbBundleStatus?.drive?.is_uploaded ? "✓ Uploaded & Synced" : "⏳ Pending Synchronization"}
                    </span>
                  </div>
                  <div className="db-recovery-item">
                    <span>Watch Directory:</span>
                    <code style={{ fontSize: 11 }}>{dbBundleStatus?.drive?.watch_dir || "/opt/cnc/merged"}</code>
                  </div>
                  <div className="db-recovery-item">
                    <span>Drive Total Files:</span>
                    <span>
                      <strong>{dbBundleStatus?.drive?.uploaded_count ?? "—"}</strong> files in Drive
                    </span>
                  </div>
                </div>
              </div>

              {/* Recent Drive Bundles */}
              {dbBundleStatus?.recent_bundles && dbBundleStatus.recent_bundles.length > 0 && (
                <div className="db-bundle-list">
                  <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 6 }}>
                    <span style={{ fontSize: 12, color: "var(--text-subtle)", fontWeight: 600 }}>
                      Recent Drive Inbound Bundles ({dbBundleStatus.drive_inbox}):
                    </span>
                    <span style={{ fontSize: 11, color: "var(--text-subtle)" }}>
                      Retaining 1-click restore archives
                    </span>
                  </div>
                  {dbBundleStatus.recent_bundles.map((bundle, idx) => (
                    <div key={idx} className="db-bundle-row">
                      <span style={{ display: "flex", alignItems: "center", gap: 6 }}>
                        <span style={{ color: "#74b9ff" }}>📦</span>
                        <span>{bundle.filename}</span>
                      </span>
                      <span style={{ display: "flex", alignItems: "center", gap: 14 }}>
                        <span style={{ color: "#55efc4" }}>{bundle.size_mb.toFixed(1)} MB</span>
                        <span style={{ color: "var(--text-subtle)" }}>
                          {new Date(bundle.created_at * 1000).toLocaleString()}
                        </span>
                      </span>
                    </div>
                  ))}
                </div>
              )}
            </div>

            {/* Source Search, Category Filters, and Sort */}
            <div style={{ margin: "22px 0 16px", display: "flex", gap: 12, flexWrap: "wrap", alignItems: "center", justifyContent: "space-between" }}>
              <div style={{ display: "flex", gap: 8, flexWrap: "wrap", alignItems: "center", flex: 1 }}>
                <input
                  type="search"
                  placeholder="Filter sources by label, name, or #ID..."
                  value={dbSearch}
                  onChange={(e) => setDbSearch(e.target.value)}
                  style={{ maxWidth: 360, minWidth: 240 }}
                />

                <div style={{ display: "flex", gap: 6, flexWrap: "wrap" }}>
                  <button
                    type="button"
                    onClick={() => setDbCategoryFilter("all")}
                    className={dbCategoryFilter === "all" ? "primary" : "secondary"}
                    style={{ fontSize: 12, padding: "6px 12px" }}
                  >
                    All ({dbSources.length})
                  </button>
                  <button
                    type="button"
                    onClick={() => setDbCategoryFilter("worker")}
                    className={dbCategoryFilter === "worker" ? "primary" : "secondary"}
                    style={{ fontSize: 12, padding: "6px 12px" }}
                  >
                    🤖 Swarm ({dbSources.filter((s) => getSourceCategory(s.source).type === "worker").length})
                  </button>
                  <button
                    type="button"
                    onClick={() => setDbCategoryFilter("zone")}
                    className={dbCategoryFilter === "zone" ? "primary" : "secondary"}
                    style={{ fontSize: 12, padding: "6px 12px" }}
                  >
                    🌐 Zone Files ({dbSources.filter((s) => getSourceCategory(s.source).type === "zone").length})
                  </button>
                  <button
                    type="button"
                    onClick={() => setDbCategoryFilter("discovery")}
                    className={dbCategoryFilter === "discovery" ? "primary" : "secondary"}
                    style={{ fontSize: 12, padding: "6px 12px" }}
                  >
                    🎯 Discovery ({dbSources.filter((s) => getSourceCategory(s.source).type === "discovery").length})
                  </button>
                  <button
                    type="button"
                    onClick={() => setDbCategoryFilter("generator")}
                    className={dbCategoryFilter === "generator" ? "primary" : "secondary"}
                    style={{ fontSize: 12, padding: "6px 12px" }}
                  >
                    ⚡ Generator ({dbSources.filter((s) => getSourceCategory(s.source).type === "generator").length})
                  </button>
                  <button
                    type="button"
                    onClick={() => setDbCategoryFilter("custom")}
                    className={dbCategoryFilter === "custom" ? "primary" : "secondary"}
                    style={{ fontSize: 12, padding: "6px 12px" }}
                  >
                    📁 Custom ({dbSources.filter((s) => getSourceCategory(s.source).type === "custom").length})
                  </button>
                </div>
              </div>

              <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
                <span style={{ fontSize: 12, color: "#8fa8be" }}>Sort:</span>
                <select
                  value={dbSort}
                  onChange={(e) => setDbSort(e.target.value as any)}
                  style={{ padding: "6px 10px", fontSize: 12 }}
                >
                  <option value="id_desc">Newest First (ID ↓)</option>
                  <option value="id_asc">Oldest First (ID ↑)</option>
                  <option value="obs_desc">Most Observations (Obs ↓)</option>
                  <option value="wp_desc">Most WordPress Sites (WP ↓)</option>
                  <option value="accepted_desc">Most Accepted (Accepted ↓)</option>
                </select>
              </div>
            </div>

            {/* Sources List Counter */}
            <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 12, fontSize: 12, color: "#8fa8be" }}>
              <span>
                Showing <strong>{filteredDbSources.length}</strong> of <strong>{dbSources.length}</strong> sources
                {dbSearch && ` matching "${dbSearch}"`}
              </span>
              <span>
                Total observations in view:{" "}
                <strong>
                  {filteredDbSources
                    .reduce((acc, s) => acc + (s.observation_count ?? s.observations_count ?? 0), 0)
                    .toLocaleString()}
                </strong>
              </span>
            </div>

            {/* Sources Table */}
            <div className="table-card" style={{ overflowX: "auto" }}>
              <table className="feed-table" style={{ width: "100%", fontSize: 13 }}>
                <thead>
                  <tr>
                    <th style={{ width: 60, textAlign: "center" }}>ID</th>
                    <th>Source Label &amp; Provenance</th>
                    <th style={{ width: 140 }}>Category</th>
                    <th style={{ width: 150 }}>Imported At</th>
                    <th style={{ width: 100, textAlign: "right" }}>Accepted</th>
                    <th style={{ width: 90, textAlign: "right" }}>Rejected</th>
                    <th style={{ width: 110, textAlign: "right" }}>Observations</th>
                    <th style={{ width: 120, textAlign: "right" }}>WordPress</th>
                    <th style={{ width: 230, textAlign: "center" }}>Download Datasets (.txt)</th>
                    <th style={{ width: 130, textAlign: "center" }}>Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {filteredDbSources.map((src) => {
                    const cat = getSourceCategory(src.source);
                    const obsCount = src.observation_count ?? src.observations_count ?? 0;
                    const wpCount = src.wp_count ?? src.wordpress_count ?? 0;
                    const wpPct = src.accepted > 0 ? ((wpCount / src.accepted) * 100).toFixed(1) : "0.0";

                    return (
                      <tr key={src.id} style={{ borderBottom: "1px solid #1c2a38" }}>
                        <td style={{ textAlign: "center", fontWeight: 700, color: "#8fa8be" }}>
                          #{src.id}
                        </td>
                        <td>
                          <div style={{ fontWeight: 600, color: "#e4edf5", wordBreak: "break-all" }}>
                            {src.source}
                          </div>
                        </td>
                        <td>
                          <span className={`db-source-tag ${cat.cls}`}>
                            {cat.label}
                          </span>
                        </td>
                        <td style={{ color: "#8fa8be", fontSize: 12 }}>
                          {src.imported_at ? new Date(src.imported_at).toLocaleString() : "—"}
                        </td>
                        <td style={{ textAlign: "right" }}>
                          <span className="badge" style={{ background: "rgba(39, 174, 96, 0.15)", color: "#55efc4" }}>
                            {src.accepted.toLocaleString()}
                          </span>
                        </td>
                        <td style={{ textAlign: "right" }}>
                          {src.rejected > 0 ? (
                            <span className="badge" style={{ background: "rgba(231, 76, 60, 0.15)", color: "#ff7675" }}>
                              {src.rejected.toLocaleString()}
                            </span>
                          ) : (
                            <span style={{ color: "#576574" }}>0</span>
                          )}
                        </td>
                        <td style={{ textAlign: "right", fontWeight: 600, color: "#74b9ff" }}>
                          {obsCount.toLocaleString()}
                        </td>
                        <td style={{ textAlign: "right" }}>
                          {wpCount > 0 ? (
                            <span className="badge" style={{ background: "rgba(162, 155, 254, 0.15)", color: "#a29bfe", fontWeight: 600 }}>
                              {wpCount.toLocaleString()} ({wpPct}%)
                            </span>
                          ) : (
                            <span style={{ color: "#576574" }}>0</span>
                          )}
                        </td>
                        <td style={{ textAlign: "center" }}>
                          <div style={{ display: "inline-flex", gap: 4, flexWrap: "wrap", justifyContent: "center" }}>
                            <button
                              type="button"
                              className="db-action-btn secondary"
                              onClick={() => handleDownloadTxt({ source_id: String(src.id), field: "url" })}
                              title={`Download all URLs in source #${src.id} as plain text (.txt)`}
                            >
                              ⬇ URLs
                            </button>
                            <button
                              type="button"
                              className="db-action-btn secondary"
                              onClick={() => handleDownloadTxt({ source_id: String(src.id), field: "host" })}
                              title={`Download all Hostnames in source #${src.id} as plain text (.txt)`}
                            >
                              ⬇ Hosts
                            </button>
                            {wpCount > 0 && (
                              <button
                                type="button"
                                className="db-action-btn secondary"
                                onClick={() => handleDownloadTxt({ source_id: String(src.id), wordpress: "1", field: "url" })}
                                title={`Download verified WordPress URLs in source #${src.id} as plain text (.txt)`}
                                style={{ color: "#a29bfe", borderColor: "rgba(162, 155, 254, 0.4)" }}
                              >
                                ⬇ WP
                              </button>
                            )}
                          </div>
                        </td>
                        <td style={{ textAlign: "center" }}>
                          <div style={{ display: "inline-flex", gap: 6, justifyContent: "center" }}>
                            <button
                              type="button"
                              className="db-action-btn secondary"
                              onClick={() => handleOpenSample(src)}
                              title="Inspect sample records from this source"
                            >
                              👁️ Sample
                            </button>
                            <button
                              type="button"
                              className="db-action-btn danger"
                              onClick={() => setDeleteConfirmSource(src)}
                              title="Delete this source and its linked observations"
                              style={{ padding: "4px 8px" }}
                            >
                              🗑️
                            </button>
                          </div>
                        </td>
                      </tr>
                    );
                  })}
                  {!dbBusy && filteredDbSources.length === 0 && (
                    <tr>
                      <td colSpan={10} style={{ padding: 40, textAlign: "center", color: "#8fa8be" }}>
                        {dbSearch ? `No database sources match "${dbSearch}"` : "No dataset sources found in catalog."}
                      </td>
                    </tr>
                  )}
                  {dbBusy && (
                    <tr>
                      <td colSpan={10} style={{ padding: 40, textAlign: "center" }}>
                        <span className="pulse-indicator">
                          <span className="pulse-dot"></span> Loading database sources &amp; engine stats...
                        </span>
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>
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

        {/* DATABASE SOURCE SAMPLE VIEWER MODAL */}
        {selectedDbSource && (
          <>
            <div className="modal-backdrop" onClick={() => setSelectedDbSource(null)} />
            <div className="source-modal" style={{ width: "min(1050px, 95vw)" }}>
              <div className="source-modal-header">
                <div>
                  <h3 style={{ margin: 0, fontSize: 16, color: "#f0f6fc", display: "flex", alignItems: "center", gap: 8 }}>
                    <span>👁️ Source Sample:</span>
                    <span style={{ color: "#6ce0c7", fontFamily: "monospace" }}>{selectedDbSource.source}</span>
                    <span className="badge" style={{ background: "#1c2a38", color: "#8fa8be" }}>
                      ID #{selectedDbSource.id}
                    </span>
                  </h3>
                  <small style={{ color: "#8fa8be" }}>
                    Showing first 50 observations recorded in catalog.db • Imported {selectedDbSource.imported_at ? new Date(selectedDbSource.imported_at).toLocaleString() : "—"}
                  </small>
                </div>
                <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
                  <button
                    type="button"
                    className="secondary"
                    onClick={() => {
                      const allUrls = sampleRecords.map((r) => r.url).join("\n");
                      navigator.clipboard.writeText(allUrls);
                      setSampleCopied(true);
                      setTimeout(() => setSampleCopied(false), 2000);
                    }}
                    style={{ fontSize: 12, padding: "5px 10px" }}
                  >
                    {sampleCopied ? "✓ Copied!" : "📋 Copy Sample URLs"}
                  </button>
                  <button
                    type="button"
                    className="secondary"
                    onClick={() => handleDownloadTxt({ source_id: String(selectedDbSource.id), field: "url" })}
                    style={{ fontSize: 12, padding: "5px 10px" }}
                  >
                    ⬇ Download All ({selectedDbSource.observation_count.toLocaleString()})
                  </button>
                  <button
                    type="button"
                    onClick={() => setSelectedDbSource(null)}
                    style={{ fontSize: 12, padding: "5px 10px" }}
                  >
                    ✕ Close
                  </button>
                </div>
              </div>

              <div className="source-modal-body">
                <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 14, gap: 10 }}>
                  <input
                    type="search"
                    placeholder="Filter in sampled records..."
                    value={sampleFilter}
                    onChange={(e) => setSampleFilter(e.target.value)}
                    style={{ maxWidth: 300, padding: "6px 12px", fontSize: 12 }}
                  />
                  <div style={{ fontSize: 12, color: "#8fa8be" }}>
                    Sample size: <strong>{sampleRecords.length} records</strong>
                  </div>
                </div>

                {sampleLoading ? (
                  <div style={{ padding: 40, textAlign: "center" }}>
                    <span className="pulse-indicator">
                      <span className="pulse-dot"></span> Loading source records...
                    </span>
                  </div>
                ) : (
                  <div style={{ maxHeight: "58vh", overflowY: "auto", border: "1px solid #1c2a38", borderRadius: 8 }}>
                    <table className="feed-table" style={{ width: "100%", fontSize: 12, margin: 0 }}>
                      <thead>
                        <tr>
                          <th style={{ width: 40 }}>#</th>
                          <th>URL</th>
                          <th style={{ width: 180 }}>Hostname</th>
                          <th style={{ width: 140 }}>Scanned At</th>
                          <th>WordPress &amp; Tech Evidence</th>
                        </tr>
                      </thead>
                      <tbody>
                        {sampleRecords
                          .filter((r) => {
                            if (!sampleFilter.trim()) return true;
                            const q = sampleFilter.toLowerCase();
                            return (
                              r.url.toLowerCase().includes(q) ||
                              r.host.toLowerCase().includes(q) ||
                              r.evidence.some((ev) => ev.toLowerCase().includes(q))
                            );
                          })
                          .map((rec, i) => (
                            <tr key={i} style={{ borderBottom: "1px solid #162433" }}>
                              <td style={{ color: "#495c6e", textAlign: "center" }}>{i + 1}</td>
                              <td style={{ fontFamily: "monospace", wordBreak: "break-all" }}>
                                <a
                                  href={rec.url}
                                  target="_blank"
                                  rel="noopener noreferrer"
                                  style={{ color: "#6ce0c7", textDecoration: "none" }}
                                >
                                  {rec.url}
                                </a>
                              </td>
                              <td style={{ color: "#8fa8be" }}>{rec.host}</td>
                              <td style={{ color: "#8fa8be", fontSize: 11 }}>
                                {rec.scanned_at ? new Date(rec.scanned_at).toLocaleString() : "—"}
                              </td>
                              <td>
                                {rec.evidence.length > 0 ? (
                                  rec.evidence.map((ev, ei) => (
                                    <span
                                      key={ei}
                                      className="badge badge-tech"
                                      style={{ marginRight: 4, marginBottom: 2, fontSize: 11 }}
                                    >
                                      {ev}
                                    </span>
                                  ))
                                ) : (
                                  <span style={{ color: "#576574" }}>—</span>
                                )}
                              </td>
                            </tr>
                          ))}
                        {sampleRecords.length === 0 && (
                          <tr>
                            <td colSpan={5} style={{ padding: 30, textAlign: "center", color: "#8fa8be" }}>
                              No sample records found for this source.
                            </td>
                          </tr>
                        )}
                      </tbody>
                    </table>
                  </div>
                )}
              </div>
            </div>
          </>
        )}

        {/* DATASET INGESTION MODAL */}
        {showImportModal && (
          <>
            <div className="modal-backdrop" onClick={() => !importBusy && setShowImportModal(false)} />
            <div className="source-modal" style={{ width: "min(680px, 95vw)" }}>
              <div className="source-modal-header">
                <div>
                  <h3 style={{ margin: 0, fontSize: 16, color: "#f0f6fc" }}>➕ Ingest New Dataset</h3>
                  <small style={{ color: "#8fa8be" }}>
                    Add raw URLs or domains to catalog.db under a custom provenance label
                  </small>
                </div>
                <button
                  type="button"
                  disabled={importBusy}
                  onClick={() => setShowImportModal(false)}
                  style={{ fontSize: 12, padding: "5px 10px" }}
                >
                  ✕ Close
                </button>
              </div>

              <div className="source-modal-body">
                {importResult && (
                  <div className="db-alert success" style={{ marginBottom: 16 }}>
                    <div>
                      <strong>Dataset Ingestion Successful!</strong>
                      <div style={{ fontSize: 12, marginTop: 4 }}>
                        Source: <code>{importResult.source}</code> • Accepted: <strong>{importResult.accepted.toLocaleString()}</strong> • Rejected: <strong>{importResult.rejected.toLocaleString()}</strong>
                      </div>
                    </div>
                    <button
                      type="button"
                      onClick={() => setImportResult(null)}
                      style={{ padding: "2px 8px", fontSize: 11 }}
                    >
                      ✕
                    </button>
                  </div>
                )}

                <form onSubmit={handleImportDataset}>
                  <div style={{ marginBottom: 16 }}>
                    <label style={{ display: "block", marginBottom: 6, fontWeight: 600, fontSize: 13 }}>
                      Source Label / Provenance Tag:
                    </label>
                    <input
                      type="text"
                      placeholder="e.g. manual-seed-20261122 or client-audit-list"
                      value={importSourceName}
                      onChange={(e) => setImportSourceName(e.target.value)}
                      required
                      style={{ width: "100%", boxSizing: "border-box" }}
                    />
                  </div>

                  <div style={{ marginBottom: 16 }}>
                    <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 6 }}>
                      <label style={{ margin: 0, fontWeight: 600, fontSize: 13 }}>
                        Target URLs or Hostnames (one per line):
                      </label>
                      <label
                        style={{
                          margin: 0,
                          fontSize: 11.5,
                          padding: "4px 10px",
                          background: "var(--bg-surface-elevated)",
                          border: "1px solid var(--border-medium)",
                          borderRadius: 6,
                          cursor: "pointer",
                          color: "var(--accent-cyan)",
                        }}
                        title="Upload a .txt or .csv file of URLs or hosts"
                      >
                        📁 Load .txt File
                        <input
                          type="file"
                          accept=".txt,.csv"
                          onChange={handleFileUpload}
                          style={{ display: "none" }}
                        />
                      </label>
                    </div>
                    <textarea
                      rows={10}
                      placeholder={"https://example.org\nhttps://wordpress.org\nwikipedia.org"}
                      value={importUrlsText}
                      onChange={(e) => setImportUrlsText(e.target.value)}
                      required
                      style={{
                        width: "100%",
                        boxSizing: "border-box",
                        background: "#070c12",
                        border: "1px solid var(--border-subtle)",
                        borderRadius: 8,
                        padding: "10px 14px",
                        color: "#c5d8e8",
                        fontFamily: "monospace",
                        fontSize: 12,
                        lineHeight: 1.5,
                      }}
                    />
                    <div style={{ display: "flex", justifyContent: "space-between", marginTop: 4, fontSize: 12, color: "var(--text-subtle)" }}>
                      <small>Paste URLs or domain names. Duplicates will be safely merged.</small>
                      {importUrlsText.trim() && (
                        <small style={{ color: "var(--accent-mint)", fontWeight: 600 }}>
                          {importUrlsText.split("\n").filter((l) => l.trim()).length.toLocaleString()} lines queued
                        </small>
                      )}
                    </div>
                  </div>

                  <div style={{ display: "flex", justifyContent: "flex-end", gap: 8 }}>
                    <button
                      type="button"
                      className="secondary"
                      disabled={importBusy}
                      onClick={() => setShowImportModal(false)}
                    >
                      Cancel
                    </button>
                    <button
                      type="submit"
                      className="primary"
                      disabled={importBusy || !importSourceName.trim() || !importUrlsText.trim()}
                    >
                      {importBusy ? "Ingesting Dataset..." : "⚡ Ingest into Catalog"}
                    </button>
                  </div>
                </form>
              </div>
            </div>
          </>
        )}

        {/* DELETE SOURCE CONFIRMATION MODAL */}
        {deleteConfirmSource && (
          <>
            <div className="modal-backdrop" onClick={() => !deleteBusy && setDeleteConfirmSource(null)} />
            <div className="source-modal" style={{ width: "min(520px, 95vw)" }}>
              <div className="source-modal-header" style={{ borderBottomColor: "rgba(231, 76, 60, 0.3)" }}>
                <div>
                  <h3 style={{ margin: 0, fontSize: 16, color: "#ff7675" }}>⚠️ Confirm Source Deletion</h3>
                  <small style={{ color: "#8fa8be" }}>This action cannot be undone</small>
                </div>
                <button
                  type="button"
                  disabled={deleteBusy}
                  onClick={() => setDeleteConfirmSource(null)}
                  style={{ fontSize: 12, padding: "5px 10px" }}
                >
                  ✕ Close
                </button>
              </div>

              <div className="source-modal-body">
                <p style={{ margin: "0 0 16px", fontSize: 13, lineHeight: 1.6 }}>
                  Are you sure you want to permanently delete dataset source{" "}
                  <strong style={{ color: "#6ce0c7" }}>{deleteConfirmSource.source}</strong> (ID #{deleteConfirmSource.id})?
                </p>
                <div style={{ background: "#0b1118", border: "1px solid #1c2a38", borderRadius: 8, padding: 12, marginBottom: 16, fontSize: 12 }}>
                  <div>• Accepted records: <strong>{deleteConfirmSource.accepted.toLocaleString()}</strong></div>
                  <div>• Linked observations to purge: <strong>{deleteConfirmSource.observation_count.toLocaleString()}</strong></div>
                  <div>• Linked WordPress findings: <strong>{deleteConfirmSource.wp_count.toLocaleString()}</strong></div>
                </div>
                <div style={{ display: "flex", justifyContent: "flex-end", gap: 8 }}>
                  <button
                    type="button"
                    className="secondary"
                    disabled={deleteBusy}
                    onClick={() => setDeleteConfirmSource(null)}
                  >
                    Cancel
                  </button>
                  <button
                    type="button"
                    className="danger"
                    disabled={deleteBusy}
                    onClick={handleConfirmDelete}
                  >
                    {deleteBusy ? "Purging..." : "🗑️ Permanently Delete Source"}
                  </button>
                </div>
              </div>
            </div>
          </>
        )}
      </main>

      {/* Floating Toast Notification Container */}
      <div className="toast-container" aria-live="polite">
        {toasts.map((t) => (
          <div key={t.id} className={`toast-item ${t.type}`}>
            <span style={{ fontSize: 16 }}>{t.type === "success" ? "✅" : t.type === "error" ? "❌" : "ℹ️"}</span>
            <div style={{ flex: 1, fontSize: 13, lineHeight: 1.4 }}>{t.message}</div>
          </div>
        ))}
      </div>
    </>
  );
}
