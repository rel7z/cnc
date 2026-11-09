# Integrated Git deployment to UpCloud

Set `UPCLOUD_TOKEN` through your secret manager or shell environment, then run:

```sh
python3 infra/upcloud/release.py
```

This single command provisions or reuses one server and two workers, pushes a
source-only Git release to the server, builds and tests the Go services from
that checkout, builds the remaining tools from source and installs both workers over the utility network, and builds the
Next.js dashboard from the same checkout. It verifies worker registration,
dashboard HTTP health, and identical release revisions on all nodes.

Default: Singapore, STARTER-1xCPU-4GB, three nodes, 12 GB total RAM. Change node
count, zone or plan with `--workers`, `--zone` and `--plan`. Five workers require
additional account quota. RAM and CPU quotas are checked before node creation.

The local deployment branch is `codex/upcloud-deployment`; the current working
branch and its index remain unchanged. Source snapshots exclude node_modules,
binaries, deployment credentials, environment files and runtime configurations.
Application configuration is generated for the actual provisioned addresses.
The server's deployment Git repository is `/opt/cnc/git/repo.git`, its checkout
is `/opt/cnc/source`, and every node records the deployed SHA in `/opt/cnc/RELEASE`.
No GitHub push is needed to deploy. New local source changes are included in the
next deployment snapshot. Preserve `.upcloud/` to retain state and SSH access.

Only the server has public IPv4. Workers use private utility networking. The
Go API and TCP listeners bind to the private address; the dashboard binds to
loopback. Access the dashboard through SSH:

```sh
ssh -F .upcloud/ssh_config cnc-test-server -L 3000:127.0.0.1:3000 -N
```

Then open `http://localhost:3000/dashboard`. The SSH key remains local; release
installation uses a temporary SSH agent to connect from server to workers.

Requires Python 3, Git, SSH and ssh-agent locally. Go and Node are downloaded
on the server from their official sites with checksum verification. Services
run under systemd. The UpCloud token is never committed or stored in state.

Do not run concurrent deployments. Resource creation is never automatically
retried; read requests retry transient failures. Rerun after a failed deployment
to reuse the existing nodes. Resources incur charges until deleted in UpCloud.
The deployment does not delete existing cloud resources or install external
worker-tool repositories. The search parser requires your own Apify token when used.

`provision.py` can also inspect quotas without creating resources. `--apply
--provision-only` creates the infrastructure for Git deployment. The older
binary-upload path and `deploy-ui.sh` remain available for manual diagnostics;
use `release.py` for a complete deployment from one revision.

API reference: https://developers.upcloud.com/1.3/8-servers/

The provisioner changes firewalls only with `--configure-firewall`, which requires
operator authorization. Otherwise it writes a proposed private service
rule plan to `.upcloud/firewall-plan.json` for separate operator review. Current
UpCloud default firewalls block private outbound SSH and worker port 9090, so
full deployment needs those service flows allowed. When changes are declined,
`python3 infra/upcloud/release.py --server-only` deploys the server, tools and
dashboard while leaving the workers unconnected. This is a partial deployment.

## Public dashboard

```sh
python3 infra/upcloud/release.py --public-dashboard --configure-firewall
```

This enables only the required private services and publishes an authenticated
HTTP dashboard on the server's public IP. Nginx protects the frontend and API;
raw API and worker listeners stay private. Credentials are generated once and
saved with mode 0600 in `.upcloud/dashboard-access.json`, never in Git. HTTP was explicitly requested; login credentials are transmitted without encryption.

UpCloud trials reject firewall changes with `TRIAL_FIREWALL`. Their existing
rules allow HTTP and HTTPS, so `--server-only --public-dashboard` can publish the server
and dashboard during trial; workers stay unconnected until the account is
upgraded and the private service rules can be applied. Do not disable or route
around provider trial restrictions.
