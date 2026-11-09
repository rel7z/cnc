# Processor sync verification — 2026-10-16

## Registration resolved; processing paused

After the owner approved configuration changes and default registration,
`bf`'s malformed JSON, incorrect executable path, and missing binary runtime
were corrected. The source config and controller copy now agree. Local default
routes are `wordpress.txt` to `bf` and `divi.txt` to `divi`.

SSM command `93ea864d-066d-4211-9ed1-45f03d9addd8` registered both setups through
the actual processor editor API and saved canonical templates named `bf` and
`divi`. Configuration and SQLite backups were retained. The controller also
stores the matching definitions in `/opt/cnc-processor/default-setups.json`.

The dashboard was reloaded and verified visually: **Active setups (2)** shows
both tools, the saved-template selector contains `bf` and `divi`, and the
processor badge reads **Paused**. Configuration-only validators passed before
registration. CNC internally validated and bundled the approved files; the
assistant did not analyze or execute the private programs.

Processing remains paused at the owner's request, with zero queued or running
processor jobs. Registration and saved templates are persisted; they are not
unsaved browser form values. No existing merged input was executed by this work.

## Earlier finding: registration was incomplete after file sync

File synchronization is verified; making the tools available in the dashboard
is **not complete**. A successful transfer receipt does not prove registration.

Follow-up live checks established:

- SSM `ec227e6d-8dbe-441a-90aa-9073b2ee8eee` confirmed the three expected `bf`
  files exist as regular files at `/opt/cnc-processor/tools/bf/`.
- SSM `b9135b71-d991-4d2a-9255-64ce7a5476fc` reported the processor active but
  its deployed `programs` and `routes` empty.
- SSM `8d5ab938-9a45-44b4-bf49-2ff8542fe129` queried the actual setup API used
  by the dashboard: HTTP 200, `definitions: []`, `external: []`.

At that point the dashboard had no `bf` setup to display. The owner subsequently
approved reading the two configuration files, preparing corrected drafts, and
then applying configuration/default registration while keeping processing
paused. Those later results are recorded above. Sync permission alone had not
authorized registration or execution.

The general AWS source release deliberately excludes private processor tools.
Its processor installer retains existing configuration and initializes new
installations with empty routes/programs. Permission for a private folder does
not change that installer behavior or automatically register a program.

Read-only AWS SSM audit `8dd2b86f-e8ab-4717-b6c9-43cf502646a8` confirmed the
processor service was active, `/opt/cnc-processor/tools/divi/` was absent, and
the deployed routes and programs were both empty.

After refreshing the existing AWS CLI credentials through the owner's incognito
session, the Desktop manager's `sync` action completed SSM command
`cc5e123f-6d51-4f32-965e-1abc06fd17e5`: three files copied, with installed SHA-256
hashes matching the source manifest. Content was transferred as opaque bytes;
no private code was analyzed, imported, edited, tested, or executed. No routes
were activated and no services were restarted.

Independent post-sync SSM audit `be6d3667-3399-43bd-8d42-2437d3b19c98` confirmed
all three destination files and their expected byte sizes, processor service
active, and routes/programs still empty.

The local private `last-sync.json` records the per-file hashes and remote
rollback location. Repository, Desktop, and installed manager copies match.
The manager, tests, and usage documentation are published in GitHub PR #1;
private tool files, receipts, cloud credentials, and runtime configuration are
excluded from that PR.

`python3 -m unittest infra/test_cnc_manager.py`: 22 tests passed, including full
AWS/Linode transfer simulations, opaque copy/installed hashes, replaced-file
rollback, preservation of unrelated files, corruption and symlink rejection,
expired/cross-account credentials, and resuming an existing SSM command.

## Complete tools tree and automatic sync

The owner subsequently expanded sync permission to all files in
`processor/tools/`, including both `bf` and `divi`. The manager now discovers
the complete tree recursively, without maintaining a list of tool folder names.
It preserves supporting files, nested paths, and owner execute permissions.
Source access remains limited to filenames, metadata, and opaque copying.

Explicit sync command `27983f4c-a237-414e-8e7a-20261fdf0bc6` successfully copied
ten files to `/opt/cnc-processor/tools/`, including both tools and root files,
with matching installed hashes. The updated Desktop launcher's normal
`--no-browser connect` path then automatically performed another complete sync
(`854ea5fd-7e36-46e4-8a19-66b8d860075c`) and opened the existing dashboard tunnel.
That second sync preserved the replaced files in its private rollback directory.

Normal dashboard opening and successful first-time deployment now call full
tool sync automatically. Explicit `sync` remains available. Diagnostic `--once`
and `--no-sync` skip it; the background tunnel supervisor does not repeat file
transfers whenever the tunnel reconnects. Routes remain unchanged and no tool
is run by these operations.

The expanded suite has 26 passing tests, including discovery of both tool
folders plus a newly introduced nested folder, executable permission
preservation, automatic dashboard sync ordering, sync after each provider's
deployment, diagnostic bypass, and safe completion of a pending command from
the older single-folder manager before starting a complete sync.
