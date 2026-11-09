# Safe worker maintenance

Worker shutdown now withdraws advertised capacity, preserves active task contexts
and leases, and waits for final result transmission before exiting. A persistent
systemd stop guard protects service restarts. `KillMode=mixed` avoids signalling
task children together with the main worker; `TimeoutStopSec=infinity` prevents
systemd's normal stop timeout from escalating a long drain to SIGKILL.

The guard is a separate binary at `/opt/cnc/worker-stop-guard`, so restoring an
older `/opt/cnc/cnc-worker` does not silently remove maintenance protection.
Capability metadata includes PID and process start time to reject stale markers.
For the first upgrade, a legacy worker is stopped only after its child processes
across all Go threads have finished and remained absent for ten seconds.

This prevents maintenance-triggered cancellation. OOM kills, explicit task
cancellation, a host crash, or an administrator's SIGKILL remain separate causes.
Do not force-stop a service shown as `deactivating (stop)` while it is draining.

## Installation and inspection

Stage the built Linux core worker at `/opt/cnc/cnc-worker-drain-fix.staged`, then
run `install-worker-drain.py` on the worker with its expected SHA256. It validates
the binary, saves replacements under `/opt/cnc/restart-rollback/`, installs the
guard and drop-in, verifies effective systemd properties, and requests an
asynchronous restart. It does not activate or test private tools.

Run `check-worker-drain.py` on the worker to report service state, core binary
hashes, capability PID match, and child counts. It does not inspect task contents.
A successful restart has `active/running`, no Job or ControlPID, a matching
capability PID, and the expected running core hash. An upgrade with active legacy
work remains pending with its old MainPID, nonzero ControlPID and Job.

Rollback copies and exact original paths are recorded in `manifest.json` in each
backup directory. Restore only the main worker binary if reverting application
behavior; retain the stable guard/drop-in unless separately repairing shutdown.
Request a normal service restart and allow it to drain.

## Verification on 2026-10-21

Core artifact SHA256:
`fa529cb6f490169743eaa76fa44ae05cb05959e7265901d3a62ec2645fb708a0`.

Targeted Go tests passed for drain capacity, task survival, observer cancellation,
legacy child enumeration, processor admission at zero capacity, shell tool
resolution, spread dispatch, and processor proxy access. An isolated Linux test
with a loopback fake controller proved a dummy task survives SIGTERM, produces
its result, and the worker exits cleanly. A second dummy proved the legacy guard
waits for a child process before stopping its parent.

Guard/drop-in and new binary were installed on workers 2–8; all seven had matching
installed core/guard hashes and infinite stop timeout with mixed kill mode.
Workers 2, 3, 4, 6, 7 restarted onto the new core. Workers 5 and 8 initially retained
their existing active process and child while the guard waited:

| Worker | MainPID | Guard PID | systemd restart job |
| --- | --- | --- | --- |
| 5 | 83675 | 93518 | 72303 |
| 8 | 95793 | 108409 | 73497 |

At that check, processor counts remained 2 running / 5 interrupted / 0 failed;
the rollout added no interrupted run. These two restarts must be checked again
before declaring the rollout complete. The controller download artifact was
updated with backup; controller and processor services were not restarted.

A second real systemd restart of idle upgraded worker 7 also passed: restart job
75547 finished, its main PID changed from 102850 to 103141, the core hash stayed
correct, and the service returned to active/running with no pending job. All seven
workers were online afterward; the two running attempt IDs were unchanged:
`attempt-1792556794421062610-a7cad214` (worker 5) and
`attempt-1792556797420784684-ee65c2a7` (worker 8).

The active-task systemd proof also passed using
`test-worker-drain-remote.py CORE_BINARY --systemd`. A uniquely named temporary
service used the production ExecStop, infinite stop timeout and mixed kill mode.
Stopping it while its loopback dummy task was running preserved the child,
advertised zero capacity, delivered the successful result and exited cleanly.
The temporary unit was removed afterward. Proof artifacts are at
`/tmp/cnc-drain-proof-j1r2okwt` on the controller; production services and jobs
were untouched by this test.

## Final rollout completion

The owner subsequently explicitly chose to stop both old tasks and complete the
two pending restarts immediately. Only the verified old main worker PIDs were
sent SIGTERM; the already pending systemd jobs completed normally. Worker 5 now
runs PID 94550 and worker 8 runs PID 110169, both on the expected new core hash.

A final check of every worker 2–8 confirmed active/running, no pending systemd
restart job or stop-guard process, matching running/installed/guard hashes,
matching capability PID, and persistent infinite stop timeout with mixed kill
mode. The controller reported all seven workers online. Both old runs are now
interrupted, as approved, with counts 0 running / 7 interrupted / 0 failed.
No checkpoint reset, retry request, private tool inspection, or result deletion
was performed as part of this rollout.
