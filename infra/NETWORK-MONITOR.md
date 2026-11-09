# Worker network monitor

The Workers table and Processor worker cards show download (RX) and upload (TX)
rates in B/s, KiB/s, MiB/s or GiB/s. Hover over the rate to see individual
interfaces. These are host-wide interface counters, including programs outside CNC.
Loopback is excluded. Totals sum non-loopback interfaces; tunnels/bridges can
count the same traffic on multiple interfaces. They are not Internet billing totals.

The worker reads `/proc/net/dev` with its existing resource sampler and sends
rates through the existing heartbeat. No packet capture, destination tracking,
new listener, credentials or firewall changes are involved. Only the newest
snapshot is retained in memory; there is no metrics database or history chart.

The first sample warms up. Sampling is limited to at most once per second so
other resource-sampler callers do not shorten the measurement window. Counter
resets and newly appearing interfaces establish a baseline without underflow.
Unsupported/malformed telemetry is unavailable; readings older than 15 seconds
are marked stale. Dashboard refresh follows worker heartbeats, not packet arrival.

Verification: `go -C processor test -race ./cluster -run 'Test(NetworkRates|ParseNetwork)'`.
The UI must be built with the deployment's actual `GO_API_URL`, matching the UI
service environment, because Next.js embeds rewrite destinations at build time.

Deploy server, worker and processor core binaries together, plus the UI.
Update the controller's downloadable `cnc-worker-linux` for future workers.
Only restart workers when idle and retain rollback binaries/UI. This does not
require inspecting or changing `processor/tools/`.
