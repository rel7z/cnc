"use client";

import { useDashboard } from "@/components/providers/EventProvider";
import { useTick } from "@/lib/utils";

function relativeTime(isoString: string | undefined): string {
  if (!isoString) return "never";
  const diff = Date.now() - new Date(isoString).getTime();
  const secs = Math.floor(diff / 1000);
  if (secs < 60) return `${secs}s ago`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.floor(hours / 24)}d ago`;
}

export function GDriveStatus() {
  useTick(1000);
  const { stats } = useDashboard();

  if (!stats?.gdrive_enabled) {
    return null; // Don't show if Google Drive is disabled
  }

  const uploaded = stats.gdrive_uploaded ?? 0;
  const pending = stats.gdrive_pending ?? 0;
  const lastUpload = relativeTime(stats.gdrive_last_upload);

  return (
    <div className="bg-gray-900 border border-gray-800 rounded-xl">
      <div className="px-5 py-3 border-b border-gray-800">
        <div className="flex items-center gap-2">
          <svg
            className="h-4 w-4 text-blue-400"
            fill="currentColor"
            viewBox="0 0 24 24"
            aria-hidden="true"
          >
            <path d="M12.01 1.485c-2.082 0-3.754.808-4.988 2.042l-4.5 4.5a7.014 7.014 0 0 0 0 9.915l4.5 4.5c1.234 1.234 2.906 2.042 4.988 2.042s3.754-.808 4.988-2.042l4.5-4.5a7.014 7.014 0 0 0 0-9.915l-4.5-4.5c-1.234-1.234-2.906-2.042-4.988-2.042zm0 1.5c1.653 0 3.034.648 4.103 1.717l4.5 4.5a5.514 5.514 0 0 1 0 7.796l-4.5 4.5c-1.069 1.069-2.45 1.717-4.103 1.717s-3.034-.648-4.103-1.717l-4.5-4.5a5.514 5.514 0 0 1 0-7.796l4.5-4.5c1.069-1.069 2.45-1.717 4.103-1.717z" />
            <path d="M7.5 10.5h9v1.5h-9z" />
            <path d="M10.5 7.5v9h1.5v-9z" />
          </svg>
          <h2 className="text-sm font-semibold text-white">Google Drive</h2>
          <span className="ml-auto flex items-center gap-1.5">
            <span className="relative flex h-2 w-2">
              <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75" />
              <span className="relative inline-flex rounded-full h-2 w-2 bg-emerald-400" />
            </span>
            <span className="text-xs text-emerald-400">Active</span>
          </span>
        </div>
      </div>

      <div className="p-5">
        <div className="grid grid-cols-3 gap-4">
          {/* Uploaded Count */}
          <div className="bg-gray-800/50 rounded-lg p-3 border border-gray-700/50">
            <div className="text-xs text-gray-500 mb-1">Uploaded</div>
            <div className="text-2xl font-bold text-emerald-400 tabular-nums">
              {uploaded.toLocaleString()}
            </div>
          </div>

          {/* Pending Count */}
          <div className="bg-gray-800/50 rounded-lg p-3 border border-gray-700/50">
            <div className="text-xs text-gray-500 mb-1">Pending</div>
            <div className="text-2xl font-bold text-blue-400 tabular-nums">
              {pending.toLocaleString()}
            </div>
          </div>

          {/* Last Upload */}
          <div className="bg-gray-800/50 rounded-lg p-3 border border-gray-700/50">
            <div className="text-xs text-gray-500 mb-1">Last Upload</div>
            <div className="text-sm font-medium text-gray-300 mt-2">
              {lastUpload}
            </div>
          </div>
        </div>

        {pending > 0 && (
          <div className="mt-3 flex items-center gap-2 text-xs text-gray-400 bg-blue-500/10 border border-blue-500/20 rounded-lg px-3 py-2">
            <svg
              className="h-3 w-3 animate-spin text-blue-400"
              fill="none"
              viewBox="0 0 24 24"
              aria-hidden="true"
            >
              <circle
                className="opacity-25"
                cx="12"
                cy="12"
                r="10"
                stroke="currentColor"
                strokeWidth="4"
              />
              <path
                className="opacity-75"
                fill="currentColor"
                d="M4 12a8 8 0 018-8v8z"
              />
            </svg>
            <span>
              {pending} {pending === 1 ? "file" : "files"} waiting for upload...
            </span>
          </div>
        )}
      </div>
    </div>
  );
}
