# Linode integration rollout: verified

Eight new workers in `sg-sin-2`, each `g6-standard-4` (8 GB / 4 vCPU). Compute price: $48 per worker per month, $384 per month total, excluding taxes, transfer and extras. No backups enabled.

The existing AWS controller remains in place. Worker registration and program-download addresses are supplied automatically through a persistent WireGuard connection. Address overrides are hidden behind Advanced. The download address serves worker software from the controller; it is not an output-upload destination.

## Verified

- All eight provider instances running and registered online in the live dashboard.
- All eight worker services and WireGuard services active, with recent handshakes.
- All eight workers can download the controller release marker through the encrypted tunnel.
- Each worker reports 4 CPUs and 7941 MiB usable RAM; provider plan is 8192 MiB.
- Dedicated security group `sg-0e6406261ae8960ca`: UDP 51820 only, exact eight worker public IPv4 /32 sources.
- No public exposure of controller TCP 8080 or 9090 added.
- Focused Linode tests passed on Linux with race detection; UI production build/TypeScript passed.
- Fixed missing Node runtime bootstrap. Node v22 is installed from the official distribution over HTTPS and verified against SHA256 checksums.
- New-node infrastructure repair completed on the same eight instances; no duplicate creation. Historical first-boot cloud-init failures remain historical; worker services are now active.
- No processor tool content accessed and no verification jobs initiated. Existing processing state retained.

## Release and rollback

- Initial integration release: `/opt/cnc/linode-releases/bc4de829a23c9ee0`.
- Final bootstrap fix release: `/opt/cnc/linode-releases/136a7c39cc684202`.
- Deployment source Git snapshot on controller: `4fbd26df36ceb74cb02ad0fec67c1b16aeccfa5f` (local audit snapshot; not pushed).
- Initial deployment command: `408cdcd7-393d-4b8a-b5b3-00ba7c6cb26e`.
- Final deployment command: `10afd5b7-976d-4ced-be49-306db761640b`.
- Creation command: `6361f50c-0d44-4b90-939c-c1a7673c94af`.
- Controller registration audit: `783aa35b-df72-4efc-aa59-334b2e458430`.
- Previous controller binary and UI override retained in each release for rollback.
- Private runtime state, peer keys and credentials remain outside source control. Local state and SSH config use ignored `.linode/cross-cloud-*` files.

| Worker | Linode ID | Public IPv4 | Tunnel IPv4 |
|---|---:|---|---|
| cnc-linode-worker-1 | 108447688 | 172.237.87.96 | 10.203.88.2 |
| cnc-linode-worker-2 | 108447693 | 172.237.87.186 | 10.203.88.3 |
| cnc-linode-worker-3 | 108447697 | 172.237.74.21 | 10.203.88.4 |
| cnc-linode-worker-4 | 108447706 | 172.236.155.108 | 10.203.88.5 |
| cnc-linode-worker-5 | 108447712 | 172.236.155.203 | 10.203.88.6 |
| cnc-linode-worker-6 | 108447720 | 172.236.155.236 | 10.203.88.7 |
| cnc-linode-worker-7 | 108447725 | 172.236.155.142 | 10.203.88.8 |
| cnc-linode-worker-8 | 108447729 | 172.236.155.143 | 10.203.88.9 |
