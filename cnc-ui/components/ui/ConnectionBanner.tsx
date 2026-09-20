"use client";

import { useDashboard } from "@/components/providers/EventProvider";

export function ConnectionBanner() {
  const { connected } = useDashboard();

  if (connected) return null;

  return (
    <div
      role="alert"
      aria-live="assertive"
      className="fixed top-0 left-0 right-0 z-50 flex items-center justify-center gap-2 bg-yellow-900/90 backdrop-blur text-yellow-200 text-xs py-2 px-4"
    >
      <span className="relative flex h-2 w-2">
        <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-yellow-400 opacity-75" />
        <span className="relative inline-flex rounded-full h-2 w-2 bg-yellow-400" />
      </span>
      Connection lost — reconnecting to server…
    </div>
  );
}
