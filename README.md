# CNC Dashboard

Real-time monitoring UI for the CNC distributed task cluster.

## Stack

- **Next.js 16** (App Router, TypeScript)
- **Tailwind CSS v4** (dark theme)
- **Server-Sent Events** for live push updates from the Go server

## Prerequisites

- Go 1.21+
- Node.js 18+

## Quick Start

### 1. Start the Go server

```bash
cd cnc-api
make build
./cnc-server
```

The server listens on:
- HTTP API: http://localhost:8080
- TCP (worker connections): localhost:9090
- SSE stream: http://localhost:8080/api/events

### 2. (Optional) Start a worker

```bash
./cnc-worker
```

### 3. Start the dashboard

```bash
cd cnc-ui
npm install
npm run dev
```

Open [http://localhost:3000](http://localhost:3000).

## Pages

| Route | Description |
|-------|-------------|
| /dashboard | Stats overview + worker/job summaries |
| /dashboard/workers | All connected workers with live load |
| /dashboard/jobs | All jobs with progress bars |
| /dashboard/jobs/[id] | Job detail + live task list |

## API Proxy

All /api/* requests from the browser are proxied to http://localhost:8080 via next.config.ts. No CORS configuration needed.

## Live Updates

The dashboard connects to GET /api/events (SSE) on mount. Events:

| Event | Payload |
|-------|---------|
| snapshot | Full state on first connect |
| worker_update | Single worker state change |
| job_update | Single job state change |
| task_update | Single task state change |
| stats_update | Aggregated cluster stats |

If the connection drops, the dashboard shows a yellow banner and reconnects automatically after 3 seconds.

## Submit a Job (CLI)

Job submission is done via the CLI until the API is simplified:

```bash
cd cnc-api
./cnc job submit
```
