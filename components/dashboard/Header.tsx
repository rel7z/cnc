"use client";

import { useDashboard } from "@/components/providers/EventProvider";

interface HeaderProps {
  title: string;
}

function formatTime(date: Date | null): string {
  if (!date) return "—";
  return date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

export function Header({ title }: HeaderProps) {
  const { connected, lastUpdated } = useDashboard();

  return (
    <header className="sticky top-0 z-10 bg-gray-950/80 backdrop-blur border-b border-gray-800 px-6 py-3 flex items-center justify-between">
      <h1 className="text-sm font-semibold text-white">{title}</h1>
      <div className="flex items-center gap-4">
        {lastUpdated && (
          <span className="text-xs text-gray-500">
            Updated {formatTime(lastUpdated)}
          </span>
        )}
        <div className="flex items-center gap-1.5">
          <span className="relative flex h-2 w-2">
            {connected && (
              <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75" />
            )}
            <span
              className={`relative inline-flex rounded-full h-2 w-2 ${
                connected ? "bg-emerald-400" : "bg-gray-600"
              }`}
            />
          </span>
          <span
            className={`text-xs ${connected ? "text-emerald-400" : "text-gray-500"}`}
          >
            {connected ? "Live" : "Disconnected"}
          </span>
        </div>
      </div>
    </header>
  );
}
