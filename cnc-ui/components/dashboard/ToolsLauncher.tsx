"use client";

import { useState, useEffect, useCallback } from "react";
import { ToolSelectDropdown, type ToolDefinition } from "./ToolSelectDropdown";

// ── Helper: derive input label/placeholder from tool definition ───────────────
function getInputLabel(tool: ToolDefinition, isServerTool: boolean, isSpreadTool: boolean): string {
  if (tool.input_label) return tool.input_label;
  if (isServerTool) return "Target File (Server Path)";
  if (isSpreadTool) return "Target File (Server Source Path)";
  return "Target File (Worker Path)";
}

function getInputPlaceholder(tool: ToolDefinition, isServerTool: boolean, isSpreadTool: boolean): string {
  if (tool.input_placeholder) return tool.input_placeholder;
  if (isServerTool) return "/root/file/ips.txt";
  if (isSpreadTool) return "/root/file/queries.txt";
  return "/root/file/x.txt";
}

function getInputHelperText(tool: ToolDefinition, isServerTool: boolean, isSpreadTool: boolean): string {
  if (isServerTool) return "Path on the server host. Output is placed in the GDrive sync directory.";
  if (isSpreadTool) return "File path on the server. CNC splits it and distributes slices to each online worker.";
  return "File path on connected worker nodes to process.";
}

