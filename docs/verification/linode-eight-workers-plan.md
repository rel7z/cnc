# Linode automatic connection and eight-worker rollout

Status: completed and verified. See `linode-eight-workers-verified.md` for final deployment evidence. The options below record the original planning stage; option 1 was selected.

Requested: eight new workers, each 8192 MiB RAM and 4 vCPU.

Observed:
- Active controller: AWS, private listeners 172.31.4.149:9090 (registration) and 172.31.4.149:8080 (program downloads).
- The controller's saved Linode token is valid; the account inventory is empty. The local .linode token is stale (HTTP 401); old instance addresses are not authoritative.
- Public Linode catalog: g6-standard-4, 8192 MiB, 4 vCPU, $0.072/hour and $48/month. Eight machines total $0.576/hour, capped at $384/month at standard regional pricing; backups, taxes and other extras excluded. Region-specific pricing must be checked before creation.

Prepared local change:
- Resolve controller endpoints from its real network interfaces and Linode inventory rather than the label cnc-linode-server or browser Host headers.
- Enforce matching private-network region for automatic configuration.
- Hide address inputs behind an advanced override and explain their purposes.
- Refuse automatic provisioning when a controller on another provider has no configured network path.
- Focused TestLinode tests and UI TypeScript check passed. Not yet deployed.

Remaining deployment decision:
1. Retain AWS controller and establish an encrypted private connection to the new Linode workers. Proposed WireGuard: controller UDP 51820 permitted only from the eight allocated worker public IPs; per-worker peers; worker routes limited to the controller private IP. Keep dashboard access private. Never expose raw worker/download listeners publicly. No jobs or tool execution as verification.
2. Alternatively use a Linode controller in the workers' region. This requires an additional machine and a separate migration plan for configuration, state and secrets, with its cost disclosed.

For option 1, prepare an isolated release and rollback for the integration; record eight unique labels, provider IDs and installation progress. Reconcile uncertain provider responses by label before retrying. Verify the encrypted connection, download availability using metadata only, worker registration, resources and heartbeat. Do not access processor/tools or activate tools. Do not create billable machines until the topology and cost have been confirmed.
