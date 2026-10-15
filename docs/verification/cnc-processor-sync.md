# Processor sync verification — 2026-10-16

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
