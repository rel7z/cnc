# Durable spread inputs — 2026-11-22

Spread tasks retain their most recently received chunk at `/root/<filename>`. Each task still executes against its isolated temporary input; task cleanup removes that workspace and leaves the stable copy. Sending the same filename again atomically replaces the retained copy with its newest chunk.

Configuration defaults, DigitalOcean installation, AWS installation and Linode provisioning/release paths set `spread_workspace_root` to `/root`. `infra/digitalocean/update-workers.py` publishes the downloadable artifact, drains active tasks before replacement, preserves input files, verifies the running binary checksum and retains rollback copies.

The deployed Linux worker SHA-256 is `c308576a9de0568997a23ff592bbb37a99ec26787dd29338a9e3ef8b41ae54d8`. All eight workers use this binary. Harmless spread job `job_1795287766_222` delivered eight distinct 112-byte dummy chunks to `/root/cnc-spread-durable-proof-20261122.txt`; each stable file matched its controller chunk SHA-256 after its task input was removed. No scanners or private processor programs were run.

Regression checks: `go test . -run 'TestShellWorkspace|TestSpreadConfigRetainsDefaultRoot' -count=1` passed.

A second drain-safe rollout completed on all eight workers. The same eight retained files were then checked again: their SHA-256 checksums remained identical and temporary task inputs remained absent. The controller downloadable artifact and every running worker matched the release checksum. Local release package and manifest were synchronized. Rollback artifacts remain under `/opt/cnc-deploy/worker-release-before-*` on workers.
