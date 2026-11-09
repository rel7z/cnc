# Production enum verification — 2026-10-15

The requested production behavior is deployed on the existing AWS controller
and three workers. Controller release: `7f1dd7d9b08654d7c16abedeefc75bc4caf71354`.
Final scanner refinement: `c9b3270fac9002ac7b7e49d993a6f50d0bae16d7`.

## Distribution

New jobs use one balanced part per requested worker. The real saved input was
split and hashed during production build verification:

| Part | Lines | Bytes |
| --- | ---: | ---: |
| 1 | 1,101,285 | 20,624,081 |
| 2 | 1,101,285 | 22,772,459 |
| 3 | 1,101,284 | 22,259,706 |

Total: 3,303,854 lines. Concatenated part bytes match the normalized input
exactly, preserving order and every target. Parts are no longer split at
1,000 lines, 1 MiB, or 16 MiB. The existing 256 MiB worker transfer limit
remains; oversized input produces an explicit error rather than extra parts.

## Output contract

- Named plugin TXT files contain clean site URLs only.
- `plugins_name.txt` contains clean plugin directory URLs only.
- `admin.txt` contains ordinary login forms; cPanel findings are excluded.
- `cpanel.txt` contains positively identified cPanel login pages.
- JSONL remains private internal evidence and publication receipts. It is not
  announced in text exports, copied to the merged directory, or uploaded by the
  Drive watcher. Nine legacy JSONL files were archived beneath the controller's
  private data directory. Existing historical Drive files were not deleted.
- Known old enum metadata suffixes are normalized when merging Drive text
  history, preventing appended fields from returning to updated TXT results.

Login detection parses actual form elements. It rejects password-looking
script text, articles without login forms, password-reset/setup and registration
forms, blocked responses, and missing-page lookalikes. cPanel requires specific
branding and form evidence, including localized login titles. Merely using a
particular port does not identify cPanel.

## Evidence

Local and production-host enum regression tests passed, including race checks,
classification, login routing, cancellation, immutable transfer/retry behavior,
private JSONL publication, URL-only Drive merging, and input conservation.
The Drive watcher test proves legacy enum JSONL triggers no upload or deletion
requests. Controlled localhost fixtures on the controller and each worker verify
clean plugin results, ordinary login detection, separate cPanel output, and no
JSONL export. Installed hashes and distribution-archive hashes are checked.

The final controlled benchmark preserved request counts and URL findings:

| Workload | Previous | Updated | Requests |
| --- | ---: | ---: | ---: |
| 512 CMS targets | 0.709 s | 0.711 s | 512 |
| 64 WordPress core/plugin targets | 0.698 s | 0.702 s | 512 |

This retains the previously deployed throughput improvement, with roughly 1%
timing variation. It is a localhost comparison, not a guarantee of public-site
latency or a completed multi-million-target scan. Worker-wide admission remains
64 requests, with per-origin limits, rate-limit cooldowns, TLS verification, and
scope validation intact.

Production activation occurred only after authoritative checks showed no active
jobs. No scan was cancelled, restarted, or launched by this deployment. The
controller was restarted once; worker agents remained running. The final
scanner-only refinement restarted no services. Rollback copies remain on every
node. The protected `processor/tools/` subtree was not accessed.

Machine-readable evidence:

- [Controller activation](enum-three-workers-initial.json)
- [Final scanner rollout](enum-three-workers.json)
- [Installed production verification](enum-three-workers-final.json)
