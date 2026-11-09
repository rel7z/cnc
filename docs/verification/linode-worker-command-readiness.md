# Worker command readiness: exit 127 fixed

The affected command was `./tools/dork2`. Newly provisioned Linode workers had only `cnc-enum` because the controller's downloadable worker archive contained an incomplete tool installation. SSH, registration and release-marker download checks did not detect this command-readiness defect.

## Applied fix

- Rebuilt the controller archive from its seven supported generic executables and the complete dork2 source/dependencies, excluding processor/tools, symlinks, Git metadata and credential files.
- Published an integrity manifest for all 1373 included files. Archive SHA256: `8c82292b656475dd40fe4354cd8f76ad8b7bfd1cbfc0991e7eb15f5f37706121`.
- Installed the complete package atomically on the same eight Linode workers, retaining rollback directories. Worker agents were not restarted during tool synchronization.
- Cloud worker creation now rebuilds the complete tool package before creating paid resources. A package-preparation failure returns an error before a provider creation request.
- New Linode/AWS cloud-init scripts validate the manifest and required executable paths before starting the worker service. Missing or corrupt files no longer produce a falsely ready worker.
- No user scan was replayed, API token used, or external search target queried during verification. The actual dork2 help branch exits before Apify client execution.

## Verification

- Eight workers: all 1373 file hashes passed; dork2 help ran twice per worker from an isolated shell directory using `./tools/dork2` (16 successful direct checks).
- Local manifest regressions passed: complete package accepted; enum-only, missing/corrupt dork2 and escaping paths rejected.
- Cloud integration regressions passed, including an incomplete-package fixture proving zero charged creation calls.
- Linux cloud-integration race checks passed in the deployed isolated release.
- Live bootstrap preview confirms manifest verification precedes worker service startup and includes runtime checksum verification.
- All eight Linode workers remain connected.

Actual CNC scheduler/worker execution, each task using offline dork2 help with output markers:

| Job | Mode | Result |
|---|---|---|
| `job_1792303751_2` | Broadcast, round 1 | 11/11 completed, exit 0; every Linode worker exercised |
| `job_1792303753_3` | Broadcast, round 2 | 11/11 completed, exit 0; every Linode worker exercised |
| `job_1792303756_4` | Spread with generated local input chunks | 11/11 completed, exit 0; input distribution and isolated workspace verified |

An earlier spread smoke job `job_1792303702_1` also completed 11/11. Spread scheduling can reuse a newly freed worker, so it does not alone prove every worker was exercised; the two broadcast rounds supply that coverage.

## Deployment and evidence

- Release: `/opt/cnc/linode-releases/7d3f61b52a2586ce`.
- Deployed source Git snapshot: `7672b8ae24a9c1a202dc4a7df70a9d0b7af41e91` (controller-local; not pushed).
- Deployment command: `038b04ec-729f-4e2e-b864-fcb74ae6c90d`.
- Package rebuild command: `29a14f81-41bb-4d82-a2ed-e67d13a2897e`.
- Actual CNC verification command: `c3f7996a-79b0-4362-9c88-2e0b347b4a59`.
- Controller verification record: `/opt/cnc/data/linode/spread-readiness-verification.json`.
- Local per-worker metadata: ignored `.linode/generic-tools-verification.json`.
- Screenshot: `/private/tmp/cnc-worker-command-verification.png`.

These tests establish installation, dependency loading and CNC command execution. They do not establish success of an external Apify search or change historical failed user jobs.
