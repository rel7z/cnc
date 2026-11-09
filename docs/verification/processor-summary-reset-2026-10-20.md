# Processor summary diagnosis and checkpoint reset

## Confirmed cause

The synced source and local binary matched SHA-256
`4682fb2e78f685f64c8ddb707c32a3c916dd5d612a0f272311e030c0d90c7fac`.
However the registered bundle contained the prior immutable editor snapshot,
whose binary hash was
`8f68e9f3faa12ebec16f8d6b207f9d72e3c7226346d00899b7af959e3ccae1fb`.

Mechanical file sync updates `tools/bf/rr`; it does not replace files inside
`ui-programs/active/...`, registered bundles or already pinned runs.
Production completed attempts using the old bundle had genuinely empty logs,
including their staged log files. They exited zero without a summary. This
was not evidence of log loss in the completion path.

## Evidence

- Local generic runner stdout upload test passed.
- The manifest/supervisor runtime test now asserts both stdout and stderr
  summary lines arrive in the uploaded log **before** completion acknowledgment.
  Bash and Node fixtures passed.
- Old and current binary were tested with empty input and invented fixture
  credentials under `nobody`, with a private network namespace, inaccessible
  production directories, read-only system, and bounded time/CPU/memory. Neither
  binary was reverse engineered. The old binary exited zero without stdout;
  the current binary exited zero and printed `processed = 0/0`.
- An opt-in Linux runner regression then executed the current binary through
  bundle install → supervisor → worker log upload → completion, using only an
  empty input and a loopback mock API in a private network namespace. It passed
  and verified the exact summary was retained. SSM command:
  `f5d4c6d1-e487-4b9d-bb92-bfb4be395d04`.
- A second isolated test supplied one loopback address on a closed port, still
  with invented fixture credentials and no external networking. It exited zero
  and printed `processed = 0/1`; no claim is made that the numerator represents
  attempted records. SSM command: `cafb7afc-63bc-40c4-82c2-fc9857f24880`.
- Its fixture test executable SHA-256 was
  `9d01fc0c75cfbf94e52205570753e0f6793be820759f555e081205643f6fdd5f`.

## Authorized activation and reset

The owner explicitly approved refreshing the production snapshot, moving only
never-executed queued work, and subsequently resetting the input checkpoint to
process from the start. The reset superseded moving old queued specs: those
queued runs were retained as interrupted history, while a new capture generation
created fresh queued runs pinned to the updated snapshot. No fake completion
status or fabricated program summary was written.

- Backup: `/opt/cnc-processor/summary-releases/1792442180395846480`.
  It contains config and SQLite backups, reset metadata and a rollout receipt.
- Activation used the existing editor API; no service or worker restart.
- Old bundle: `e9ebc7b5e7f553c94f3c2850b141affbae07a4cac5fb8e084f860aa9c1e5f182`.
- New bundle: `c96e1229257e5f3a53ebfab302965949be91764001af553fbd193c723704b450`.
- Verified the new bundle embeds exactly the synced binary hash.
- Superseded 5,364 never-executed queued runs. They retain the explanation
  “Superseded by owner-requested checkpoint reset”. Completed history and result
  files were preserved; nine existing active attempts were left to finish.
- Reset only `/root/merged/wordpress.txt`'s cursor. The source was about 4.48 MB.
  Processing was resumed for the owner's explicit replay request.
- Fresh capture generation: `423a08dd-6981-490c-b538-83ee542d0852`.
- Activation/reset SSM command: `79515f60-788d-4549-bc04-27b86e759078`.
- First read-only verification observed 22 running / 164 queued on the new
  version and a fresh cursor at byte 4,484,659.

## Completed production verification

At `1792444025078`, the final audit checked **17 completed attempts on the new
registered version**, including **16 real-input batches of 1,000 records each**
and one owner-approved empty fixture. Every completed attempt had a saved summary;
none was missing or invalid. Each exit code was zero, and the summary denominator
matched that batch's input record count. The numerator is reported verbatim;
this audit does not interpret it as a success count or independently validate
private program behavior.

Examples from real-input completed runs:

| Run | Worker | Saved summary |
| --- | --- | --- |
| `run-1792442183160088246-0` | `cnc-linode-worker-3` | `processed = 37/1000` |
| `run-1792442183869790906-0` | `cnc-linode-worker-1` | `processed = 41/1000` |
| `run-1792442183977613276-0` | `cnc-aws-worker-2` | `processed = 27/1000` |

The real-input summary `processed = 37/1000` was also fetched successfully through
the authenticated dashboard log endpoint used by the UI (HTTP 200). Its SHA-256,
`8086679033a45b019fd90f77fe721d81044af32c4f293cbf6272709fe3129d70`, matches the
saved controller log in the independent database/file audit.

- Empty production fixture: `run-summary-proof-1792443898722180398`.
  The owner specifically approved inserting this one labeled zero-target batch
  and run. It completed on `cnc-linode-worker-3`, exit zero, with a 16-byte log
  containing exactly `processed = 0/0\n`. No real target was added by that test.
- Fixture submission SSM command: `4c8c4e3a-3999-4682-b5cb-59ea233e1eb9`.
- Fixture verification SSM command: `7b1076e4-4362-4c73-9980-83b0c8b0b1cd`.
- Final all-completed audit SSM command: `797bb7f5-e297-4488-b91c-c7fb010b841d`.
- Full non-sensitive receipt on controller:
  `/opt/cnc-processor/summary-releases/1792442180395846480/final-summary-audit.json`.
- Verified active version embeds the current approved binary SHA-256.
- Processing remains enabled; the reset cursor is on generation
  `423a08dd-6981-490c-b538-83ee542d0852`, observed at byte 4,571,248.
- AWS CLI sign-in was refreshed through official Chrome Incognito login after
  the prior session expired. The existing CNC Manager restored the local tunnel
  at `http://localhost:8087/dashboard` without syncing tools or restarting cloud
  services.

## Limits and operational notes

- This proves the current binary's summary reaches completed production logs;
  it does not assert all queued/running work has finished or validate the
  private program's findings.
- Historical logs from the older binary remain empty; they were not fabricated
  or retroactively replaced with the new summary.
- Retired pre-reset queued runs remain interrupted history, with their reset
  explanation. Existing completed history and results were preserved.
- Future mechanical syncs still require explicitly refreshing the active managed
  snapshot before new captured work uses changed tool bytes. Existing pinned
  running/completed work retains its original executable version.
