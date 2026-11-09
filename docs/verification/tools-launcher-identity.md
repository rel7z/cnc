# AWS Tools Launcher identity repair — 2026-10-15

The reported error came from the controller's signed-dashboard identity check.
Live inspection confirmed CNC UI had a session secret but neither service had
`CNC_DASHBOARD_AUDIT_SECRET`. UI fallback signing therefore could not be verified
by the backend, which requires the dedicated audit key.

Installed a stable owner-only `/opt/cnc/dashboard-audit.env` and systemd drop-ins
loading that same file for `cnc-server` and `cnc-ui`. Login and signature checks
remain enforced. The AWS installer invokes the same idempotent configurator so
future deployments preserve the key. Tools Launcher now clears a stale error
after a successful registry Sync.

The owner explicitly approved this production authentication-wiring repair.
Git release: `704df87e00d72e2572c5fd2ba9012d5f4afc4aaa`, branch
`codex/launcher-identity-aws`. SSM deployment
`873c05bc-2d5d-4987-8cb4-67d0aeba7a5d` completed successfully. Rollback copies are
under `/opt/cnc/launcher-releases/704df87e00d72e2572c5fd2ba9012d5f4afc4aaa/rollback`.

Verification:

- Two configurator tests pass: private/stable key, matching service drop-ins,
  rejection of invalid existing keys and symlinks without overwriting them.
- Existing Go audit test passes for valid signatures and rejected actor/body
  tampering, missing signatures, forged signatures, and expired timestamps.
- Installer shell syntax and dashboard TypeScript checks pass; production UI
  build passes on AWS.
- Live effective audit keys match. Dashboard login succeeds. Authenticated
  launcher probe with deliberately invalid `threads: 0` reaches option validation
  (HTTP 400), demonstrating identity verification passed without executing a job.
  The equivalent unsigned backend request is rejected (HTTP 401).
- Authenticated registry loads all seven tools. Browser Sync completes with no
  identity error. Three workers remain online and Google Drive stays connected.
- No tool was launched or executed, and no protected processor tool was accessed.

Open `http://localhost:8081/dashboard/tools`; refresh an already-open old page
once to load the deployed dashboard assets.
