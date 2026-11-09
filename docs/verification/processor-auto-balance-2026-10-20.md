# Processor CPU auto-balance — 20 October 2026

The processor core now supports an opt-in `auto_balance` policy. The existing
production config enables it with this policy:

```json
"auto_balance": {
  "enabled": true,
  "pause_cpu_percent": 75,
  "emergency_cpu_percent": 90,
  "recover_cpu_percent": 85,
  "cooldown_seconds": 10
}
```

## Behavior

- Each worker below 75% CPU may accept new processor work. At 75% or higher,
  new assignments to that worker wait; other eligible workers may continue.
- Above 90%, revoke one running processor attempt on that worker. Wait ten
  seconds before reassessing. Continue shedding one attempt per interval until
  CPU is at most 85%. Admission still requires CPU below 75%.
- Existing lease renewal detects revocation, normally within five seconds, and
  the worker cancels its process group. Revocation is not a synchronous remote
  kill; network loss can delay cancellation until the existing 30-second lease.
- Enumeration jobs and attempts already uploading results are not selected.
  Retry-safe attempts retain their existing retry budget; other interrupted
  attempts retain history/input and require an explicit retry.
- Implicit six-per-program and two-per-worker defaults are replaced by hardware
  capacity. Explicit program/worker overrides remain effective. Task slots,
  CPU reservations, memory safeguards, cgroup restrictions and fresh telemetry
  remain mandatory; this is not unlimited process creation.
- The operator's manual processing switch remains authoritative. It was paused
  before deployment and remains paused afterward. No new production workload
  was resumed by this rollout.

## Verification

- Complete local test packages passed: distributed controller (10.986 s), store
  (2.930 s), cluster resource/runner contracts (44.350 s).
- New dummy tests verify ten concurrent allocations across three workers,
  legacy behavior when disabled, explicit caps, gradual emergency shedding,
  lease fencing, the ten-second cooldown, recovery at 85%, holding at 75%, and
  refusing stale resource telemetry without killing work.
- Existing regressions cover manual pause, retained active remote leases after
  core restart, broker 409 failover and refusal-message redaction.
- Linux fixture tests passed on the production controller before installation.
  Fixtures use temporary inert files/mock services, without running private tools.
- Core restart took 0.392 seconds. Three existing active attempts were checked;
  none was unexpectedly interrupted. Registered program versions and the manual
  processing switch were preserved. Only `auto_balance` changed in config.
- Controller and worker services were not restarted. No tools were inspected,
  changed, synced, reverse engineered or activated by this rollout.
- Independent SSM verification `f1659bf7-0367-45a9-876d-93c3a474256f`
  confirmed the loaded executable hash and policy, all eleven workers online,
  healthy API, 4,779 queued / three running / zero interrupted, and the existing
  eleven-task enumeration job still running. Historical stored program limit
  values remain six to preserve pinned specs; adaptive admission overrides only
  the implicit default when the policy is enabled.
- High-CPU behavior was tested with simulated telemetry, not by stressing
  production hosts. Live dispatch beyond six is not claimed while manually paused.

## Release and rollback

- Core SHA-256:
  `0ff0d26a5d60857772bc2fcf4132d3150a1a4c752791737d1d4a97317d28f96e`.
- Controller release directory:
  `/opt/cnc-processor/balance-releases/1792439608458549128`.
- Rollout SSM command: `fc37babf-310a-4efd-9912-0a8cd7cfed76`.
- Release retains the previous core, config backup, SQLite backup, fixture test
  log and receipt. The installer rolls back core/config on failed health checks;
  it never restores the database over legitimate progress from live workers.
- Artifact transport used the existing private S3 release bucket with AES256
  encryption and SHA-256 verification.

Source: `processor/cluster/types.go`, `processor/internal/store/distributed.go`,
`processor/internal/distributed/controller.go`,
`processor/internal/distributed/auto_balance.go`, and its fixture tests.
