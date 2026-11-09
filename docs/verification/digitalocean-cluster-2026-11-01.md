# DigitalOcean deployment — 2026-11-01

Three Singapore servers are active, each with 8 GB RAM / 4 vCPU. Total: $144/month before tax.

| Role | Public IP | Private IP |
|---|---|---|
| Controller | 143.198.214.173 | 10.104.0.3 |
| Worker 1 | 165.232.162.99 | 10.104.0.4 |
| Worker 2 | 167.71.202.121 | 10.104.0.5 |

The old wp-bruter-test server was deleted. Firewall applies to all three servers. Dashboard and processor ports are loopback-only; backend ports accept cluster traffic only. SSH accepts the admin address and cluster traffic.

All services are active. Both workers completed job job_1793540189_1 with exit code 0 and exact output `cnc-digitalocean-ready`. Executable hashes and hardware were verified. Authenticated dashboard returned 200; unauthenticated API returned 401.

Dashboard: http://localhost:8113/dashboard through the running local SSH tunnel. CNC Manager defaults now select DigitalOcean. macOS auto-reconnect installation failed with launchctl exit 5; the current background SSH tunnel works, but automatic login startup is not installed.

Processor is paused with seven default version checks. This is a fresh deployment: zero private programs configured; processor/tools content was neither inspected nor copied. Google Drive is not connected.

Reconnect manually from the repository with `python3 infra/cnc_manager.py connect --no-browser`.

Machine-readable verification is saved alongside this report. Credentials and API tokens remain in protected local storage and are excluded from Git.
