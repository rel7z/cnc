# Worker utilization — 20 October 2026

Read-only diagnosis. No private tool access, queue reset, restart, capacity change,
new job, or program execution was requested by this investigation.

## Live evidence

Metadata sample: `1792430317331` milliseconds Unix time, SSM command
`bcf47e72-d53f-4559-907b-40b4bd4e2132`.

- `172.31.5.83` maps to `cnc-aws-worker-3`, instance `i-024d40c7fc4583647`.
- That worker reports online, processor-v2, the required installed version,
  zero load, zero reservations, two CPUs, about 0.5% CPU usage, 3,164 MiB available
  memory, and fresh telemetry.
- Three recent assignment attempts for that worker were rejected by the broker
  with HTTP 409 and the specific message `worker unavailable`; none reached a
  worker execution start timestamp. The new scheduler retained the reason and
  dispatched other eligible workers after the rejection cooldown.
- Processing is now enabled. Six attempts are genuinely running, with populated
  start timestamps. Completed batches increased from 10,304 to 10,309.
- The queue contains 786 batches. The active program has a cluster-wide parallel
  limit of six, and 100 queued rows explicitly report that concurrency limit.
- Worker limits are empty in configuration. The default processor admission
  limit is two per worker, bounded further by CPU count, available task slots,
  resource reservations, and telemetry/cgroup checks. This is distinct from
  the dashboard's overall transport slot count of four on AWS or eight on Linode.
- A separate enum job has eleven task shards. Current worker load includes its
  tasks as well as the six processor attempts, explaining why many nodes show
  one or two tasks despite low measured CPU utilization.

## Connection discrepancy

The broker rejects unavailable workers if the record is absent, its command
channel is nil, it is offline, or its protocol differs. Live discovery shows the
record, online status and expected protocol. This makes a stale/missing command
channel or a connection-state race the leading explanation; it does not indicate
RAM exhaustion. Existing heartbeat handling can restore online status while
leaving a cleared command channel nil. Connection cleanup also identifies a
worker solely by ID, so stale connection cleanup is another possible contributor.

The exact historical connection event has not been proved. A bounded historical
journal query exceeded its 45-second observation limit; it produced no additional
verified lifecycle evidence. This report does not claim a confirmed disconnect
trigger or a repaired connection.

## Meaning

The idle AWS worker has a readiness/connection problem, while cluster utilization
is separately limited by program and per-worker concurrency policy. Raising a
slot count alone does not repair a broker connection, and low CPU percentage does
not override admission policy. No limits or worker services were changed.

## Subsequent connection recovery

On the owner's follow-up request to fix it, only the idle
`cnc-aws-worker-3` service was stopped and started. Before stopping it, controller
metadata showed zero assignments/reservations and the worker process had no child
tasks. The controller confirmed the old connection offline before the replacement
process was started. No controller or other worker was restarted.

The replacement worker registered online with fresh processor-v2 telemetry.
A deliberately invalid inner worker ID then returned HTTP 400
`invalid worker assignment`, rather than HTTP 409 `worker unavailable`.
That guard is reached only after the controller finds an available worker record
and non-nil command channel. The malformed probe fails before any task is created
or sent; it does not execute a program.

At final verification, all six global processor slots remained occupied and this
worker still had zero current load. Its connection was ready, but actual receipt
of a new production job had not yet been observed. Concurrency settings were not
changed. This is recovery of the stale connection, not a claim that controller
connection-lifecycle code has been permanently corrected.

Evidence SSM commands:
- Idle worker stop: `d1421b38-5e82-48ff-abda-eed4715d92fb`.
- Old connection confirmed offline: `a1de2f7b-ce8c-4b16-b805-1fd17c44fecd`.
- Worker start: `e711a57a-14c5-43de-bd69-364297865908`.
- Independent readiness verification: `58a0eb59-5933-44ba-8c89-99e9ce8c0455`.
