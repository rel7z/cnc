# Plugin verification repair: production rollout and rescan

## Verified deployment

- Controller enum binary: `/opt/cnc/tools/cnc-enum`.
- Current SHA-256: `c6b28f73208d3994d40de4a986aed781a2cf6f4de12df2aa6bd03b420e3c4418`.
- Current release and rollback: `/opt/cnc/plugin-verification-releases/e3f38baf5897f0b3/`.
- Initial repaired binary SHA-256: `bab0fada09149b43d4f38785ecc4b717d544169bd5b3e40284965d3f92839ee0`, retained in `/opt/cnc/plugin-verification-releases/94443abd84698108/`.
- Successful deployment SSM command: `2fa4d164-fb88-43d6-88c5-62c9dc59f62c`.
- Controller service was not restarted.
- Production source baseline for the two implementation files matched the inspected local source before modification.
- Existing enum jobs were terminal, including `enum_20fcfe31e2d3a79de3472793` (cancelled).

## Behavior and verification

Plugin text exports now require a version that matches the job's rules whenever rules are present. With no rules, validated version discovery remains supported. Unknown or conflicting versions do not produce plugin text results. Plugin names output follows the same gate.

WordPress readmes must identify the requested slug in their heading, with two explicit display-name aliases. Redirects to a different plugin metadata path are rejected. Joomla manifests must contain a matching component identity as well as the expected extension type.

Public metadata is evidence of files served by a site; it cannot establish that a plugin is active or promise 100% accuracy. Missing metadata, unfamiliar display names, hidden files, and conflicting metadata can cause legitimate components to be omitted. Ambiguous targets are intentionally skipped.

Production build ran race-enabled version and pipeline tests. A separate executable-level verifier passed eight localhost scenarios: WordPress and Joomla accepted version 1.0.0 under `< 2.0.0`, rejected the 2.0.0 boundary and version 9.0.0, and rejected unrelated plugin/manifest identities. Text output remained one URL per routed line and agreed with structured match records.

The initial production staging attempt stopped at a failing old test fixture that lacked the newly required Joomla identity. The fixture now explicitly rejects missing identity and accepts a named manifest. No live binary replacement occurred during that failed staging attempt.

## Worker rollout and cleanup

- All 11 workers installed the pinned binary and restarted successfully, with per-node rollback copies. AWS SSM worker command: `9765df8c-0d86-4f4e-96fa-050547285c9e`; the eight Linode workers were updated over their existing SSH configuration.
- Capacity is now 4 tasks on the 2-vCPU AWS workers and 8 on the 4-vCPU Linode workers; the enum request budget stays bounded separately.
- Worker installation refuses active child tasks and retains binary/config/manifest rollback copies.
- Reset SSM command `8b1fc770-368b-430f-9c7e-f29d67494519` moved 80 local component result files to `/opt/cnc/plugin-result-reset/1792377632520757400` and trashed 80 matching Drive files. No plugin output files remained immediately after cleanup. WordPress and Joomla lists retained their hashes.
- Read-only cleanup audit (`916072fa-d5ab-49c3-9e22-1d22970407b4`) identified 80 component result files totaling 1,755,915 bytes. There were zero active jobs.
- Preserved `wordpress.txt`: 1,598,492 bytes, SHA-256 `7bf457b99dbcbb4122ec9adc362447ee2ce153f36b548967d1526e4001b6943b`.
- Preserved `joomla.txt`: 31,215 bytes, SHA-256 `2fc2c06f06a06b71416c1c43d00f07c8aa943b9d6255c486228d730d2012345b`.
- Cleanup script defaults to plan-only; applying it requires explicit `--apply`. It derives component filenames from existing enum artifacts, preserves CMS lists, moves local results into private rollback storage, and trashes matching files only in the configured Drive folder.
- The owner explicitly approved worker installation/restarts, increased capacity, local rollback/Drive Trash cleanup, and rescan in the scoped approval question after the initial automatic approval rejections.
- Rescan `enum_1187ac70423421c4ba73c4eb` launched successfully (SSM `8951c738-e8b8-4796-8e6a-c4f5f45ab9a1`): 57,471 unique targets from 58,394 WordPress lines and 1,079 Joomla lines. It uses the original 105 rules with canonical rules SHA-256 `44784b9af5d22a751e0c36a3738e92e4eb558884c960f39b641bd0773ff2424d`. The old job remains cancelled.
- Full scan finished: 11 tasks completed, zero failed, at `2026-10-19T05:17:35.755788395Z`. All workers report the new capacity.
- Read-only inspection of running processes on Linode worker 1 confirmed two actual enum processes use the pinned binary SHA. Four complete buffered records had known versions, matched rules, and authoritative metadata confidence; no target URLs were printed.
- Initial live output validation found 72 clean URL lines in seven files and zero invalid lines. No plugin names remained in restored-only uploader state.
- `burst-statistics.txt` was stable and matched Drive's MD5 metadata; its local SHA-256 was `edc970e69029208daae3ff74d0547767f587ecaaf5d56c9cc3b56a0a0a0eb128`. Other sampled files were still changing or pending upload. This is an initial synchronization check, not a claim that the full scan or all uploads have finished.
- Git audit reference for the controller release: `codex/plugin-verification-audit`. No GitHub push is claimed.

