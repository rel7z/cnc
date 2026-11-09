# Stable Drive authorization — 21 October 2026

## Cause and correction

Both dashboard Connect and Drive recovery replaced the saved OAuth callback with
`window.location.origin`. The currently selected tunnel used port 8111; the old
saved client settings also carried a stale `127.0.0.1:3400` callback. Google
requires the redirect to match the client's registration exactly, including the
port. See [Google's web-server OAuth documentation](https://developers.google.com/identity/protocols/oauth2/web-server#authorization-errors-redirect-uri-mismatch).

The existing client accepted
`http://localhost:8080/api/config/gdrive/callback` and reached Google's sign-in
page. Its stale 3400 callback produced `redirect_uri_mismatch` during the same
read-only authorization-endpoint check. No Google Cloud registration change was
needed for the accepted fixed address.

## Installed behavior

- `infra/drive_oauth.py` provides a loopback-only callback on IPv4 and IPv6 port
  8080. It reads the manager's currently selected dashboard port for each request;
  changing a controller or dashboard port does not change Google's callback.
- Only the one-use `/drive-connect` ticket route, Drive callback, and minimal
  health endpoint are available. Callback forwarding retains the HttpOnly Drive
  state cookie and existing controller single-use state validation. It does not
  forward dashboard sessions or unrelated localhost cookies. Other routes and
  arbitrary external redirects are rejected. Callback request URLs are not
  logged. Health CORS is restricted to the selected localhost dashboard origin.
- A conflicting callback listener produces a local setup error; the manager
  does not replace the registered callback with an unregistered random port.
- The owner-only profile at
  `~/Library/Application Support/CNC Manager/drive-oauth-profile.json` stores the
  existing client settings with mode 0600. The manager reapplies the stable
  callback and fills missing client settings when connecting to a fresh
  controller. Automatic setup preserves the uploader's enabled/disabled state
  and refuses to overwrite a controller configured for a different client.
- Connect and recovery preserve the saved callback and obtain a one-use bridge
  ticket. They verify the local callback helper before opening Google.
- The Desktop `CNC_Manager.py` was an independent old copy. It is now a thin
  launcher for the maintained repository implementation, preventing another
  stale desktop copy. Its previous source was backed up privately.
- Menu **8 — Connect Google Drive** and CLI action `drive` prepare the connection
  and open Google without requiring manual callback edits. Source for the desktop
  launcher is retained in `infra/desktop_launcher.py`.

## Verification

- Python callback tests passed with real temporary HTTP servers: state-cookie
  preservation, replay rejection, switching upstream ports/controllers, route,
  host and CORS restrictions, idempotent profile application, and fresh-controller
  setup without implicitly enabling uploads.
- Eleven combined callback/doctor tests and three relevant manager tests passed.
  Nine frontend OAuth tests passed locally and on Linux. Production Next.js build
  and TypeScript checks passed before dashboard replacement.
- The live bridge returned 307 to `accounts.google.com`, with the fixed 8080
  `redirect_uri` and HttpOnly state cookie. A live callback with invalid state
  returned 400 before code exchange.
- The repaired Python manager opened a fresh authorization. The live Drive API
  status subsequently reported authorized=true for the owner's requested Google
  account; the dashboard visibly displayed that connected account.
- Final audit confirmed the fixed callback, seven online workers, private local
  profile permissions, and the processor still paused. No private tool subtree
  was accessed and no processor job was run for this work.

Only the dashboard service was replaced. Its old process required forced close
after its pending graceful stop did not finish; controller, worker and processor
services were not restarted. UI rollback is available at
`/opt/cnc-controller/ui-before-stable-drive-1792552733145805738`, with receipt
`/opt/cnc-controller/stable-drive-ui-receipt.json`. Local verification metadata is
in `.linode/stable-drive-verification.json`; no tokens or client secrets are in
this report or the Git audit.

For future deployments, retain the Mac manager profile and existing Google
client. Google may still require account consent on a fresh server, or after a
grant is revoked; the registered callback does not need changing per server.
