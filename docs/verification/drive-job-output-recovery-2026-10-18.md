# Drive synchronization and job output recovery

Verified on 18 October 2026 through backend APIs and metadata. No browser page
was opened, and `processor/tools/` was not accessed.

## Changes deployed

- Explicit manual Drive synchronization can publish retained restore-only files.
  Automatic scans retain the existing restore protection. Pending counts exclude
  unchanged restore-only files.
- Generic search jobs share a five-task provider budget. The search CLI queues
  provider capacity errors with bounded retries, streams dataset results while
  the Actor runs, and returns failure when a query fails. Each Actor has a
  900-second provider timeout.
- Controller task errors explain missing commands/interpreters, permissions,
  missing dependencies, provider authentication/capacity errors and termination.
  Worker failure log summaries do not copy raw stderr into the service log.
  Timeout/cancellation diagnostics are streamed before the result is sent.
- The dashboard log modal displays the task's failure reason. Exit 127 is not
  described as proof that the tool binary itself is absent.

Controller/UI release: `5de8855b6abe5a10`. Matching worker agent SHA256:
`f9407fba684a566494581d8f97e48fb8837f65c09d1879e12fd08a7717bcc617`.
All eleven agents were installed and verified, with rollback copies retained.

## Live verification

1. `doms.txt`, `rxzz.txt`, `xxz.txt`, and `xzxz.txt` all appeared in the configured
   Drive folder. Before the real rerun, each matched its local MD5 checksum and
   the pending count was zero.
2. A neutral verification file uploaded with a matching checksum. Moving only
   that file to Drive Trash removed it from the local merged directory and
   retained an identical copy beneath `.cnc-deleted`. No real result file was
   deleted in this test.
3. Job `job_1792307817_1` intentionally ran a nonexistent neutral command on all
   eleven workers. Every task returned exit 127, an explicit missing-command or
   interpreter explanation, and a streamed diagnostic.
4. The real search rerun used the exact saved 49-query input, SHA256
   `a32b9feca523fb24da439bfb8942e58a12e68d98aafa418704d91475ba49f516`.
   Initial rerun `job_1792307946_2` emitted fresh output. A downloaded Drive
   snapshot exactly matched the locally deduplicated result, its SHA256
   description and Google's MD5 metadata.

## Owner-requested case-only IP removal

The owner subsequently requested removal of standalone IPv4/IPv6 result lines
for this case. The original local and Drive content was backed up privately.
Exactly 19,984 IP-only lines were removed; 235 other lines were retained in the
cleanup snapshot. The same Drive file was updated and its bytes verified.

The preceding job and its own active provider runs were stopped. Job
`job_1792308432_3` continues the same 49-query input with five tasks and an inline
IP-only stdout filter. Its unfiltered intermediate output stays inside each
temporary worker workspace. `bash -o pipefail` preserves provider failures.
This filter is part of this job command; global tool defaults were not changed.

The filtered job has already emitted additional output. The job was still
running during verification; this report does not claim all 49 queries finished.
The canonical Drive file may be smaller than the local append-only file because
the uploader deduplicates lines.

After the scoped rerun, 80 additional result lines were observed. A separate
local snapshot contained 309 total lines and zero standalone IPv4/IPv6 lines.
The later downloaded Drive snapshot exactly matched the canonical local SHA256
`a8614f5f349f0f0f8b5d5022f9e1d6d9dc6e9dd70b3bf0112589e2944895d2a1`.
Google's MD5 and the uploader's SHA256 description also matched the downloaded
bytes. This is snapshot verification, not a claim that a running job's file size
will remain fixed.

## Tests and evidence

- Focused Go race tests: task failure classification/publication, real local
  missing-command/interpreter fixtures, shell workspace behavior, Drive manual
  synchronization and provider task capacity.
- Provider queue tests: capacity retry, bounded retries, streaming without
  duplicates and failed Actor propagation.
- Offline search CLI fixtures: real domain output and nonzero failure status.
- Case filter tests: standalone IPv4/IPv6 removal, preservation of other lines,
  and provider exit status propagation through the pipeline.
- Production release tests and UI build passed before activation. No active
  controller/processor work was restarted during backend deployment.

Private runtime evidence remains under `/opt/cnc/data/dork-recovery/`, plus
`/opt/cnc/data/task-diagnostics-verification.json` and
`/opt/cnc/data/drive-deletion-verification.json`. These records contain no
credential values in their public metadata; original private job snapshots and
result backups are kept outside Git with restricted permissions.

Drive output: https://drive.google.com/file/d/1HMYIk5dW8Y7fBytNg_yCPpgHtiqWaU8y/view
