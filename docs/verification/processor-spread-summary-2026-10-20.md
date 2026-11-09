# Processor spread and completion summary

Scope: core scheduling for the existing `wordpress.txt` route. Private tool
contents are not inspected, copied, changed, or directly executed by this work.

## Behavior

- Divide newly captured records across workers with fresh telemetry; counts
  differ by at most one for a stable worker set. Persist each share's worker
  identity, input, and capture cursor atomically. Appends continue the rotation.
- Run at most one processor share on each assigned worker. Busy workers retain
  their queued share. Existing active attempts remain intact.
- Set `auto_balance.interrupt_running=false`: CPU pressure holds new admissions
  without terminating running attempts. Worker shutdown, explicit cancellation,
  lost leases, and operating-system memory limits can still stop a process.
- Require exit zero and a saved `processed = X/Y` line before completing this
  input route. Y must equal the input record count and 0 <= X <= Y. X is the
  program's reported count, not independently interpreted by the scheduler.
  Missing or invalid summaries produce a failed run with an explicit reason.
- Repartition only never-started queued records, preserving version pins,
  ancestry, limits, capture cursors, completed output, and active attempts.

## Verification

Local targeted tests passed on 2026-10-20 for store, watcher, and distributed
controller. Fixtures include 23 records distributed 8/8/7 across three actual
localhost runners; saved summaries are required before completed. Additional
tests cover append/restart deduplication, snapshots larger than 8 MiB, atomic
cursor rollback, worker affinity, high CPU without interruption, bad summaries,
and immutable-version preservation during queued redistribution.

Linux test binaries are included in the release and must pass their dummy-only
tests before production changes. Only the processor core service is restarted;
the existing registered program versions are retained through startup pins.
Backups of the old core, environment, configuration, and database are retained.

## Production evidence

Before rollout, the existing core was
`0ff0d26a5d60857772bc2fcf4132d3150a1a4c752791737d1d4a97317d28f96e`.
Read-only preflight found eleven online workers, two active attempts, and 2,727
never-started queued runs containing 10,295 records. Dispatch was paused.
No completed input is reset or rerun.

The first rollout was rolled back because startup compared connected programs
before resolving their internal target pins. During that failed startup window,
two existing attempts became interrupted; their input and stored output were
retained, and they were not automatically retried. The repair resolves target
pins before comparing settings. Core-only startup now reconstructs retained
manifest groups from registered metadata, so source files and manifests do not
need reopening. A regression test removes both dummy sources and their manifest
before startup, and a separate test resolves an absent binary manifest entirely
from saved architecture metadata.

Before any subsequent restart, the release runs `validate-retained` against a
disposable database copy. It refuses unpinned programs and starts neither a
watcher nor a broker connection. Linux dummy tests also run before installation.


## Installed release

- Release: `/opt/cnc-processor/spread-releases/1792492132848105949`.
- Running core SHA-256:
  `0e76faba2eaa2e1a35254f5784cad4c30f80fa59acf2d1a3b0b3dd68af9bbaa4`.
- Linux dummy store, watcher, and controller tests passed before installation;
  retained-config preflight also passed on a disposable database.
- 2,727 never-started runs became eleven worker-pinned shares. Ten workers each
  received 936 records and one received 935; total 10,295.
- Input multiset SHA-256 matched before and after:
  `afe6d40339b3c493e15b446830e2f49a99d1aa12745cbd00d65ba2d7b3e50fb3`.
- All registered program versions stayed unchanged. Neither the CNC controller
  nor any worker service was restarted. Dispatch is enabled.
- The installer initially saw a null worker snapshot immediately after startup.
  The core was healthy and remained paused; maintenance was resumed without
  another restart after treating a null snapshot as an empty, warming list.
- Receipt: `receipt.json` in the release directory. The successful maintenance
  SSM command was `3df0d893-891a-4413-bfe2-f1a1eaa11468`.
- The second restart's exact duration was not recorded; the receipt reports null
  rather than claiming a measured lease-preservation window.

The first failed rollout and its two interrupted historical attempts are an
explicit limitation of this rollout, not a guarantee that those processes were
preserved. Their retained inputs have not been silently rerun.


Live post-rollout verification found all eleven workers each running exactly one
processor attempt, no scheduler error, enabled dispatch, matching loaded binary
hash, `distribution=spread`, `require_summary=true`, and
`interrupt_running=false`. Additional newly captured file records were also
queued in balanced worker shares; the watcher preserved rotation after the
initial 10,295-record repartition. No new production share had completed at this
observation, so production completion is not claimed here. The saved-summary
completion gate was verified by the release's Linux dummy end-to-end tests.
Remote evidence: `post-rollout-verification.json` in the release directory.
