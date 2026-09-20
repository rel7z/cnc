"use client";

import { useState, useRef, useEffect, useCallback } from "react";

export interface ToolOption {
  name: string;
  flag: string;
  description: string;
  type: string;
  default: any;
}

export interface SharedFileSpec {
  name: string;
  label: string;
  flag: string;
  description: string;
  required: boolean;
}

export interface ToolDefinition {
  id: string;
  name: string;
  description: string;
  executable: string;
  scope?: string;
  default_mode?: string;
  input_flag?: string;
  output_flag?: string;
  input_label?: string;
  input_placeholder?: string;
  shared_files?: SharedFileSpec[];
  options: ToolOption[];
}

interface ToolSelectDropdownProps {
  tools: ToolDefinition[];
  selectedToolId: string;
  onSelect: (toolId: string) => void;
  loading?: boolean;
  disabled?: boolean;
}

export function ToolSelectDropdown({
  tools,
  selectedToolId,
  onSelect,
  loading = false,
  disabled = false,
}: ToolSelectDropdownProps) {
  const [isOpen, setIsOpen] = useState(false);
  const [highlightedIndex, setHighlightedIndex] = useState(-1);
  const containerRef = useRef<HTMLDivElement>(null);
  const listboxRef = useRef<HTMLDivElement>(null);

  const selectedTool = tools.find((t) => t.id === selectedToolId) ?? tools[0];

  // Close when clicking outside
  useEffect(() => {
    function handleClickOutside(event: MouseEvent | TouchEvent) {
      if (
        containerRef.current &&
        !containerRef.current.contains(event.target as Node)
      ) {
        setIsOpen(false);
      }
    }
    if (isOpen) {
      document.addEventListener("mousedown", handleClickOutside);
      document.addEventListener("touchstart", handleClickOutside);
    }
    return () => {
      document.removeEventListener("mousedown", handleClickOutside);
      document.removeEventListener("touchstart", handleClickOutside);
    };
  }, [isOpen]);

  // Sync highlighted index with selected tool when opened
  useEffect(() => {
    if (isOpen) {
      const idx = tools.findIndex((t) => t.id === selectedToolId);
      setHighlightedIndex(idx >= 0 ? idx : 0);
    }
  }, [isOpen, selectedToolId, tools]);

  // Handle keyboard navigation
  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (disabled || loading || tools.length === 0) return;

      if (!isOpen) {
        if (e.key === "Enter" || e.key === " " || e.key === "ArrowDown") {
          e.preventDefault();
          setIsOpen(true);
        }
        return;
      }

      switch (e.key) {
        case "ArrowDown":
          e.preventDefault();
          setHighlightedIndex((prev) => (prev + 1) % tools.length);
          break;
        case "ArrowUp":
          e.preventDefault();
          setHighlightedIndex((prev) =>
            prev <= 0 ? tools.length - 1 : prev - 1
          );
          break;
        case "Enter":
        case " ":
          e.preventDefault();
          if (highlightedIndex >= 0 && highlightedIndex < tools.length) {
            onSelect(tools[highlightedIndex].id);
            setIsOpen(false);
          }
          break;
        case "Escape":
        case "Tab":
          setIsOpen(false);
          break;
      }
    },
    [disabled, loading, tools, isOpen, highlightedIndex, onSelect]
  );

  if (loading) {
    return (
      <div className="w-full bg-gray-900/60 border border-gray-800 rounded-xl px-4 py-3 flex items-center gap-3 animate-pulse">
        <div className="w-9 h-9 rounded-lg bg-gray-800 shrink-0" />
        <div className="flex-1 space-y-2">
          <div className="h-4 bg-gray-800 rounded w-1/3" />
          <div className="h-3 bg-gray-800/60 rounded w-2/3" />
        </div>
        <div className="w-5 h-5 rounded-full bg-gray-800 shrink-0" />
      </div>
    );
  }

  return (
    <div
      ref={containerRef}
      className="relative w-full"
      onKeyDown={handleKeyDown}
    >
      {/* Trigger Button */}
      <button
        type="button"
        disabled={disabled || tools.length === 0}
        onClick={() => setIsOpen((prev) => !prev)}
        aria-haspopup="listbox"
        aria-expanded={isOpen}
        className={`w-full text-left rounded-xl px-4 py-3 flex items-center justify-between gap-3 transition-all duration-200 ease-out outline-none select-none group
          ${
            isOpen
              ? "bg-gray-900 border-blue-500/80 ring-2 ring-blue-500/20 shadow-[0_0_20px_rgba(59,130,246,0.15)]"
              : "bg-gray-950/80 hover:bg-gray-900/80 border-gray-800 hover:border-gray-700/80 shadow-sm"
          }
          border backdrop-blur-sm
          ${disabled ? "opacity-50 cursor-not-allowed" : "cursor-pointer"}
        `}
      >
        <div className="flex items-center gap-3.5 min-w-0">
          {/* Tool Icon container */}
          <div
            className={`w-9 h-9 rounded-lg flex items-center justify-center shrink-0 border transition-all duration-300 ${
              isOpen
                ? "bg-blue-500/20 border-blue-500/40 text-blue-400 shadow-sm shadow-blue-500/20"
                : "bg-gray-900 border-gray-800 text-blue-400 group-hover:border-blue-500/30 group-hover:bg-blue-500/10"
            }`}
          >
            <svg
              className="w-5 h-5"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              strokeWidth={2}
            >
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                d="M13 10V3L4 14h7v7l9-11h-7z"
              />
            </svg>
          </div>

          {/* Tool Details */}
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <span className="font-semibold text-white tracking-tight text-sm truncate">
                {selectedTool?.name || "Select a tool..."}
              </span>
              {selectedTool?.scope === "server" ? (
                <span className="font-mono text-[11px] px-2 py-0.5 rounded-md bg-purple-500/10 text-purple-400 border border-purple-500/20 shrink-0 font-medium tracking-wide">
                  Server-Side
                </span>
              ) : selectedTool?.default_mode === "spread" ? (
                <span className="font-mono text-[11px] px-2 py-0.5 rounded-md bg-emerald-500/10 text-emerald-400 border border-emerald-500/20 shrink-0 font-medium tracking-wide">
                  Load Balanced
                </span>
              ) : (
                <span className="font-mono text-[11px] px-2 py-0.5 rounded-md bg-blue-500/10 text-blue-400 border border-blue-500/20 shrink-0 font-medium tracking-wide">
                  Worker
                </span>
              )}
              {selectedTool?.executable && (
                <span className="font-mono text-[11px] px-2 py-0.5 rounded-md bg-gray-800 text-gray-400 border border-gray-700/60 shrink-0 font-medium tracking-wide">
                  {selectedTool.executable}
                </span>
              )}
            </div>
            <p className="text-xs text-gray-400 truncate mt-0.5">
              {selectedTool?.description || "No tool selected"}
            </p>
          </div>
        </div>

        {/* Animated Chevron Indicator */}
        <div
          className={`w-7 h-7 rounded-lg flex items-center justify-center shrink-0 text-gray-400 transition-all duration-300 ${
            isOpen
              ? "rotate-180 text-blue-400 bg-blue-500/10"
              : "group-hover:text-gray-300 group-hover:bg-gray-800/50"
          }`}
        >
          <svg
            className="w-4 h-4 transition-transform duration-300"
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
            strokeWidth={2.2}
          >
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              d="M19 9l-7 7-7-7"
            />
          </svg>
        </div>
      </button>

      {/* Smooth Popover Menu */}
      <div
        ref={listboxRef}
        role="listbox"
        aria-label="Target Tool"
        className={`absolute left-0 right-0 top-full mt-2 z-50 rounded-xl border border-gray-700/60 bg-gray-900/95 backdrop-blur-xl shadow-[0_20px_50px_rgba(0,0,0,0.7)] overflow-hidden ring-1 ring-white/10 transition-all duration-200 ease-out origin-top ${
          isOpen
            ? "opacity-100 scale-100 translate-y-0 pointer-events-auto"
            : "opacity-0 scale-98 -translate-y-1 pointer-events-none"
        }`}
      >
        {/* Menu Header */}
        <div className="px-3 py-2 border-b border-gray-800/80 bg-gray-950/40 flex items-center justify-between text-[11px] font-semibold text-gray-400 uppercase tracking-wider">
          <span>Available Tools</span>
          <span className="text-[10px] text-gray-500 font-mono lowercase">
            {tools.length} blueprint{tools.length === 1 ? "" : "s"}
          </span>
        </div>

        {/* Tool List */}
        <div className="p-1.5 space-y-1 max-h-72 overflow-y-auto custom-scrollbar">
          {tools.length === 0 ? (
            <div className="py-4 text-center text-xs text-gray-500">
              No tools available
            </div>
          ) : (
            tools.map((tool, index) => {
              const isSelected = tool.id === selectedToolId;
              const isHighlighted = index === highlightedIndex;

              return (
                <button
                  key={tool.id}
                  type="button"
                  role="option"
                  aria-selected={isSelected}
                  onMouseEnter={() => setHighlightedIndex(index)}
                  onClick={() => {
                    onSelect(tool.id);
                    setIsOpen(false);
                  }}
                  className={`w-full text-left px-3 py-2.5 rounded-lg flex items-center justify-between gap-3 transition-all duration-150 group cursor-pointer ${
                    isSelected
                      ? "bg-blue-600/15 border border-blue-500/30 text-white"
                      : isHighlighted
                      ? "bg-gray-800/70 border border-gray-700/40 text-gray-200"
                      : "border border-transparent text-gray-300 hover:bg-gray-800/40 hover:text-white"
                  }`}
                >
                  <div className="flex items-center gap-3 min-w-0">
                    <div
                      className={`w-8 h-8 rounded-lg flex items-center justify-center shrink-0 border transition-colors ${
                        isSelected
                          ? "bg-blue-500/20 border-blue-500/40 text-blue-400"
                          : "bg-gray-950/80 border-gray-800 text-gray-400 group-hover:border-gray-700 group-hover:text-blue-400"
                      }`}
                    >
                      <svg
                        className="w-4 h-4"
                        fill="none"
                        viewBox="0 0 24 24"
                        stroke="currentColor"
                        strokeWidth={2}
                      >
                        <path
                          strokeLinecap="round"
                          strokeLinejoin="round"
                          d="M13 10V3L4 14h7v7l9-11h-7z"
                        />
                      </svg>
                    </div>

                    <div className="min-w-0">
                      <div className="flex items-center gap-2">
                        <span
                          className={`text-sm font-medium tracking-tight truncate ${
                            isSelected ? "text-blue-300 font-semibold" : ""
                          }`}
                        >
                          {tool.name}
                        </span>
                        {tool.scope === "server" ? (
                          <span className="font-mono text-[10px] px-1.5 py-0.5 rounded bg-purple-500/10 text-purple-400 border border-purple-500/20 shrink-0">
                            server
                          </span>
                        ) : tool.default_mode === "spread" ? (
                          <span className="font-mono text-[10px] px-1.5 py-0.5 rounded bg-emerald-500/10 text-emerald-400 border border-emerald-500/20 shrink-0">
                            spread
                          </span>
                        ) : (
                          <span className="font-mono text-[10px] px-1.5 py-0.5 rounded bg-blue-500/10 text-blue-400 border border-blue-500/20 shrink-0">
                            worker
                          </span>
                        )}
                        <span className="font-mono text-[10px] px-1.5 py-0.5 rounded bg-gray-800/80 text-gray-400 border border-gray-700/60 shrink-0">
                          {tool.executable}
                        </span>
                      </div>
                      <p className="text-xs text-gray-400 truncate mt-0.5 max-w-[260px] sm:max-w-md">
                        {tool.description}
                      </p>
                    </div>
                  </div>

                  {/* Selected checkmark */}
                  {isSelected ? (
                    <div className="w-5 h-5 rounded-full bg-blue-500 text-white flex items-center justify-center shrink-0 shadow-sm shadow-blue-500/50">
                      <svg
                        className="w-3.5 h-3.5"
                        fill="none"
                        viewBox="0 0 24 24"
                        stroke="currentColor"
                        strokeWidth={3}
                      >
                        <path
                          strokeLinecap="round"
                          strokeLinejoin="round"
                          d="M5 13l4 4L19 7"
                        />
                      </svg>
                    </div>
                  ) : (
                    <div className="w-5 h-5 rounded-full flex items-center justify-center shrink-0 text-transparent group-hover:text-gray-500 transition-colors">
                      <svg
                        className="w-3.5 h-3.5"
                        fill="none"
                        viewBox="0 0 24 24"
                        stroke="currentColor"
                        strokeWidth={2}
                      >
                        <path
                          strokeLinecap="round"
                          strokeLinejoin="round"
                          d="M9 5l7 7-7 7"
                        />
                      </svg>
                    </div>
                  )}
                </button>
              );
            })
          )}
        </div>
      </div>
    </div>
  );
}
