# Linode controller migration — 21 October 2026

## Verified deployment

- Controller: existing Linode `108447688`, public IP `172.237.87.96`, private
  IP `192.168.136.27`. Its previous `cnc-worker` service is stopped and disabled.
- Seven existing workers retain IDs `cnc-linode-worker-2` through
  `cnc-linode-worker-8`; all registered online with the new controller and
  reported zero active load after recovery settled.
- No VPS was created, deleted, or resized. Linode cloud labels were not changed.
- Controller core: `/opt/cnc-controller`; processor core: `/opt/cnc-processor`.
  Existing worker data and installed programs were not deleted or overwritten.
- Services `cnc-server`, `cnc-ui`, and `cnc-processor` are enabled and healthy.
  HTTP/TCP bind only to the private IP on 8080/9090; UI and processor API bind
  only to loopback on 3000/9191.
- Current private dashboard: `http://localhost:8111/dashboard`, through the
  existing Mac CNC Manager's trusted SSH connection. Its config selects Linode,
  not AWS/SSM. The active deployment record takes priority over stale AWS state.

## Authentication and verification

The owner explicitly approved transferring saved dashboard credentials and
Google OAuth client configuration to this controller via SSH. Passwords,
session cookies, OAuth secrets, and browser sign-in links were not printed.
The configured login passed an authenticated HTTP 200 dashboard check;
unauthenticated dashboard access redirected to login (307). The authenticated
realtime endpoint delivered a valid snapshot with seven workers.

Linux UI install, production compilation, TypeScript checking, and generation
of all 21 static pages passed. Controller and processor Linux binaries built
from the unrestricted local core source. No `processor/tools/` content was
included, read, copied, indexed, imported, or executed by the assistant.

Local verification passed eight doctor tests plus targeted manager tests for
SSH forwarding, active Linode discovery, preserving healthy ports, and IPv4/
IPv6 port conflicts. A temporary real socket check verified that TIME_WAIT does
not falsely force a new port. The reloaded native manager restored the SSH
tunnel on the same port, and its authenticated realtime check passed again.

The historical AWS-named doctor and shortcut now follow the configured Linode
SSH manager. The recurring Codex monitor prompt was retargeted to Linode while
preserving its existing **PAUSED** state and five-minute schedule. No AWS login
or AWS API access is needed for the new dashboard.

## Explicit limitations

- AWS was unreachable both directly and through the existing private tunnel.
  Its job history, database, merged input, output, custom templates, and Drive
  refresh token have **not** been recovered. This controller has a fresh data
  directory; old source data was not erased or silently represented as restored.
- Processor health is `ok` and dispatch is **paused**, with zero queued/running
  jobs and empty program routes. The spread/required-summary policy is retained
  for future `wordpress.txt` routing; no private program was activated or tested.
  Restoring private tool files/manifests or activating routes requires the
  owner's task-specific permission under `AGENTS.md`.
- Google OAuth client settings were restored; Drive is disabled until its user
  authorization is reconnected. No prior Drive session is claimed as restored.
- The saved Linode API token returned HTTP 401. Existing-node SSH operation does
  not depend on that token, but cloud API management needs a valid token later.

## Evidence and rollback locations

- New controller receipt: `/opt/cnc-controller/migration-receipt.json`.
- Private staging and build logs:
  `/opt/cnc-migration-staging/20261021-core/`.
- Each worker retains its previous connection configuration under
  `/opt/cnc/migration-backups/<timestamp>/`.
- Mac connection backup:
  `~/Library/Application Support/CNC Manager/config-before-linode-20261021.json`.
- Local deployment mapping: `.linode/active-deployment.json`.
- Local authentication/worker verification:
  `.linode/dashboard-migration-verification.json`.

The temporary first tunnel port was 8106. Transient network failures caused
earlier reconnect attempts to move to 8111; the socket reuse repair prevents
recently closed ports from causing that drift. The manager still selects a new
port when a different application actually owns the configured port.
