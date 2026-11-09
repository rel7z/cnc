# Worker password display

Open **Workers → SSH → Show password** to reveal a worker's saved SSH password.
Use **Copy password** to paste it into a terminal or SSH client. Hide clears the
revealed value; closing the dialog clears password fields and ignores pending
responses. Revealed passwords also hide after one minute. The dialog displays
the SSH username, host and port, and retains CNC Manager key-based login.

New workers created through the dashboard's Linode integration retain their
root password before the provider creation request. When a deployment-owned
`DataDir/worker-default-password` policy exists, it replaces random passwords
for new workers and configures root SSH password login. The same policy is
applied by dashboard AWS provisioning; the standalone Linode provisioner reads
its private local `.linode/worker-root-password` counterpart for workers only. This also retains
the credential when a provider response is lost. API tokens and passwords are
not returned in ordinary worker lists or creation responses.

Old worker passwords were not saved by earlier provisioning. They cannot be
recovered from Linux password hashes; the owner has now explicitly authorized
replacing all seven active workers' root passwords with the saved default. **Save a known password** stores a password
the owner already knows; it does not reset or verify the worker's password or
change SSH authentication settings. Without a configured policy, key-only/random provisioning keeps its previous behavior.

Credentials live in controller runtime `DataDir/worker-logins/`, with directory
mode 0700 and atomic file writes with mode 0600. Back up this runtime directory
privately with controller state. The dashboard route requires a valid session,
same-origin POST and a bounded body; the backend route accepts only local
controller/dashboard calls and registered worker IDs. Responses are no-store.

Fixture tests verify reveal/save persistence, permissions, remote access denial,
unknown workers, invalid IDs, Linode password retention, authenticated proxy
behavior and UI reveal/copy/hide/close including late responses. The production patch `worker-login-1792741077` is deployed on 23 October 2026.
All seven active workers passed password-only SSH login as root using pinned
ED25519 host keys. Worker service PIDs were preserved. The controller retained
the policy and per-worker credentials with mode 0600; authenticated dashboard
reveal returned 200 and unauthenticated reveal returned 401. Server, dashboard
and processor are healthy, and all seven workers reconnected. The processor
and worker services were not restarted; the controller/UI were restarted while
idle. No private processor tool was accessed or executed.

Controller/UI rollback: `/opt/cnc-controller/releases/worker-login-1792741077/rollback`.
Worker login rollback: `/opt/cnc/rollouts/worker-login-1792741077/login-rollback`.
Private local receipts: `.linode/worker-login-verification.json` and
`.linode/worker-login-production.json`. Password values remain in private runtime
files and are deliberately omitted from source, receipts and logs. Application
patches preserve controller runtime data and SSH login settings. Reapplying the
worker policy uses `infra/linode/apply-worker-login-policy.py` with the verified
active deployment inventory; its original rollback copies are retained.
