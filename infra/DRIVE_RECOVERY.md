# Automatic Google Drive recovery

CNC Manager keeps the callback at `http://localhost:8080/api/config/gdrive/callback` even when the dashboard port changes.

The Mac's private `~/Library/Application Support/CNC Manager/drive-oauth-profile.json` holds the OAuth client configuration and, after a successful connection, a renewable Google session. It is written atomically with mode 0600. Keep this file out of Git and retain it when replacing a server.

Its optional `preferred_account` email is used as Google's login hint for every connection through the fixed callback, across controller and dashboard-port changes. This selects the intended account but never bypasses Google login or consent.

With `drive_oauth_auto_restore: true`, the installed Mac supervisor checks the selected controller every minute. A fresh controller receives the saved client configuration and enabled preference. If a saved renewable session exists, it is restored through the existing host-verified controller SSH connection before applying the configuration. No HTTP endpoint exports tokens. Linode and DigitalOcean use this session recovery; AWS currently restores client settings only.

A controller that already has a different OAuth client is rejected. A configured controller that was deliberately disconnected is not automatically reconnected. Disabling Drive on an existing controller is preserved. Selected-server login and SSH configuration remain required.

If no renewable session was backed up, or Google revokes authorization, complete Google consent again. CNC Manager captures the session on its next successful check. It cannot recover a session from an inaccessible old server or bypass Google consent.

For an External Google app in Testing, Drive refresh tokens expire after seven days. A durable production setup must address the app's Google publishing/verification status as well as CNC's session persistence. Adding a test user resolves the tester access block but does not remove that expiry. See [Google OAuth token expiration](https://developers.google.com/identity/protocols/oauth2#expiration).

For a new deployment, install/start CNC Manager with its existing private profile and the new verified controller SSH configuration. Do not write a deployment connection with `drive_oauth_auto_restore: false` when automatic recovery is desired. The server installer intentionally starts without embedded secrets; Manager restores them after the connection is selected.

Verification: `python3 -m unittest infra/test_drive_oauth.py -v`. These tests use localhost fixtures and dummy credentials; they cover callback port changes, session transfer ordering, replay-safe file restoration, private file modes, mismatched clients, and intentional disconnect behavior.
