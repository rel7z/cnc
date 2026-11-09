# CNC Manager auto-port recovery — 20 October 2026

## Result

Dashboard recovered at `http://localhost:8083/dashboard`. The saved local port
changed from 8081 to 8083. Docker retained its original listener on 8081. The
SSM tunnel was independently verified listening at `127.0.0.1:8083` only.

The controller was online in SSM. Read-only cloud verification showed `cnc-ui`
and `cnc-server` active; the remote login page returned HTTP 200 with CNC and
password markers. No cloud service was restarted and no tools were resynchronized.

## Behavior added

- Reuse a healthy CNC tunnel without changing its port.
- For an occupied/unbindable port, try the next available local port, checking
  both IPv4 and IPv6. Save the new setting and a private configuration backup.
- Adopt a background supervisor's saved port change if it happened during sync.
- Do not mistake an unrelated application's username/password form for CNC.
- Automatically run bounded, read-only diagnostics on connection recovery
  failure and periodically during failed retries, at most once per minute.
- Diagnose port conflicts, expired AWS sessions, offline SSM registration,
  failed provider checks, timeouts and unavailable forwarded dashboards. Raw
  provider errors and credentials are not printed or saved in diagnosis output.
- Save automatic failure diagnostics to `last-diagnosis.json` alongside the
  connection configuration.

## Installed copies and verification

- Updated repository source: `infra/cnc_manager.py`.
- Updated Desktop copy: `/Users/fahrel/Desktop/CNC_Manager.py`.
- Updated existing agent copy:
  `/Users/fahrel/Library/Application Support/CNC Manager/cnc_manager.py`.
- Installed source SHA-256:
  `d4928bbdaa16007706de5cd4cab31e5fdd7885b9c92b32e1641f89484719c5ca`.
- Both installed copies matched that hash. The existing local agent was reloaded;
  no new automation was installed.
- The Desktop launcher independently returned Ready using `--once --no-sync`.
- All 35 isolated manager tests passed. Tool-sync tests use inert synthetic
  temporary files only; actual private tools were not accessed.
- Prior manager/config/agent copies were backed up under
  `/Users/fahrel/Library/Application Support/CNC Manager/manager-rollbacks/1792437145396092000`.
