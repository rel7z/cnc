# Drive connection recovery verification

The connection ticket is single-use, stored in UI process memory, and expires after ten minutes. Restarting the UI also removes tickets. An old ticket therefore cannot be a permanent connection entrypoint.

Implemented recovery redirects for missing, expired, reused, and invalid bridge links, with no-store and no-referrer headers. The recovery screen checks the existing connection, offers a fresh connection and Sync now, and reports unavailable-controller errors. Fresh connection saves the current browser callback before starting OAuth. Sign-in now returns to recovery or fresh authorization through an explicit local destination allowlist. Controller authorization requests time out after ten seconds.

Verified locally:

- Seven focused tests pass: accepted bridge, rejected destination/state/origin swaps, single-use and expiry, recovery redirects, cookie behavior, and safe sign-in return destinations.
- Next.js production build passes; TypeScript check passes.
- Browser: invalid ticket redirects to recovery; signed-out user sees Sign in; sign-in returns to recovery after restart; existing authorization enables Sync now; one click produces one request to the local mock controller and shows queued feedback.
- Browser test used a localhost-only mock controller, not Google Drive. It proves request routing and UI behavior, not a real file upload or a Google consent round trip.

The original Linode deployment was unreachable. The owner subsequently confirmed AWS as the current deployment. The targeted dashboard release is now deployed to the existing AWS controller through Git, encrypted S3, and SSM; see the AWS verification below.

This change cannot guarantee Google consent never expires or network connections never fail. It provides recovery from unusable ticket links while retaining OAuth state and dashboard authentication.


## AWS production verification — 2026-10-15

- Source-only Git release: `b929ddf7ffa34085b42e2ebb6ef6ae7901544eac`, branch `codex/drive-recovery-aws`.
- Deployment command: `ab802f48-fd1c-4ec9-8d14-83b6ab85b1f7`; SSM reported Success.
- Built and ran the seven focused tests on the AWS controller before switching the dashboard to its isolated standalone build. Rollback files retained under the release directory.
- Recovery HTTP 200; expired ticket HTTP 303 to `/drive-recover`; unauthenticated API HTTP 401; callback without state HTTP 400. Three workers remained online. Backend and worker services were not restarted; no protected tools were accessed.
- Existing automatic SSM tunnel forwards `http://localhost:8081` to the AWS dashboard; local HTTP recovery check returned 200.
- Real browser: recovery loads, reads the real disconnected authorization state, disables Sync until connected, and Connect reaches Google's sign-in page for the saved OAuth app with the correct localhost callback. The dashboard also loads without the earlier chunk error and shows LIVE worker updates after hydration.
- Google sign-in and consent remain pending with the owner. Real upload/download verification is not complete. No mock result is represented as a real Drive upload.
- Canonical entrypoint: `http://localhost:8081/dashboard?connectDrive=1`. Do not bookmark single-use ticket links or the retired Linode port 3400.

Google documents that external OAuth apps in Testing receive refresh tokens that expire after seven days for Drive scopes. The saved app's publishing status has not been verified. Application code cannot override that Google-side expiry policy: https://developers.google.com/identity/protocols/oauth2/#expiration
