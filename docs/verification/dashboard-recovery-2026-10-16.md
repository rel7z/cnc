# Private dashboard recovery — 2026-10-16

## Owner requirements and decisions

Fix recurring localhost:8081 connection failures and automate diagnosis/login. The owner explicitly approved a one-minute local tunnel check, a five-minute browser check, use of existing saved dashboard credentials, and opening official AWS login when expired. AWS approval/MFA remains a human step. The owner chose to keep access private; no public listener, domain, firewall change, tool execution, or processor resume was requested or performed.

## Root cause and deployed fix

The realtime bootstrap contained 19,830 historical enum tasks. SSM `dc098cb1-abee-4c64-a8ad-88bac5d68455` measured a 21,004,116-byte initial snapshot. This blocked browsers from receiving the initial event promptly through Session Manager, producing the DISCONNECTED banner even when ordinary HTTP requests worked.

- Go realtime snapshots now contain the 100 newest tasks, with stable ordering.
- The task list API accepts a validated optional limit, preserving the complete default response for existing callers.
- UI bootstrap requests 100 tasks; job details fetch their dedicated job-task endpoint, including terminal jobs. Reconnection retains already-loaded detail records.
- All 19,830 historical records remain stored and retrievable.

The Linux release `/opt/cnc/dashboard-releases/86425ca6d43057b3` passed focused broker/dashboard race tests, a complete production Next.js build, and TypeScript checks. Source SHA-256: `86425ca6d43057b3723ded63e0385db4683a6689f1a34cb89fea2ffccfb9423f`. Running controller SHA-256: `8b47fbb162f71bb50689c96dcac2321c11af619493926966727791bc9170a135`.

The controller was activated via SSM `24225526-dae2-42ac-8622-008ab46f4be3`. Existing launcher/Drive systemd overrides initially took precedence over the new UI drop-in. Final activation uses `zzzzz-dashboard-recovery.conf` and asserts the effective WorkingDirectory before restarting. A 10-second graceful-stop limit prevents persistent old SSE sessions from indefinitely delaying UI restarts. Final UI activation: SSM `177ecfde-3628-4510-9dc9-eda247dc732f`.

SSM `8a908a9d-8eeb-46f2-9051-4895606da405` independently verified running controller hash, actual UI release, all three controller services active, all 19,830 tasks retained, 100 bootstrap tasks, 36 processor jobs queued, zero running, disabled processing, unchanged applied setup revision, and bf/divi template revisions 2.

SSM `c7da6b0e-a650-49f4-81aa-f491a7c4d550` measured a 150,002-byte snapshot with three workers, eight jobs, and 100 tasks. The local authenticated probe reported connected; the in-app browser visibly showed LIVE, PAUSED, and 36 queued. The selected historical three-part cancelled job still displayed all its task rows. Google Drive's backend configuration was not modified.

## Local recovery and login

`infra/aws/dashboard-doctor.py` uses only the standard library and existing AWS CLI/profile. It never deploys, restarts remote services, starts jobs, resumes processing, or accesses private tool code.

- Installed LaunchAgent: `com.cnc.aws-dashboard-tunnel`, every 60 seconds, RunAtLoad.
- Private installed copy: `~/Library/Application Support/CNC Dashboard Tunnel/dashboard-doctor.py`.
- One-click launcher: `~/Applications/CNC Dashboard.app`; it invokes the private installed helper and normal saved-credential login.
- `infra/aws/open-dashboard.sh --check` diagnoses HTTP, STS authentication, and the authenticated initial realtime snapshot with a 512 KiB read bound.
- `--repair` repairs only an owned Session Manager listener on port 8081; an unrelated listener is left untouched. A second health probe avoids tearing down a tunnel after a transient stall.
- Expired credentials open the official AWS browser login, with a 15-minute cooldown. Network failures and stale/clock-skewed signature requests are classified separately.
- Dashboard sign-in uses the existing server credential check. A single-use, 60-second loopback handoff transfers the existing HttpOnly cookie to the browser. Credentials/cookies are absent from URLs, arguments, state files, and logs; no authentication bypass or extra server access was added.
- Shared file locking prevents competing repairs. A user opening the app waits for an in-progress check instead of silently skipping the sign-in.

Seven focused Python tests pass, covering auth classification, transient/healthy behavior, network versus login failures, login cooldown, unknown-port protection, bounded realtime frames, and single-use browser handoff. A real local tunnel interruption recovered in 12.8 seconds through the installed watchdog. A real saved-credential handoff navigated the existing browser to the signed-in dashboard.

The tested source and installed copy hashes matched, the app executable exists, and the one-minute LaunchAgent configuration was inspected.

## Recurring automation and approval

The first Codex heartbeat request was rejected by automatic review for insufficient explicit scheduling/credential-use consent. The local recurring watchdog was disabled while permission was pending. After the owner expressly approved the exact recurring workflow, it was reenabled and the Codex heartbeat was created successfully:

- Automation ID: `cnc-dashboard-diagnose-and-reconnect`
- Name: `CNC dashboard: diagnose and reconnect`
- Status on creation: ACTIVE
- Interval: five minutes

It repairs the tunnel, checks the existing dashboard tab, signs in using a fresh single-use local link when needed, and notifies only for meaningful recovery, persistent failure, or required human action. It is prohibited from accessing processor/tools, changing server auth/firewall, deploying, restarting cloud services, running jobs, or resuming the processor.

## Current authentication gate

During subsequent launcher testing the local AWS session expired. The watchdog correctly reported login_required and opened the official AWS sign-in page in Chrome. A later STS call and SSM audit confirmed authentication is expired, rather than proving a service failure. The owner has been asked to finish the official AWS login/approval/MFA. Current reconnection remains unverified until that human step completes. Do not claim the dashboard is presently reachable based only on the earlier LIVE evidence.

## Incognito authentication correction and recovery

The owner clarified that their AWS console session is in Chrome Incognito and completed the official login there. The native browser showed the AWS success confirmation. The local helper then reported AWS authentication valid, dashboard healthy, and realtime connected (approximately 150 KB snapshot). The existing dashboard tab's own expired CNC session was restored with the approved single-use local handoff; the UI visibly showed LIVE, PAUSED, 36 queued, zero running, and three fresh worker heartbeats.

Future automatic AWS login now sets a child-only `BROWSER` command selecting the installed Google Chrome executable with `--incognito`. It leaves dashboard browser selection and the OS default browser unchanged. AWS CLI's official login source delegates browser opening to Python `webbrowser.open_new_tab`; an isolated controller-selection check verified Chrome plus the `--incognito` argument without opening another auth flow. The focused Python tests passed and the installed private watchdog copy was refreshed.

This supersedes the earlier authentication gate: AWS sign-in and dashboard reconnection are now verified successful. Required AWS approval/MFA remains human-operated when AWS requests it.