## Redirect receipt repair and positive-target verification

The full scan produced 47 clean text files (7,241 raw lines, including the plugin-name index). All 47 matched Drive checksums after the uploader's existing line deduplication. Only `plugins_name.txt` and `really-simple-ssl.txt` required deduplication, two duplicate lines each.

The full publication audit then found ten internal receipts still pending. A retry of an unchanged result returned HTTP 400. A read-only check of a rejected shard found three records whose final locator differed from the submitted input locator after redirects. The controller correctly rejected these unbound records. Its input binding and validation were not weakened.

The pipeline now retains `job.target` in the structured receipt while continuing to collect metadata and export clean URLs at the working canonical locator. A redirect fixture verifies both properties. Race tests and nine actual-executable localhost scenarios passed on production before activation.

The owner explicitly approved this additional repair, deployment without service restarts, backup/Drive Trash of the 47 current outputs, and verification of the 2,129 positive targets. Deployment command: `200922c9-6fd6-42ea-af74-40d95249760d`. All eleven workers installed the current pinned SHA without restart (AWS command `c7b044ad-9a46-4052-9162-ee51ff8b28bb`). Slots remain 4 on AWS and 8 on Linode.

The positive-target input is preserved at `/opt/cnc/plugin-positive-reverification/20261019/input.txt`, SHA-256 `8b1c928d2ca0b85195aa583665555c2646e371fa071630ff56fb26f7d2cb758d`. Previous outputs are retained at `/opt/cnc/plugin-result-reset/1792400553714386836`; the matching 47 Drive files were trashed. CMS file hashes were unchanged by this reset.

Follow-up job `enum_17887d7d45e06be7fa6f88aa` launched with the same 105 rules (command `e3208986-2340-49db-ab87-8e4d0b52f355`). It completed all 11 tasks with zero failures at `2026-10-19T09:11:53.093436999Z`, approximately 8 minutes 35 seconds after starting. Historical full-scan worker artifacts and receipts were not rewritten or falsely marked accepted.

## Final completion audit

- All 11 follow-up publications were accepted. The standalone audit reconstructed each record with `version.ValidateResult`, checked the immutable job/shard binding and all version clauses, and validated **5,347 records** against the 105-rule artifact.
- All **46 final text files** were reconciled with those records. Four input/canonical export aliases were explicitly reclassified under the artifact scope; none were silently ignored. There were **6,820 raw URL lines**, or **6,816 after per-file deduplication**, including the plugin-name index. Every exported line had matching validated evidence. Audit command: `4756fd9b-bd7d-4dc8-91f4-6c3f26ca1f43`.
- All **46 Drive files** matched their local outputs after the uploader's existing deduplication, with zero pending/mismatching files and zero malformed output lines. Three files required expected duplicate removal: `litespeed-cache.txt` (one line), `really-simple-ssl.txt` (one), and `plugins_name.txt` (two). Checksum command: `798444d9-63ad-426d-aabe-b6f558387ab3`.
- All **57,471 original unique CMS targets** remained in the local WordPress/Joomla lists. Both rollback sets were read and their stored SHA-256 hashes verified (80 original files and 47 first-rescan files). The original job remained cancelled. Retention command: `6e9815aa-d4e7-4c9a-b8da-ac0e6b73d779`.
- All eleven worker services were online and idle at the final job-status check, with capacities 4 on AWS and 8 on Linode.

These checks prove rule matching and transport integrity for the recorded public metadata; they do not establish plugin activation or universal 100% detection coverage. Missing, blocked, conflicting, or unidentified metadata is omitted. The historical pre-redirect-fix full-scan receipt files remain retained with their actual state, rather than being retroactively falsified.
