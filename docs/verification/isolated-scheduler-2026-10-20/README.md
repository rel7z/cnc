# Isolated scheduler probe — 20 October 2026

The initial authorization covered local dummy testing. The owner subsequently
requested the generic core repair and production rollout. The overlay probes
exercise the controller against an ephemeral localhost broker and a temporary SQLite database.
They never construct a worker runner, execute a program, install a bundle, access
private tools, or call a production/cloud endpoint. The initial probe reproduced
the defect; the current probe verifies the subsequent generic scheduler repair.

## Verified results

Initial diagnostic run: all three tests passed (package result: 0.553 seconds),
meaning the defect was reproduced rather than repaired at that time.

Current verification after the repair:

| Test | Observed behavior |
| --- | --- |
| `TestIsolated409Failover` | Worker A returns HTTP 409, enters a five-second cooldown, and worker B receives the next assignment. The specific refusal survives in the rejected attempt and blocked-run record. No runner executes. |
| `TestIsolatedAcceptControl` | A successful mock acknowledgment moved the run to running, but the attempt remained assigned. No program executed. |
| `TestIsolatedPausedControl` | A paused scheduler retained the queued dummy and sent no assignment. |

The mock 409 reason is deliberately injected; it does not prove the historical
production refusal had the same cause. Additional tests verify cooldown expiry,
unknown-response redaction, and priority scheduling after recovery. The complete
distributed-controller regression package, which includes benign temporary
fixture programs, passed in 9.848 seconds. These tests
do not by themselves establish production health or real tool results.

## Reproduction

From the repository root, create a temporary Go overlay that adds the diagnostic
test to the controller package without adding it to production source:

```sh
python3 - <<'PY'
import json
from pathlib import Path
r = Path.cwd()
overlay = {'Replace': {
    str(r / 'processor/internal/distributed/isolated_queue_probe_test.go'):
    str(r / 'docs/verification/isolated-scheduler-2026-10-20/queue_probe_test.go')
}}
Path('/tmp/cnc-isolated-scheduler-overlay.json').write_text(json.dumps(overlay))
PY
env GOPROXY=off GOTOOLCHAIN=local GOCACHE=/tmp/cnc-isolated-go-cache \
  go test -C processor -overlay /tmp/cnc-isolated-scheduler-overlay.json \
  -run '^Test(Isolated|BrokerRefusal|WorkerRejection|RankedAdmission)' \
  -count=1 -v ./internal/distributed
```

The command requires the local Go toolchain and already cached dependencies.
Dependency downloads are disabled. An execution sandbox must allow ephemeral
localhost listeners for `httptest`; no external connectivity is required.

Only the explicitly named diagnostic tests run. Do not replace the test pattern
with a full repository test or tool discovery command.
