# Linode deployment

Provision an 8 GB shared CPU server and six 4 GB shared CPU workers in Singapore:

```sh
export LINODE_TOKEN='your-token'
python3 infra/linode/provision.py             # print resource and price plan
python3 infra/linode/provision.py --apply     # create/resume matching labels
python3 infra/linode/release.py --public-dashboard
```

The default listed total is $192/month, excluding backups and other extras. Instances remain billable until deleted. API tokens need permission to read catalogs and create Linodes. Deployment uses Ubuntu 24.04 and an isolated `codex/linode-deployment` Git release without altering the user's staging index. Every node's `/opt/cnc/RELEASE` is checked against the release, and all six workers must register before deployment verification succeeds.

Private runtime files are stored in ignored `.linode/` with restricted permissions: SSH key, cluster state, SSH configuration, and generated dashboard credentials in `dashboard-access.json`. The UI and backend sources contain no API token. The HTTP dashboard uses the Next.js login page and a signed HttpOnly session cookie; Nginx proxies requests to it. Raw server services bind to the server's private IP. Management endpoints accept only the local dashboard proxy; workers retain access to downloads. HTTP credentials travel without encryption, as requested for this deployment.

Open **Server Management → Linode** to connect an API token, view live regions and sizes with prices, and create an additional worker. The UI supports Ubuntu 24.04 cloud-init, optional paid backups, private server addresses, instance status, and actual CNC registration. Use the server's region for private networking. New workers inherit server administrator SSH keys, optionally accept an additional public key, fetch the binary and tools from the private server, record its Git release, and start a systemd service. The server needs `/opt/cnc/cnc-worker-linux` and `/opt/cnc/worker-tools.tar.gz`, installed by the Git release script.

Creation is not retried automatically. If a request times out, refresh the instance list and reconcile the label before another attempt; an instance may have been created even if its response was lost. Existing duplicate labels are rejected. Settings responses expose only whether a token is configured; stored tokens are validated against Linode before an atomic replacement with mode 0600.

Batch creation accepts a worker count from 1 to 20. One worker uses the label as entered; larger batches use `prefix-1` through `prefix-N`. All labels are checked for collisions before creating any instances. The displayed base price is multiplied by the count. Creation stops at the first provider error and reports confirmed creations, the uncertain label, and remaining unattempted labels; refresh the instance list before retrying a partial batch.

## Dashboard login

Open `/login`. The user-requested default account is `fahrel` / `fahrel`. Runtime credentials and the randomly generated session-signing secret are kept in `/opt/cnc/login.env` (0600), loaded by a systemd drop-in for `cnc-ui`. Existing login configuration survives redeploys. To change it, update that environment file and restart `cnc-ui`; credential changes invalidate existing sessions.

Dashboard pages redirect unauthenticated visitors to login. API requests without a valid session return 401. Sessions last 12 hours; the header's **Keluar** button clears the browser session. Login and logout reject requests from a different origin.

`python3 infra/linode/verify-login.py` checks public login, wrong-password rejection, same-origin enforcement, session-cookie flags, protected API access, and logout. `verify.py` and `verify-processor.py` now authenticate using the login form API.
