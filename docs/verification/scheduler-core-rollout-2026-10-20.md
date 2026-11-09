# Scheduler core rollout — 20 October 2026 (Asia/Jakarta)

The generic processor core repair was deployed to the existing AWS controller.
Only `cnc-processor` was restarted. Processing remained paused throughout.

## Changes

- A worker that returns HTTP 409 receives a five-second scheduling cooldown;
  another eligible worker can receive the next assignment.
- Rejected attempts and their queued run retain the worker identity and the
  broker's specific, allowlisted control-plane refusal reason.
- Arbitrary response bodies are withheld. Error-body reads are limited to
  1,024 bytes; unknown responses do not expose credentials or proxy payloads.
- Cooldown expiration restores eligibility; capacity and processing-state
  admission controls remain enforced.

## Verification

- The complete distributed-controller regression package passed locally
  (9.848 seconds).
- The Linux dummy-test binary ran successfully on the controller for all six
  requested scenarios: HTTP 409 failover, successful assignment without execution,
  paused scheduling, refusal-message redaction, cooldown expiry, and priority
  scheduling after capacity recovery. No real tool or queued job ran in these tests.
- Independent verification hashed the running process executable through
  `/proc/<pid>/exe`, confirming that the service loaded the new core binary.
- Service health: `ok`; systemd active/running; zero automatic restarts.
- Configuration hash, registered program versions, run counts, and absence of
  active attempts matched the pre-deployment snapshot.
- After rollout: paused, 10,304 completed, 716 queued, zero running.

## Artifacts and rollback

- New core SHA-256: `4f6dea34e8d8751f9a6336039ee771c36d7ba44888f9bc67f61104e0bd05b573`.
- Previous core SHA-256: `99e0a17eb2c78a0fd3b36fd411a574f980a4d4cb5893b5ded29cc12d4d01cc1c`.
- Configuration SHA-256: `a658b35245e85d7f7d899a2712e0ee699f2e2969fcb85e831f5b1fdba9f3b1b9`.
- Release directory on controller:
  `/opt/cnc-processor/scheduler-releases/1792429996732988102`.
- That directory contains the prior binary, a SQLite backup, a configuration
  backup, Linux dummy-test log, and rollout receipt. Transfers used the existing
  private S3 release bucket with server-side encryption and checksum verification.
- Rollout SSM command: `b8255eb1-3383-47ff-bd55-de661eda9282`.
- Independent verification SSM command: `6f8ad27e-593a-4ec5-8811-1573abc5e29a`.

## Limits

This verifies scheduler behavior and deployment health, not the behavior of any
private binary or its outputs. The historical HTTP 409 body was not recoverable;
future refusals now retain recognized specific reasons. No production workload
was resumed. Input files, output files, capture checkpoints and run history were
not reset. No private tool was inspected or reverse engineered.
