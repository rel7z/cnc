# CNC Manager

Run the standalone Python file on your Desktop:

```sh
python3 ~/Desktop/CNC_Manager.py
```

Python 3.9 or newer is required. The menu opens the dashboard, configures an
existing server, deploys a new cluster, diagnoses connectivity, installs macOS
automatic reconnection, or stops the managed tunnel.

For this existing deployment, choose **1**. The dashboard is available through
`http://localhost:8081/dashboard`. The automatic tunnel starts after macOS login
and reconnects when the child process exits or repeated dashboard health checks
fail. It listens on loopback only. The computer must be awake and online.

## First-time setup

Keep the CNC Git checkout on the computer; this file invokes its source
installers. Choose **3**, or run:

```sh
python3 ~/Desktop/CNC_Manager.py --repo /path/to/cnc deploy
```

Both providers require Git and OpenSSH. Install Go/Node dependencies on the
servers through the existing deployment installers, rather than locally.

- **AWS:** install AWS CLI and the Session Manager plugin; configure an AWS CLI
  profile with credentials permitted to manage EC2, VPC/security groups, IAM
  instance profiles, SSM, and the private S3 release bucket, and read pricing and
  service quotas. The initial installer supports `us-east-2` (Ohio). Choose a
  profile and worker count. The manager reads current account inventory, quota,
  and compute pricing before proposing resources.
- **Linode:** supply a token with read/write permission for Linodes and relevant
  SSH-key operations. The token is requested without echo and is not saved by
  the manager. Choose region, controller plan, worker plan, and worker count.
  The manager validates the region and plans using the API and shows base cost.

Review the displayed estimate. Only typing `DEPLOY` authorizes resource creation.
Then choose the dashboard username/password. The algorithm provisions or resumes
recorded nodes, waits for SSM/SSH, packages the allowed repository source, builds
and deploys the controller and workers, installs dashboard authentication, checks
the deployment, saves the local tunnel configuration, synchronizes the complete
local tools tree, and opens a tunnel.
Existing verified deployments are refused by this first-time flow; use setup
instead. Partial deployments retain their state for another deploy attempt.
Interrupting deployment does not delete billed resources or cancel remote work
already accepted by the provider. Inspect the private setup log before resuming.

AWS uses a source archive and SSM; Linode transfers a source Git revision over
SSH. Private `processor/tools/` files are transferred separately as opaque bytes
through the private sync path. Tools are never executed by the manager. Route
registration and Google Drive authorization require separate setup.

## Commands and files

```sh
python3 ~/Desktop/CNC_Manager.py connect
python3 ~/Desktop/CNC_Manager.py doctor
python3 ~/Desktop/CNC_Manager.py setup
python3 ~/Desktop/CNC_Manager.py install
python3 ~/Desktop/CNC_Manager.py stop
python3 ~/Desktop/CNC_Manager.py sync
```

`connect` synchronizes the complete tools tree before opening the dashboard. It
runs in the foreground if no managed launcher is installed. Leave its
terminal open. `install` enables automatic reconnection on macOS. `stop` stops
the current managed tunnel; it does not delete cloud resources or uninstall the
login launcher. Run `install` to start it again, or use foreground `connect`.

Configuration and logs live in `~/Library/Application Support/CNC Manager/` on
macOS. Other systems use `~/.config/cnc-manager/` and foreground connections.
`--config PATH` selects a separate configuration; `--no-browser` suppresses browser
opening. Deployment credentials remain in private provider state directories in
the local checkout. Keep these directories and the setup log out of Git.
Use `--no-sync connect` for tunnel-only access. The diagnostic `--once connect`
also skips sync. The background tunnel supervisor does not copy files on each
reconnect; normal dashboard opening and first-time deployment perform the sync.

The replaced legacy macOS tunnel plist is retained under the support directory
as `com.cnc.aws-dashboard-tunnel.plist.rollback`.

## Private tool synchronization

Menu **7**, or `sync`, recursively discovers **all files and folders** beneath
`processor/tools/` and copies them to `/opt/cnc-processor/tools/` on the configured
controller. This includes `bf`, `divi`, their supporting files, and newly added
folders without editing the launcher. Nested directory paths and owner execute
permissions are preserved. Source files are copied as opaque bytes: no parsing, imports,
execution, route registration, or tool tests. Existing destination files have
rollback copies; unrelated destination files are retained. Symlinks are refused.

Both AWS and Linode compare SHA-256 hashes after installation. AWS uses the
recorded encrypted private release bucket and SSM; Linode uses SSH/SCP. The AWS
account must match local deployment state, and the bucket must block public
access. AWS credentials must be current; if they expire, run
`aws login --profile cnc` (or the selected profile) and complete its sign-in using
the same browser session, including incognito when appropriate.

`last-sync.json` in the manager configuration directory records the verified
manifest and rollback location. If observing an AWS command fails or times out,
`pending-sync.json` retains its command ID. Rerunning `sync` observes that same
command rather than starting another transfer.

General source deployment excludes private tools and preserves an existing
processor configuration. Sync permission therefore does not imply that a tool
becomes active: adding a program/route or running its manifest requires separate
authorization under the repository policy. GitHub publication of this manager
does not include private tool files, credentials, or runtime receipts.

## Verification scope

The isolated unit tests exercise both providers' confirmed and cancelled wizard
flows using simulated API/process responses, loopback tunnel arguments, private
configuration permissions, stale-child reconnection, and failed-install rollback.
They create no cloud resources and do not inspect private tools. Actual access
is verified separately against the existing AWS controller. A fresh billed
deployment has not been performed as part of delivering this launcher.
