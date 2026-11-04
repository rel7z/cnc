# SSH identity and new deployments

CNC provisioners reuse one Mac-local identity:

`~/Library/Application Support/CNC Manager/ssh/id_ed25519`

Keep this private key backed up securely. Never commit it or upload it to a VPS.
Only its public key is registered with a provider or installed on a server.
A changing client IP does not change this identity. This is possession of a key,
not a hardware-bound restriction: anyone with a copy of the private key can use it.

DigitalOcean, Linode, AWS and UpCloud provisioning scripts use
`infra/ssh_identity.py` and `infra/ssh_policy.py`. Existing provider keys are
preserved for legacy server access. New accounts require their own valid API
credentials; the same SSH public key can be registered there.

For Termius, import/select the private key locally and choose key authentication.
Use the SSH username configured for the server (root for current DO nodes,
ubuntu for AWS). Password SSH login is disabled on the three current DO nodes.

## Current network route

Use `.digitalocean/ssh_config`. The controller and worker 2 connect through
worker 1 using their private addresses. Key-only authentication does not repair
public-IP routing timeouts. On the current network, retain the jump route.

## Safe policy rollout

`infra/apply_ssh_key_only.py` is a remote helper requiring an injected POLICY.
Prepare first; apply one node at a time; test a fresh key connection and rejection
of password-only authentication; confirm only after both succeed. An unconfirmed
application automatically restores the previous drop-in after 180 seconds.

The 2026-11-04 backups on each current DO node are in:
`/var/lib/cnc-ssh-policy/mac-key-20261104/`

Verification receipt (metadata only):
`.digitalocean/ssh-key-only-verification-20261104.json`.

Changes to provisioning source apply when those provisioners are next run.
The controller's Go provisioning default was also built and deployed on
2026-11-04, so dashboard-created Linode workers disable password SSH too.
Controller rollback binary:
`/opt/cnc/releases/ssh-key-default-20261104/cnc-server.previous`.
Future Go provisioning changes still require a controller rebuild/deploy.
