# Busy worker visibility

The dashboard's Online Workers table excluded `busy` workers even though they remained connected. Updated `cnc-ui/components/dashboard/WorkerTable.tsx` to include both `online` and `busy`; offline workers remain excluded.

Validation:
- Local TypeScript check: `tsc --noEmit --incremental false -p cnc-ui/tsconfig.json` passed.
- Production Next.js build and TypeScript check passed in an isolated release.
- Deployment SSM command: `9ea6e3de-f9de-4d3d-a961-58728cda75cd`, Success.
- Release: `/opt/cnc/dashboard-releases/worker-visibility-034d398da86b`.
- WorkerTable source SHA256: `034d398da86b2aed8b42fb043c7056e51ba5507323ba386b1f516a0481755544`.
- Only `cnc-ui` restarted. Controller and processor PIDs remained unchanged. Previous UI override retained in `rollback-ui.conf` inside the release.
- Browser verified LIVE with all three busy workers visible, each at 2/2 slots.

Processor diagnosis: read-only health and run metadata showed six `bf--scanner` runs in the running phase, two per worker; 31 additional runs were queued. All workers advertised the required program version. The newest 20 runs were queued, hiding the older active runs below the recent-runs cutoff. The generic queued message mentions online/version readiness even when all matching workers' slots are occupied. No job, scheduling limits, tool code, or processing state changed. Running phase does not by itself prove target-level progress or successful results.
