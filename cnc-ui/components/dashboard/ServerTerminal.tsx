"use client";

import { useState, useRef, useEffect } from "react";
import { Terminal as TerminalIcon, Play } from "lucide-react";

interface CommandEntry {
  id: number;
  cwd: string;
  command: string;
  stdout: string;
  stderr: string;
  exitCode: number;
}

export function ServerTerminal() {
  const [cwd, setCwd] = useState("~");
  const [history, setHistory] = useState<CommandEntry[]>([]);
  const [input, setInput] = useState("");
  const [isRunning, setIsRunning] = useState(false);
  const scrollRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  // Auto-scroll to bottom when history changes
  useEffect(() => {
    if (scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [history]);

  // Focus input on mount and on clicks
  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  const handleCommand = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!input.trim() || isRunning) return;

    const cmd = input.trim();
    setInput("");
    setIsRunning(true);

    // Optimistic history entry
    const entryId = Date.now();
    setHistory((prev) => [
      ...prev,
      { id: entryId, cwd, command: cmd, stdout: "", stderr: "", exitCode: 0 },
    ]);

    try {
      const res = await fetch("/api/server/exec", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ command: cmd, cwd: cwd === "~" ? "" : cwd }),
      });

      if (!res.ok) {
        throw new Error(`HTTP ${res.status}`);
      }

      const data = await res.json();
      
      setHistory((prev) =>
        prev.map((item) =>
          item.id === entryId
            ? {
                ...item,
                stdout: data.stdout,
                stderr: data.stderr,
                exitCode: data.exit_code,
              }
            : item
        )
      );

      if (data.cwd) {
        setCwd(data.cwd);
      }
    } catch (err: any) {
      setHistory((prev) =>
        prev.map((item) =>
          item.id === entryId
            ? { ...item, stderr: `Failed to execute: ${err.message}`, exitCode: -1 }
            : item
        )
      );
    } finally {
      setIsRunning(false);
      setTimeout(() => inputRef.current?.focus(), 10);
    }
  };

  return (
    <div className="flex flex-col h-full bg-[#0c0c0c] text-[#cccccc] font-mono text-sm overflow-hidden" onClick={() => inputRef.current?.focus()}>
      
      {/* Header bar */}
      <div className="flex items-center px-4 py-2 bg-[#1e1e1e] border-b border-[#333333] shrink-0">
        <TerminalIcon className="w-4 h-4 mr-2 text-gray-400" />
        <span className="text-gray-300 font-semibold text-xs">CNC Web Shell</span>
        <span className="ml-auto text-xs text-gray-500">Connected</span>
      </div>

      {/* Terminal Output */}
      <div 
        ref={scrollRef}
        className="flex-1 overflow-y-auto p-4 space-y-4"
      >
        {history.length === 0 && (
          <div className="text-gray-500 text-xs italic mb-4">
            Welcome to the CNC Server Web Shell. Type a command to begin. (e.g., ls -la)
          </div>
        )}

        {history.map((entry) => (
          <div key={entry.id} className="space-y-1">
            <div className="flex items-center text-emerald-400">
              <span className="text-blue-400 mr-2">root@cnc:{entry.cwd}$</span>
              <span className="text-white">{entry.command}</span>
            </div>
            
            {entry.stdout && (
              <pre className="whitespace-pre-wrap break-words text-gray-300 mt-1">
                {entry.stdout}
              </pre>
            )}
            
            {entry.stderr && (
              <pre className="whitespace-pre-wrap break-words text-red-400 mt-1">
                {entry.stderr}
              </pre>
            )}

            {entry.exitCode !== 0 && (
              <div className="text-red-500/80 text-xs mt-1">
                [Exited with code {entry.exitCode}]
              </div>
            )}
          </div>
        ))}
      </div>

      {/* Input Area */}
      <form 
        onSubmit={handleCommand}
        className="flex items-center px-4 py-3 bg-[#1e1e1e] border-t border-[#333333] shrink-0"
      >
        <span className="text-blue-400 mr-2 whitespace-nowrap">
          root@cnc:{cwd}$
        </span>
        <input
          ref={inputRef}
          type="text"
          value={input}
          onChange={(e) => setInput(e.target.value)}
          disabled={isRunning}
          className="flex-1 bg-transparent border-none outline-none text-white focus:ring-0 p-0"
          autoFocus
          autoComplete="off"
          spellCheck="false"
        />
        {isRunning && <span className="ml-2 text-gray-500 text-xs animate-pulse">Running...</span>}
      </form>
    </div>
  );
}
