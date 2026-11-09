# CNC Manager verification — 2026-10-16

- Desktop deliverable: `/Users/fahrel/Desktop/CNC_Manager.py`.
- Repository source, Desktop copy, and installed supervisor have matching SHA-256:
  `55c478934fb394717a43610235ddc552f9df84734f29244683c1e69d567ea86e`.
- `python3 -m unittest infra/test_cnc_manager.py`: 13 tests passed. Confirmed and
  cancelled AWS/Linode wizard flows use mocked APIs and subprocesses. No new
  cloud resources were created. Includes stale-child reconnection and failed
  macOS install rollback.
- Python syntax checks passed for manager, Linode release, and embedded login
  installer Python. `bash -n infra/linode/install-login.sh` passed.
- Actual foreground tunnel worked on spare localhost port 8082; its process and
  children were stopped after verification.
- Actual macOS manager installation succeeded after adding an explicit working
  directory. `com.cnc.dashboard-manager` has RunAtLoad and KeepAlive enabled.
  Legacy launch agent is removed from login startup and retained for rollback.
- Desktop `--no-browser --once connect` returned ready on localhost:8081.
- Desktop `doctor` reported local dashboard healthy and AWS SSM node Online.
- Final local `/login` response: HTTP 200. Configuration file permission: 0600.
- Read-only SSM command `936f12c4-46a5-4f8f-a156-46dc160dcfc5`: Success;
  cnc-server, cnc-ui, and cnc-processor active; controller loopback port 3000
  `/login` HTTP 200. Port 80 unavailable; the configured tunnel uses port 3000.
- Cloud services were not restarted and no scan was launched by this work.
  Protected `processor/tools/` was not accessed.

Fresh billed cloud provisioning has not been exercised. The interactive wizard
uses existing source installers and requires typed cost confirmation before
calling their provisioning paths. See [usage](../cnc-manager.md).
