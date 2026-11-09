# Scheduler memory fairness verification — 2026-10-16

## Requested behavior

Keep processing paused and preserve saved `bf` and `divi` templates. Fix automatic resource allocation so one ordinary task does not reserve nearly all memory and incorrectly block another available CPU slot. Preserve CPU limits, explicit memory reservations, fresh telemetry requirements, and enforced cgroup protection. Do not access or execute private tool programs.

## Implementation

- Automatic reservations use a share of the normal 70% job memory pool per runnable CPU/task slot, with the existing 512 MiB minimum admission request.
- The broker normalizes older automatic burstable reservations for accounting only. Existing task resource records and hard limits remain unchanged.
- Admission accounts for the greater of reserved memory and observed aggregate job memory. Since `MemAvailable` already excludes resident job memory, only the unused portion of reservations is subtracted from it.
- Queued API responses show `processing is paused` while paused rather than an obsolete memory-block reason.
- The deployment helper refuses a controller restart when jobs are active, verifies service and worker recovery, keeps binary/database backups, and uses atomic replacement for install and rollback.

## Evidence

Three production worker samples had 3812 MiB total RAM, two CPUs, and one running task each:

| Available MiB | Job usage MiB | Previous automatic reservation MiB | Fair reservation MiB |
| --- | --- | --- | --- |
| 3197 | 51 | 2116 | 1334 |
| 3181 | 63 | 2109 | 1334 |
| 3114 | 110 | 2106 | 1334 |

The cluster fixture accepts a new 512 MiB request beside each old task while preserving explicit reservations and rejecting insufficient CPU/memory/headroom. The broker fixture verifies admission without altering or redelivering the existing task.

Local race tests passed for `./cluster ./api ./internal/distributed`. Targeted broker/capacity tests also passed locally.

Linux staged release: `/opt/cnc/scheduler-releases/59e5faa5af2cc6fe`.

SSM `05f71e6d-683a-4e29-85d7-55080d67090e` runs the isolated build and race suite. Its processor log reports:

- cluster: PASS, 46.447s
- API: PASS, 2.495s
- distributed: PASS, 57.714s

The distributed test checks ten 1000-line runs across six synthetic workers, live publication, no duplicate/missing lines, and unchanged source/cache. Its verification now drains the durable publication outbox: production deliberately transfers one bounded batch per tick, so execution completion alone does not prove publication has finished. Two harmless local-only fixture programs are included in the release archive; private `processor/tools/` is excluded.

Focused Linux proof, SSM `9236d1ff-3fa0-41b3-b6d5-89da036f7c7a`, passed all six selected tests without skips:

- hard 64 MiB child limit kills only the oversized child;
- missing cgroup enforcement fails closed;
- the three production telemetry examples admit a small job;
- resident memory is not double-counted;
- automatic jobs share slots and stay within CPU/memory bounds;
- a 96 MiB allocation can exceed its 64 MiB soft reservation under a 192 MiB isolated hard ceiling, without OOM.

## Production state and remaining rollout gate

SSM `bc61a7b7-f158-4610-b480-d5eeda345849` confirmed processor disabled, 36 queued, zero running; saved templates `bf` and `divi` revision 2 correspond to the applied definitions.

SSM `450ebc11-c227-43c2-93fa-88ab005195b4` confirmed enum job `enum_4d80c85363447ce57c3f719a` still running with three shards. Current controller recovery marks in-flight enum tasks failed after restart. Do not restart it while that job remains active without an explicit decision to interrupt/restart the job.

Build and production activation are separate gates. This document does not claim the broker patch is active in production. Record final build hashes, processor activation, and controller activation below when independently verified.

## Verified build and processor activation

The staging command completed successfully, including targeted Linux broker/capacity race tests and both static binary builds.

- Source SHA-256: `59e5faa5af2cc6fe58817100c5e35c3c8834c0547fb012c30a8b5b05e99162a2`
- Processor binary SHA-256: `99e0a17eb2c78a0fd3b36fd411a574f980a4d4cb5893b5ded29cc12d4d01cc1c`
- Controller binary SHA-256: `dc1c9545db93e5f9b2394d6786dbcd7248cdf03d8d23e7be80999274679a5072`

SSM `79709d6a-0e4a-4ffd-96e2-92a6a1bb887c` installed and checksum-verified only the processor binary. Backup: `/opt/cnc/scheduler-releases/59e5faa5af2cc6fe/rollback-1792099356157282369`. The deployment verifier checked unchanged queue counts, disabled state, and paused reasons on all queued API responses.

SSM `986ca0c5-028e-4296-86d5-8c7655e46c95` independently reverified: healthy, disabled, 36 queued, zero running, both templates still revision 2, and the same applied setup revision `a658b35245e85d7f7d899a2712e0ee699f2e2969fcb85e831f5b1fdba9f3b1b9`.

SSM `5ec6f303-54bf-4a56-95bf-f4c3ff6aa090` confirmed the existing three-shard enum job is still running. The controller candidate remains staged, not installed. The full memory-fairness change is therefore **not yet active in production**; it requires controller activation after that job finishes or an explicit decision to interrupt/restart it. No workers were restarted and no private tool was run.

## Approved controller rollout completed

The owner subsequently approved the restart (`restart aja uda`). SSM `ab89039b-dbb0-4847-b109-e2fe5daae651` installed the tested controller candidate, restarted it, verified its checksum, and preserved processor pause and queue counts. Controller rollback copy: `/opt/cnc/scheduler-releases/59e5faa5af2cc6fe/rollback-1792099968270508777`.

Controller recovery marked the previous three-shard enum job failed as disclosed before approval. Its worker processes initially remained active. SSM `fd9c6f59-3608-4d48-9393-56739b6cf503` restarted all three existing worker services to stop those orphaned executions. The previous enum job was not automatically resubmitted.

Independent final verification, SSM `74c68ef9-08db-4311-86a3-5ee0f39192de`, proved:

- controller, processor, and UI services active;
- hashes of the actual running controller and processor executables (`/proc/<MainPID>/exe`) exactly match the Linux-tested candidates above;
- all three workers online with zero active load and fresh telemetry (1.0–1.3 seconds old);
- processor still disabled, 36 queued, zero running, with paused reasons verified;
- both saved templates remain revision 2 and applied setup revision is unchanged;
- previous enum job is failed, as expected for the approved interruption.

The memory-fairness patch is now active in production. Processor execution remains paused by owner request. Private tools were neither inspected nor activated. Earlier pending-rollout statements above describe the pre-approval state and are superseded by this section.