export function ToolsLauncher() {
  const [tools, setTools] = useState<ToolDefinition[]>([]);
  const [selectedToolId, setSelectedToolId] = useState<string>("");
  const [inputFile, setInputFile] = useState<string>("/root/file/x.txt");
  const [outputFile, setOutputFile] = useState<string>("x.txt");
  const [toolOptions, setToolOptions] = useState<Record<string, any>>({});
  // shared file inputs keyed by SharedFileSpec.name (e.g. "passlist", "userlist")
  const [sharedFiles, setSharedFiles] = useState<Record<string, string>>({});

  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [launching, setLaunching] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);

  const fetchTools = useCallback(async (isRefresh = false) => {
    if (isRefresh) setRefreshing(true);
    try {
      const res = await fetch("/api/tools", { cache: "no-store" });
      if (!res.ok) throw new Error("Failed to fetch tools");
      const data: ToolDefinition[] = await res.json();
      setTools(data);
      if (data.length > 0) {
        setSelectedToolId((prev) => {
          const exists = data.some((t) => t.id === prev);
          const currentId = exists ? prev : data[0].id;
          const currentTool = data.find((t) => t.id === currentId) || data[0];
          initToolState(currentTool);
          return currentId;
        });
      }
    } catch (err: any) {
      setError(err.message || "Could not load tools");
    } finally {
      setLoading(false);
      if (isRefresh) setRefreshing(false);
    }
  }, []);

  useEffect(() => {
    fetchTools();
  }, [fetchTools]);

  const initToolState = (tool: ToolDefinition) => {
    // Populate options from tool definition defaults
    const initialOpts: Record<string, any> = {};
    if (tool.options) {
      tool.options.forEach((opt) => {
        initialOpts[opt.name] = opt.default ?? "";
      });
    }
    setToolOptions(initialOpts);

    // Initialise shared file inputs (empty strings — user must provide paths)
    const initialShared: Record<string, string> = {};
    if (tool.shared_files) {
      tool.shared_files.forEach((sf) => {
        initialShared[sf.name] = "";
      });
    }
    setSharedFiles(initialShared);

    // Set input file placeholder from registry metadata or sensible fallback
    const isServer = tool.scope === "server";
    const isSpread = tool.default_mode === "spread";
    setInputFile(getInputPlaceholder(tool, isServer, isSpread));
  };

  const handleSelectTool = (toolId: string) => {
    setSelectedToolId(toolId);
    const tool = tools.find((t) => t.id === toolId);
    if (tool) initToolState(tool);
  };

  const selectedTool = tools.find((t) => t.id === selectedToolId) || tools[0];
  const isServerTool = selectedTool?.scope === "server";
  const isSpreadTool = selectedTool?.default_mode === "spread";
  const hasSharedFiles = (selectedTool?.shared_files?.length ?? 0) > 0;

  const handleOptionChange = (name: string, value: any) => {
    setToolOptions((prev) => ({ ...prev, [name]: value }));
  };

  const handleSharedFileChange = (name: string, value: string) => {
    setSharedFiles((prev) => ({ ...prev, [name]: value }));
  };

  // ── Command preview (best-effort; shared file download steps omitted for brevity) ──
  const getCommandPreview = () => {
    if (!selectedTool) return "";
    let cmd = selectedTool.executable;
    const inFlag = selectedTool.input_flag || (isServerTool ? "-f" : "-l");
    const targetInput = isSpreadTool ? "{input}" : inputFile;
    if (targetInput) cmd += ` ${inFlag} ${targetInput}`;

    if (outputFile) {
      const outFlag = selectedTool.output_flag || "-o";
      cmd += ` ${outFlag} ${outputFile}`;
    }

    if (selectedTool.options) {
      selectedTool.options.forEach((opt) => {
        const val = toolOptions[opt.name];
        if (val !== undefined && val !== null && val !== "") {
          if (opt.flag) {
            cmd += ` ${opt.flag} ${val}`;
          } else {
            cmd += ` ${val}`;
          }
        }
      });
    }

    // Append shared file flags (paths will be /tmp/cnc_<name>.txt on workers)
    if (selectedTool.shared_files) {
      selectedTool.shared_files.forEach((sf) => {
        if (sharedFiles[sf.name]) {
          cmd += ` ${sf.flag} /tmp/cnc_${sf.name}.txt`;
        }
      });
    }

    return cmd;
  };

  // ── Check required shared files are filled ────────────────────────────────
  const missingRequired = (selectedTool?.shared_files ?? [])
    .filter((sf) => sf.required && !sharedFiles[sf.name]?.trim())
    .map((sf) => sf.label);

  const handleLaunch = async (e: React.FormEvent) => {
    e.preventDefault();
    if (missingRequired.length > 0) {
      setError(`Missing required fields: ${missingRequired.join(", ")}`);
      return;
    }
    setError(null);
    setSuccess(null);
    setLaunching(true);

    try {
      // Build payload — merge shared file paths at top level so server can distribute them
      const payload: Record<string, any> = {
        tool_id: selectedTool.id,
        input_file: inputFile,
        output_file: outputFile,
        options: toolOptions,
      };
      // Attach each shared file path at the top level (e.g. payload.passlist = "/root/…")
      Object.entries(sharedFiles).forEach(([k, v]) => {
        if (v.trim()) payload[k] = v.trim();
      });

      const res = await fetch("/api/tools/launch", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });

      if (!res.ok) {
        const errData = await res.json();
        throw new Error(errData.error || "Failed to launch tool");
      }

      const data = await res.json();
      const modeLabel =
        data.mode === "server"
          ? "Server-Side"
          : data.mode === "spread"
          ? "Load-Balanced (Spread)"
          : "Worker";
      setSuccess(`Job launched successfully as ${modeLabel} Job (ID: ${data.job_id})`);
    } catch (err: any) {
      setError(err.message || "An error occurred");
    } finally {
      setLaunching(false);
    }
  };

  const formatOptionLabel = (name: string): string =>
    name
      .split("_")
      .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
      .join(" ");

  if (loading) {
    return (
      <div className="text-gray-400 p-12 flex flex-col items-center justify-center gap-3">
        <div className="w-8 h-8 rounded-full border-2 border-blue-500 border-t-transparent animate-spin" />
        <span className="text-sm font-medium animate-pulse">Loading dynamic tool configurations...</span>
      </div>
    );
  }

  // Filter out extra_args for separate rendering
  const standardOptions = selectedTool?.options?.filter((opt) => opt.name !== "extra_args") || [];
  const extraArgsOption = selectedTool?.options?.find((opt) => opt.name === "extra_args");

  return (
    <div className="max-w-4xl mx-auto space-y-6">
      <div className="bg-gray-900 border border-gray-800 rounded-2xl shadow-2xl overflow-hidden backdrop-blur-md">

        {/* Header */}
        <div className="px-6 py-5 border-b border-gray-800 bg-gray-900/60 flex justify-between items-center">
          <div className="flex items-center gap-3">
            <div className={`w-10 h-10 rounded-xl flex items-center justify-center shrink-0 border ${
              isServerTool
                ? "bg-purple-500/15 border-purple-500/30 text-purple-400 shadow-[0_0_15px_rgba(168,85,247,0.2)]"
                : "bg-blue-500/15 border-blue-500/30 text-blue-400 shadow-[0_0_15px_rgba(59,130,246,0.2)]"
            }`}>
              <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M13 10V3L4 14h7v7l9-11h-7z" />
              </svg>
            </div>
            <div>
              <div className="flex items-center gap-2.5">
                <h2 className="text-lg font-bold text-white tracking-tight">Tools Launcher</h2>
                <span className="text-xs px-2 py-0.5 rounded-full font-mono font-medium border bg-gray-800/80 text-gray-300 border-gray-700/60">
                  {tools.length} dynamic tool{tools.length === 1 ? "" : "s"}
                </span>
              </div>
              <p className="text-xs text-gray-400 mt-0.5">
                Form options dynamically load and configure in real-time from the server registry.
              </p>
            </div>
          </div>

          <button
            type="button"
            onClick={() => fetchTools(true)}
            disabled={refreshing}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium bg-gray-800/70 hover:bg-gray-800 text-gray-300 hover:text-white border border-gray-700/60 transition-all cursor-pointer disabled:opacity-50"
            title="Refresh tool definitions from server"
          >
            <svg
              className={`w-3.5 h-3.5 ${refreshing ? "animate-spin text-blue-400" : "text-gray-400"}`}
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              strokeWidth={2}
            >
              <path strokeLinecap="round" strokeLinejoin="round" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
            </svg>
            <span>{refreshing ? "Syncing..." : "Sync"}</span>
          </button>
        </div>

        {/* Body */}
        <div className="p-6 space-y-6">
          {error && (
            <div className="p-4 rounded-xl bg-red-500/10 border border-red-500/20 text-red-400 text-sm flex items-start gap-3">
              <svg className="w-5 h-5 shrink-0 mt-0.5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
              </svg>
              <span>{error}</span>
            </div>
          )}
          {success && (
            <div className="p-4 rounded-xl bg-emerald-500/10 border border-emerald-500/20 text-emerald-400 text-sm flex items-start gap-3">
              <svg className="w-5 h-5 shrink-0 mt-0.5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z" />
              </svg>
              <span>{success}</span>
            </div>
          )}

          {/* Mode info notices */}
          {isServerTool && (
            <div className="p-4 rounded-xl bg-purple-500/10 border border-purple-500/20 text-purple-300 text-sm flex items-start gap-3 animate-fadeIn">
              <div className="p-1 rounded-md bg-purple-500/20 text-purple-400 shrink-0 mt-0.5">
                <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M5 12h14M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2m-2-4h.01M17 16h.01" />
                </svg>
              </div>
              <div>
                <span className="font-semibold text-purple-200">Server-Side Tool:</span> This tool executes directly on the CNC server host and is tracked as a server job in your dashboard. Remote worker nodes are bypassed.
              </div>
            </div>
          )}

          {isSpreadTool && (
            <div className="p-4 rounded-xl bg-emerald-500/10 border border-emerald-500/20 text-emerald-300 text-sm flex items-start gap-3 animate-fadeIn">
              <div className="p-1 rounded-md bg-emerald-500/20 text-emerald-400 shrink-0 mt-0.5">
                <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M4 6h16M4 10h16M4 14h16M4 18h16" />
                </svg>
              </div>
              <div>
                <span className="font-semibold text-emerald-200">Load-Balanced Tool:</span>{" "}
                {hasSharedFiles
                  ? "Your target list is split and distributed across all connected workers. Additional shared files (passlist, userlist) are uploaded to the server and automatically downloaded by each worker before execution."
                  : "This tool automatically distributes your target list across all connected worker servers in parallel slices. Results are streamed back, deduplicated, and synced to Google Drive."}
              </div>
            </div>
          )}

          <form onSubmit={handleLaunch} className="space-y-6">

            {/* Tool Selection */}
            <div className="space-y-2">
              <label className="block text-sm font-medium text-gray-300">Target Tool</label>
              <ToolSelectDropdown
                tools={tools}
                selectedToolId={selectedToolId}
                onSelect={handleSelectTool}
                loading={loading}
              />
            </div>

            {/* Core Target & Output Paths */}
            <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
              {/* Input File — label/placeholder driven by registry metadata */}
              <div className="space-y-2">
                <div className="flex items-center justify-between">
                  <label className="block text-sm font-medium text-gray-300">
                    {selectedTool ? getInputLabel(selectedTool, isServerTool, isSpreadTool) : "Target File"}
                  </label>
                  <span className="text-[11px] font-mono text-gray-500">
                    Flag: {selectedTool?.input_flag || (isServerTool ? "-f" : "-l")}
                  </span>
                </div>
                <div className="relative group">
                  <div className="absolute inset-y-0 left-0 pl-3.5 flex items-center pointer-events-none">
                    <svg className="w-4 h-4 text-gray-500 group-focus-within:text-blue-500 transition-colors" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M3 7v10a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-6l-2-2H5a2 2 0 00-2 2z" />
                    </svg>
                  </div>
                  <input
                    id="tool-input-file"
                    type="text"
                    value={inputFile}
                    onChange={(e) => setInputFile(e.target.value)}
                    placeholder={selectedTool ? getInputPlaceholder(selectedTool, isServerTool, isSpreadTool) : "/root/file/x.txt"}
                    className="w-full bg-gray-950/80 border border-gray-800 rounded-xl pl-10 pr-4 py-2.5 text-white font-mono text-sm focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20 transition-all placeholder:text-gray-600"
                    required
                  />
                </div>
                <p className="text-xs text-gray-500">
                  {selectedTool ? getInputHelperText(selectedTool, isServerTool, isSpreadTool) : ""}
                </p>
              </div>

              {/* Output File */}
              <div className="space-y-2">
                <div className="flex items-center justify-between">
                  <label className="block text-sm font-medium text-gray-300">Watcher Output Filename</label>
                  <span className="text-[11px] font-mono text-gray-500">
                    Flag: {selectedTool?.output_flag || "-o"}
                  </span>
                </div>
                <div className="relative group">
                  <div className="absolute inset-y-0 left-0 pl-3.5 flex items-center pointer-events-none">
                    <svg className="w-4 h-4 text-gray-500 group-focus-within:text-purple-500 transition-colors" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-8l-4-4m0 0L8 8m4-4v12" />
                    </svg>
                  </div>
                  <input
                    id="tool-output-file"
                    type="text"
                    value={outputFile}
                    onChange={(e) => setOutputFile(e.target.value)}
                    placeholder="x.txt"
                    className="w-full bg-gray-950/80 border border-gray-800 rounded-xl pl-10 pr-4 py-2.5 text-white font-mono text-sm focus:outline-none focus:border-purple-500 focus:ring-2 focus:ring-purple-500/20 transition-all placeholder:text-gray-600"
                  />
                </div>
                <p className="text-xs text-gray-500">Filename created and merged inside Google Drive sync directory.</p>
              </div>
            </div>

            {/* Shared Files (e.g. passlist / userlist for wp-bruter) — data-driven from SharedFiles registry */}
            {hasSharedFiles && (
              <div className="pt-2">
                <h3 className="text-xs font-semibold text-gray-400 uppercase tracking-wider mb-4 flex items-center gap-2">
                  <span>Shared Worker Files</span>
                  <div className="h-px flex-1 bg-gray-800" />
                  <span className="text-[10px] text-amber-500/80 font-mono lowercase normal-case tracking-normal">
                    uploaded to server · fetched by each worker
                  </span>
                </h3>
                <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                  {selectedTool?.shared_files?.map((sf) => (
                    <div key={sf.name} className="space-y-2">
                      <div className="flex items-center justify-between">
                        <label className="block text-sm font-medium text-gray-300 flex items-center gap-1.5">
                          {sf.label}
                          {sf.required && (
                            <span className="text-[10px] px-1.5 py-0.5 rounded bg-red-500/10 text-red-400 border border-red-500/20 font-mono">
                              required
                            </span>
                          )}
                        </label>
                        <span className="font-mono text-[10px] px-1.5 py-0.5 rounded bg-gray-800 text-gray-400 border border-gray-700/60">
                          {sf.flag}
                        </span>
                      </div>
                      <div className="relative group">
                        <div className="absolute inset-y-0 left-0 pl-3.5 flex items-center pointer-events-none">
                          <svg className="w-4 h-4 text-gray-500 group-focus-within:text-amber-500 transition-colors" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z" />
                          </svg>
                        </div>
                        <input
                          id={`tool-shared-${sf.name}`}
                          type="text"
                          value={sharedFiles[sf.name] ?? ""}
                          onChange={(e) => handleSharedFileChange(sf.name, e.target.value)}
                          placeholder={`/root/file/${sf.name}.txt`}
                          required={sf.required}
                          className={`w-full bg-gray-950/80 border rounded-xl pl-10 pr-4 py-2.5 text-white font-mono text-sm focus:outline-none focus:ring-2 transition-all placeholder:text-gray-600 ${
                            sf.required && !sharedFiles[sf.name]?.trim()
                              ? "border-red-800/50 focus:border-amber-500 focus:ring-amber-500/20"
                              : "border-gray-800 focus:border-amber-500 focus:ring-amber-500/20"
                          }`}
                        />
                      </div>
                      {sf.description && (
                        <p className="text-xs text-gray-500">{sf.description}</p>
                      )}
                    </div>
                  ))}
                </div>
              </div>
            )}

            {/* Dynamically Generated Tool Options */}
            {standardOptions.length > 0 && (
              <div className="pt-2">
                <h3 className="text-xs font-semibold text-gray-400 uppercase tracking-wider mb-4 flex items-center gap-2">
                  <span>Tool-Specific Options</span>
                  <div className="h-px flex-1 bg-gray-800" />
                </h3>

                <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                  {standardOptions.map((opt) => {
                    const val = toolOptions[opt.name] ?? "";

                    return (
                      <div key={opt.name} className="space-y-2">
                        <div className="flex items-center justify-between">
                          <label className="block text-sm font-medium text-gray-300">
                            {formatOptionLabel(opt.name)}
                          </label>
                          {opt.flag && (
                            <span className="font-mono text-[10px] px-1.5 py-0.5 rounded bg-gray-800 text-gray-400 border border-gray-700/60">
                              {opt.flag}
                            </span>
                          )}
                        </div>

                        <div className="relative group">
                          {opt.type === "boolean" ? (
                            <label className="flex items-center gap-3 cursor-pointer select-none group py-2">
                              <div className="relative">
                                <input
                                  id={`tool-opt-${opt.name}`}
                                  type="checkbox"
                                  checked={!!val}
                                  onChange={(e) => handleOptionChange(opt.name, e.target.checked)}
                                  className="sr-only peer"
                                />
                                <div className="w-10 h-5 bg-gray-800 border border-gray-700 rounded-full peer-checked:bg-blue-600 peer-checked:border-blue-600 transition-colors duration-200" />
                                <div className="absolute top-0.5 left-0.5 w-4 h-4 bg-gray-400 peer-checked:bg-white rounded-full shadow transition-all duration-200 peer-checked:translate-x-5" />
                              </div>
                              <span className="text-sm text-gray-400 group-hover:text-gray-300 transition-colors">
                                {val ? "Enabled" : "Disabled"}
                              </span>
                            </label>
                          ) : opt.type === "number" ? (
                            <input
                              id={`tool-opt-${opt.name}`}
                              type="number"
                              value={val}
                              onChange={(e) => handleOptionChange(opt.name, parseInt(e.target.value) || 0)}
                              className="w-full bg-gray-950/80 border border-gray-800 rounded-xl px-4 py-2.5 text-white text-sm focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20 transition-all"
                            />
                          ) : (
                            <input
                              id={`tool-opt-${opt.name}`}
                              type="text"
                              value={val}
                              onChange={(e) => handleOptionChange(opt.name, e.target.value)}
                              placeholder={`Default: ${opt.default ?? "none"}`}
                              className="w-full bg-gray-950/80 border border-gray-800 rounded-xl px-4 py-2.5 text-white text-sm focus:outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-500/20 transition-all placeholder:text-gray-600"
                            />
                          )}
                        </div>

                        {opt.description && (
                          <p className="text-xs text-gray-500">{opt.description}</p>
                        )}
                      </div>
                    );
                  })}
                </div>
              </div>
            )}

            {/* Extra Arguments (if supported) */}
            {extraArgsOption && (
              <div className="space-y-2">
                <div className="flex items-center justify-between">
                  <label className="block text-sm font-medium text-gray-300">Extra Arguments</label>
                  <span className="text-[11px] font-mono text-gray-500">Passthrough</span>
                </div>
                <div className="relative group">
                  <div className="absolute inset-y-0 left-0 pl-3.5 flex items-center pointer-events-none">
                    <span className="text-gray-500 font-mono font-bold group-focus-within:text-amber-500 transition-colors">$_</span>
                  </div>
                  <input
                    id="tool-extra-args"
                    type="text"
                    value={toolOptions["extra_args"] ?? ""}
                    onChange={(e) => handleOptionChange("extra_args", e.target.value)}
                    placeholder="e.g. -v --debug"
                    className="w-full bg-gray-950/80 border border-gray-800 rounded-xl pl-10 pr-4 py-2.5 text-white font-mono text-sm focus:outline-none focus:border-amber-500 focus:ring-2 focus:ring-amber-500/20 transition-all placeholder:text-gray-600"
                  />
                </div>
                <p className="text-xs text-gray-500">{extraArgsOption.description}</p>
              </div>
            )}

            {/* Live Command Preview */}
            <div className="mt-8 pt-6 border-t border-gray-800">
              <div className="flex items-center justify-between mb-2">
                <label className="block text-xs font-semibold text-gray-400 uppercase tracking-wider">
                  Live Generated Command
                </label>
                <span className="text-[11px] font-mono text-gray-500">
                  {isServerTool ? "Mode: server" : isSpreadTool ? "Mode: spread (load balanced)" : "Mode: broadcast"}
                </span>
              </div>
              <div className="bg-black/90 border border-gray-800 rounded-xl p-4 font-mono text-sm text-emerald-400 overflow-x-auto shadow-inner flex items-center gap-2">
                <span className="text-gray-500 select-none">$</span>
                <span className="whitespace-pre">{getCommandPreview()}</span>
              </div>
              {hasSharedFiles && (
                <p className="text-xs text-gray-600 mt-1.5">
                  ↳ Workers will also pre-download shared files via <code className="text-gray-500">curl</code> before running the command.
                </p>
              )}
            </div>

            {/* Actions */}
            <div className="pt-2 flex items-center justify-between">
              <span className="text-xs text-gray-500">
                {isServerTool
                  ? "Direct server process invocation with SSE progress streaming"
                  : isSpreadTool
                  ? "Splits target input and load-balances slices across online workers"
                  : "Broadcasts execution across all online cluster workers"}
              </span>
              <button
                type="submit"
                disabled={launching || !selectedTool || missingRequired.length > 0}
                className={`relative px-6 py-2.5 rounded-xl font-medium text-white shadow-lg overflow-hidden transition-all duration-300 cursor-pointer
                  ${launching || missingRequired.length > 0
                    ? "bg-blue-600/50 cursor-not-allowed opacity-60"
                    : isServerTool
                    ? "bg-gradient-to-r from-purple-600 to-indigo-600 hover:from-purple-500 hover:to-indigo-500 shadow-purple-500/20 hover:shadow-purple-500/30"
                    : isSpreadTool
                    ? "bg-gradient-to-r from-emerald-600 to-teal-600 hover:from-emerald-500 hover:to-teal-500 shadow-emerald-500/20 hover:shadow-emerald-500/30"
                    : "bg-gradient-to-r from-blue-600 to-cyan-600 hover:from-blue-500 hover:to-cyan-500 shadow-blue-500/20 hover:shadow-blue-500/30"
                  }`}
              >
                {launching && (
                  <div className="absolute inset-0 flex items-center justify-center bg-inherit rounded-xl">
                    <div className="w-4 h-4 border-2 border-white/30 border-t-white rounded-full animate-spin" />
                  </div>
                )}
                <span className={`flex items-center gap-2 ${launching ? "opacity-0" : "opacity-100"}`}>
                  <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M14.752 11.168l-3.197-2.132A1 1 0 0010 9.87v4.263a1 1 0 001.555.832l3.197-2.132a1 1 0 000-1.664z" />
                    <path strokeLinecap="round" strokeLinejoin="round" d="M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                  </svg>
                  {isServerTool
                    ? "Launch Server Job"
                    : isSpreadTool
                    ? "Launch Distributed Job (Load Balanced)"
                    : "Launch Job (Broadcast)"}
                </span>
              </button>
            </div>

          </form>
        </div>
      </div>
    </div>
  );
}
