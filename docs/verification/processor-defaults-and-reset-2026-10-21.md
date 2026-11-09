# Processor defaults, reset and deployment verification

## Cause

The new Linode had an enabled processor with an empty program registry and empty
routes. The old AWS registry/database was unavailable during migration. A tool
file sync alone does not create managed program snapshots or register routes.
The dashboard also had no checkpoint reset control.

## Deployed state

- Controller: `172.237.87.96`; seven workers online.
- BF and Divi source sync: seven files, opaque copies only, hashes verified.
- Source sync rollback: `/opt/cnc-processor/sync-rollback/tools-ul9s5wwt/previous`.
- Registered routes: `wordpress.txt → bf`, `divi.txt → divi`.
- Two saved default templates, two managed setups and three executable bundles.
- Six copied snapshot files and all three bundle hashes verified.
- Restarted the processor and verified both setups, both templates and paused
  state persisted. No private program ran; all processor run counts remain zero.
- Drive remains authorized with zero pending uploads.
- `wordpress.txt` exists in merged (4,018,743 bytes). `divi.txt` is absent:
  Divi will wait for that input even after processing is enabled. No placeholder
  target file was invented and no private program was run to manufacture input.

The owner separately approved source sync and then registration/snapshot copying,
with processing left paused. Private program contents were not analyzed or tested.

## Persistent changes

Default editor templates are embedded in the processor binary and seeded once
into each new database. Restarting does not overwrite owner edits or restore
templates the owner intentionally deleted. A test keeps the public example
templates consistent with the embedded defaults. Supporting files are declared
by filename; their contents are not embedded in templates.

`infra/processor_defaults.py` is an explicit registration step after an approved
file sync. It pauses first, requires no queued/running jobs, preserves existing
setups and never enables processing. Installed controller command:

```sh
python3 /opt/cnc-controller/maintenance/processor_defaults.py --apply --pause
```

Normal file sync remains separate from activation. Do not replace an existing
processor config/database with empty deployment defaults. The default templates
are available automatically on a fresh database; the explicit registration step
builds snapshots after the corresponding files have been supplied.

The Linode installer now requires the registration helper in its checksummed
core artifact manifest, installs it under `maintenance/`, and refuses to report
success unless the running processor actually exposes both default templates.
Include `infra/processor_defaults.py` as `processor_defaults.py` when preparing
the next core release. This prevents an old binary or incomplete release from
silently producing another deployment with no built-in templates.

`Reset checkpoints` appears beside the processing button on the dashboard and
processor page. It requires a confirmation and a paused, idle queue. The reset
serializes with capture/scheduling, backs up cursors transactionally and clears
only checkpoints. Jobs, results and templates remain intact. Existing input is
read again only when the owner enables processing.

Health now reports configured program/route counts and explains an empty
configuration instead of displaying a misleading running badge. The reset API
requires authentication, same-origin browser writes and explicit confirmation.

The UI stop timeout is now 15 seconds with `KillMode=mixed`, in the live systemd
drop-in and future Linode installer. This prevents realtime connections from
holding a normal restart open indefinitely.

## Validation

- Store, watcher and API test suites passed; new tests cover reset rollback,
  history preservation, default persistence/deletion and no automatic activation.
- Distributed suite passed. Two cluster fixture tests failed during the initial
  concurrent build/test run; both passed when rerun alone.
- Five UI control/editor tests passed locally and on Linux; TypeScript and the
  Linux production UI build passed.
- Named-folder opaque sync tests verify rollback, hash integrity and exclusion
  of unapproved sibling directories.
- Live unconfirmed reset returned HTTP 400; unauthenticated reset returned 401.
- Browser verification shows Reset checkpoints, both active routes, both default
  template options, paused state and the LIVE connection indicator.

Core rollback: `/opt/cnc-controller/processor-controls-rollback/1792556235916288930`.
Remote receipts: `/opt/cnc-controller/processor-controls-receipt.json` and
`/opt/cnc-controller/processor-defaults-verification.json`.
Local final receipt: `.linode/processor-controls-verification.json`.
