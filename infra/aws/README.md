# CNC on AWS

The development layout is one 8 GiB `m7i-flex.large` controller and seven 4 GiB
`c7i-flex.large` workers in `us-east-2`. Each instance uses 2 vCPUs. The account's
present standard-instance quota is 8 vCPUs, so only the controller and three workers can run until AWS
approves 16 vCPUs. The worker count is a capacity limit, not a requirement to
run all jobs at once; CNC's existing admission controls still govern job starts.
After approval, `python3 infra/aws/startup-deploy.py --add-workers --accept-compute-hourly-usd 0.33916` adds workers 4–7 at the current verified
compute rate. Recheck `python3 infra/aws/provision.py` first because prices and
quota can change.

`provision.py` shows the account, quota, selected VPC and subnet, and live
compute price without creating resources. `rebuild-development.py` creates the
controller and three development workers after previous instances are gone.
The startup deployment route uses first-boot
scripts and a private, encrypted S3 release bucket when SSH and SSM cannot
reach fresh instances. Release archives include only explicitly selected CNC
source directories. The protected `processor/tools` and
`docs/processor/tools` trees are never bundled.

The dashboard's Server Management → AWS tab shows EC2 instances, CNC worker
registration, quota, and the price of new workers. Worker creation from the
dashboard requires an explicit cost check. AWS credentials stay in the
controller's instance role; no access keys are stored in the dashboard.

The dashboard binds to loopback on the controller. Run
`infra/aws/open-dashboard.sh` to open a local AWS Session Manager tunnel, then
visit `http://localhost:8081/dashboard`. The official Session Manager plugin
is installed in ignored `.aws-cnc/bin` rather than system-wide. Dashboard
credentials default to `fahrel` / `fahrel` on a fresh deployment and are recorded
in the ignored, mode-0600 `.aws-cnc/dashboard-access.json`. The controller API listens on its
private VPC address, and worker security groups accept SSH only from the
controller group. Google Drive starts disconnected on a new controller and
must be connected using the dashboard before uploads can resume. The processor
starts with empty routes and programs until the owner explicitly authorizes
registration and sync of any private tool files.

Every EC2 node also has a 40 GiB encrypted gp3 root volume. EBS, public IPv4,
and transfer charges are additional to the displayed compute price. Terminating
the EC2 nodes stops compute charges; retained S3 objects and other resources
may still incur charges. The source release remains in `.aws-cnc/` for rollback.

## One-click private dashboard and recovery

Install or update local recovery with `infra/aws/open-dashboard.sh --install`.
This installs **CNC Dashboard.app** in the user's Applications folder and a
macOS LaunchAgent that checks the local forwarding tunnel once a minute.
Opening the app repairs the tunnel and signs into the dashboard using the
existing ignored `.aws-cnc/dashboard-access.json`; no password is embedded in
the app or URL. Browser sign-in uses the normal server login and a single-use,
60-second localhost cookie handoff. Session cookies are kept in memory and
remain HttpOnly. No server authentication change is required.

`infra/aws/open-dashboard.sh --check` diagnoses the HTTP login page, AWS
identity, and authenticated realtime snapshot. `--repair` repairs only the
local tunnel. `--browser-link` returns a single-use local link for an existing
browser tab. Expired AWS login starts the official browser flow, limited to one
attempt per 15 minutes; required approval/MFA is completed by the owner. Network
failures and stale request signatures are reported separately from expired
credentials. Port 8081 is never reclaimed from an unrelated program.

The separately approved Codex thread automation checks browser connectivity
and login every five minutes while available. The native one-minute watchdog
does not depend on that automation. Neither resumes the processor, starts
jobs, opens a public port, nor reads private tool code.

Dashboard bootstrap and realtime snapshots contain the 100 most recent tasks.
Full stored history remains available through the original task API; a job's
details load through `/api/jobs/{id}/tasks`. This keeps historical task volume
from blocking the initial realtime snapshot over Session Manager.

Automatic AWS login opens **Chrome Incognito**, matching the owner's AWS console
session. This selection is scoped to the login child process; dashboard links
and the system's default browser are unchanged. Keep the authorized AWS console
session available there; complete AWS approval/MFA when requested.
