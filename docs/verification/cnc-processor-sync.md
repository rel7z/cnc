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
