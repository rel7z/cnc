# Processor queue analysis — 19 October 2026

Scope: read-only service, queue, scheduler, and resource metadata. No private tool source inspection, tool execution, queue reset, application deployment, or application service restart.

## Observations

- Initial snapshot: enabled, 10,304 completed batches, 385 queued, zero running.
- Queue subsequently grew through 522, 530, and 711 pending batches while completed stayed at 10,304.
- At the detailed snapshot, 1,412 attempts were rejected. The eight latest attempts all selected `cnc-aws-worker-3`, approximately once per second. All eight had null `started_at`; they were rejected before execution authorization.
- All eleven workers were online, advertised processor-v2, and advertised the version required by the queued batches.
- The repeatedly selected worker reported zero current load, zero reservations, two CPUs, about 0.5% CPU use, and 3,175 MiB available RAM. These observations do not establish the exact HTTP refusal reason.
- Final health snapshot: processing disabled (`enabled=false`), status `ok`, 10,304 completed, 716 queued, zero running, no cluster error, eleven workers. This investigation did not change the processing flag. The actor responsible for that flag change was not established.

## Findings

1. The current lack of dispatch is explained by the paused flag. The controller checks enabled state before dispatching queued work.
2. Before processing became paused, repeated assignment rejection prevented progress. A momentary run status of running represented allocation, not confirmed tool execution: recent rejected attempts had no start timestamp.
3. The scheduler can repeatedly select the same least-loaded worker after HTTP 409. It ends the scheduling step after a rejection; rejection leaves that worker idle, so the following step selects it again. Live attempt metadata exhibits this pattern. This can prevent otherwise available workers from being tried.
4. Refusal diagnostics are lost. The broker client returns only the operation and HTTP status for non-success responses. The 409 handler then stores the generic message `CNC capacity or readiness changed`. The persisted reason does not identify whether the underlying refusal was resource admission, readiness, or another broker conflict.
5. The completed count is historical batch accounting. Synchronizing source files does not reset run history, capture offsets, or existing pinned program versions. Completed does not prove that useful findings were produced.

## Limits

The original HTTP 409 body was not persisted and was not recovered. Passive response observation found no matching packets after retries stopped; a later health check confirmed processing had become paused. No claim is made that physical RAM or CPU shortage caused the historical refusal. No queue resume or executable diagnostic assignment was performed. Temporary packet observers were terminated and their absence was verified.

## Proposed correction, not applied

- Preserve a bounded, sanitized broker refusal reason and expose it on the queued batch.
- On readiness/capacity rejection, apply per-worker backoff and try another eligible worker rather than continually selecting the same worker.
- Distinguish reserved/assigned work from execution confirmed by the worker in dashboard counters.
- Validate those control-plane changes against mock workers with a rejecting first worker and an available second worker before any production rollout.
- Keep source files, output files, run history, and capture checkpoints intact; resetting them does not repair assignment rejection.
