# CMS scanner verification

The built-in CMS scanner was checked and improved on 2026-10-15. This work
does not access or modify `processor/tools/`.

## Fixes

- Validate gateway settings and output destinations before truncating results.
  Reject input/output aliases, hardlinks between result files, symlinks, and
  nonregular destinations. Existing input and results survive validation errors.
- Stop processing queued targets after cancellation. Unscanned targets are no
  longer emitted as network errors.
- Keep categorized output and worker `[FILE:...]` records, shared request budget,
  TLS verification, and partial-result exit status intact.

## Local checks

All passed:

- `go test -race ./...` in `cnc-backend/cms-scan`.
- `go test -race ./detection` in `cnc-backend/enum`.
- `go test . -run 'TestCMS|TestShellWorkspaceFileRecords' -count=1` in `cnc-api`
  exercises the CMS command test. The separately run exact test
  `TestShellFileRecordsWithConfiguredOutputDoNotReplay` covers worker file records.
- `infra/aws/verify-cms-scanner.py` against a locally built native binary.

Regression cases include eight CMS fingerprints, unknown content, blocked and
error responses, malformed and duplicate inputs, startup preservation, and
cancellation with queued work. The verifier uses only disposable HTTP fixtures
bound to `127.0.0.1` and temporary result directories.

## AWS rollout

Git source snapshot: `1b528384ecf13694327c6a8fa5c495401e402844`, branch
`codex/cms-scanner-aws`. The selected source and Linux binary are transferred in
an encrypted private S3 archive and installed using SSM. Installed paths are
`/opt/cnc/tools/cms-scan` on the controller and three existing workers.

Each node verifies the candidate before replacing its installed binary, then
verifies the installed copy. The expected fixture totals are 8 detected,
1 unknown, 1 blocked, and 2 errors; exit code 2 deliberately reports partial
results. The input/output collision check must preserve input bytes.

Replacement is atomic, with old binaries retained at
`/opt/cnc/cms-releases/1b528384ecf13694327c6a8fa5c495401e402844/rollback`.
Already running processes retain their original executable. No service restart
or job cancellation is performed. The controller's worker distribution archive
is updated by replacing only its `tools/cms-scan` member; a backup is retained.
The selected CMS source and shared build dependencies are also retained in Git
and copied to the controller source tree for subsequent builds.

Machine-readable per-node outcomes and SSM command IDs are in
[cms-scanner.json](cms-scanner.json). An initial attempt stopped before replacement
because a worker could not read the controller jobs API. Preflight was moved to
the controller without changing access controls. Its active job was preserved.

This verifies classification and worker output compatibility against controlled
fixtures. It does not claim live public-site accuracy, launch a production CNC
scan, or add fixture results to merged files or Google Drive.
