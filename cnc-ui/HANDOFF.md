# CNC UI — Agent Handoff Document

> Read this first. Update the Session Log before finishing a session.

---

## What CNC Does (Simplified Model)

**One job = one input file → split into N equal parts → one command per worker.**

That's it. No output files, no exec modes, no byte-size chunking config.
The user provides a file and a command with `{input}` as placeholder:

```
cnc run targets.txt "node index.js {input}" --workers 5
```

The server splits the file into 5 equal parts and sends one chunk to each worker.

---

## Stack

| Thing | Version |
|---|---|
| Next.js | 16.3.4 |
| React | 19.2.8 |
| Tailwind | 4 (`@import "tailwindcss"` — NOT `@tailwind base`) |
| TypeScript | 5 |
| No component library — plain Tailwind only |

API proxy: `next.config.ts` rewrites `/api/*` → `http://localhost:8080/api/*` ✅

---

## Data Model (current — after simplification)

```typescript
interface Job {
  id: string;
  name: string;
  command: string;        // e.g. "node index.js {input}"
  input_file: string;     // path to the source file on the server
  workers: number;        // how many parts to split into (0 = auto)
  timeout_seconds: number;
  total_tasks: number;
  completed: number;
  failed: number;
  status: "pending" | "running" | "completed" | "failed" | "cancelled";
  created_at: string;
  started_at?: string;
  completed_at?: string;
}

interface Task {
  id: string;
  job_id: string;
  status: "pending" | "assigned" | "running" | "completed" | "failed";
  assigned_to?: string;
  created_at: string;
  started_at?: string;
  completed_at?: string;
  result?: { message?: string };
  error?: string;
  retry_count: number;
}

interface Worker {
  id: string; address: string; status: "online"|"offline"|"busy";
  max_tasks: number; current_load: number;
  last_seen: string; registered: string;
}

// REMOVED from Job: exec_mode, output_dir, split_size, split_count
// REMOVED from TaskResult: output_file, bytes_out
```

---

## File Map

```
cnc-ui/
  app/
    page.tsx                      → redirects /dashboard
    layout.tsx                    → root layout
    globals.css                   → Tailwind + dark theme vars
    dashboard/
      layout.tsx                  → SSR fetch + EventProvider + Sidebar
      page.tsx                    → home: StatsGrid + WorkerSummary + JobSummary
      workers/
        page.tsx                  → workers table  ✅
      jobs/
        page.tsx                  → jobs list + Submit button  ✅
        new/page.tsx              → submit form (input, command, workers, timeout)  ✅
        [id]/page.tsx             → job detail (live progress + task table)  ✅
  components/
    providers/EventProvider.tsx   → SSE + useReducer; useDashboard() → {...state, dispatch}
    dashboard/
      Header.tsx                  → sticky header, live/disconnected dot
      Sidebar.tsx                 → nav: Dashboard / Workers / Jobs
      StatCard.tsx                → single metric tile
      StatsGrid.tsx               → 4-up stats; polls /api/stats when SSE down
      WorkerSummary.tsx           → compact worker list (home)
      WorkerTable.tsx             → full worker table
      JobSummary.tsx              → compact job list, last 5 (home)
      JobTable.tsx                → full job table, workers column  ✅
      JobProgress.tsx             → live segmented progress bar (reads useDashboard)
      JobDetail.tsx               → detail panel + task list (alternate, not used by [id])
      TaskTable.tsx               → live task table (reads useDashboard)
      SubmitJobForm.tsx           → simplified form: input_file, command, workers, timeout  ✅
    ui/
      EmptyState.tsx / Skeleton.tsx / StatusBadge.tsx
  lib/
    api.ts                        → fetch helpers
    types.ts                      → aligned to Go structs (simplified)
```

---

## What Is Done ✅

- Full layout, SSE live updates, auto-reconnect, polling fallback
- Workers page
- Jobs list with Workers column
- Job detail page with live progress bar + task table
- Submit job form (simplified to 4 fields)
- Go server: `/api/events` SSE, all broadcasts wired
- Go CLI: `cnc run file.txt "cmd {input}"` — no flags required
- Both projects build cleanly

---

## What Could Still Be Built

| Item | Notes |
|---|---|
| Cancel job button on detail page | DELETE /api/jobs/:id exists |
| Connection banner (full-width) | show when SSE disconnected |
| Empty state on dashboard when no workers | nudge to start a worker |
| Toast on job complete | SSE job_update → toast |
| Pagination | only matters at 100+ rows |

---

## Key Patterns

1. `"use client"` only when using hooks, events, or browser APIs.
2. Data flows: SSR in `layout.tsx` → EventProvider seeds state → SSE updates → `useDashboard()`.
3. Tailwind 4: `@import "tailwindcss"`, no config file.
4. Palette: `gray-950` bg · `gray-900` cards · `gray-800` borders · `gray-400` secondary text.
5. `useDashboard()` returns `{ stats, workers, jobs, tasks, connected, lastUpdated, dispatch }`.
6. New page: `app/dashboard/<x>/page.tsx` + `components/dashboard/<Name>.tsx`.

---

## Session Log

| Date | Agent | What was done |
|---|---|---|
| 2026-09-03 | Kiro | Initial build: layout, EventProvider, workers page, jobs pages, submit form |
| 2026-09-03 | Kiro | SSE endpoint (Go), SplitCount fix, StatsGrid polling, split_count in JobTable |
| 2026-09-03 | Kiro | Simplified model: removed exec_mode/output_dir/split_size from API + UI. CLI is now `cnc run file.txt "cmd {input}"`. Both build clean. |
