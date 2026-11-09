# Watch files → CNC merged

`watch_to_merged.py` is a standalone Python 3.9+ script for Linux/macOS. No pip packages or processor configuration are needed.

## Edit settings in the file

Edit the constants near the top of `watch_to_merged.py`: `FILES` contains
`(source_path, merged_filename)` pairs; `MERGED_DIR` is the destination folder.
The sample watches `/root/lulz/hasil.txt` and writes `hasil.txt` in merged.
Replace this sample with your actual output path before running.
Set `SSH_TARGET` to `None` on the controller, or an existing SSH alias / user@host
when sending from another machine. `SSH_CONFIG` and `SSH_IDENTITY` refer to
files on that sending machine. Set `STATE_PATH` for a fixed checkpoint path,
`INTERVAL_SECONDS` for polling and `FROM_END` to skip pre-existing data.

On Linux with systemd, run `python3 watch_to_merged.py` as root with no flags.
This installs the script into `/opt/cnc-watch-to-merged/`, enables the
`cnc-watch-to-merged` service at boot and starts it. If not root, use
`sudo python3 watch_to_merged.py`. No terminal/session needs to remain open.
The installed copy retains the settings you edited, even if you remove the
original script. After changing settings, run the original script again to
update the installed copy. An unchanged reinstall leaves the running PID alone.
Previous script/unit versions are saved with a `.previous` suffix.

The service restarts five seconds after a crash or SIGKILL, including an OOM
termination. It has a 128 MiB soft memory limit and 256 MiB hard limit,
configurable via `SERVICE_MEMORY_HIGH` / `SERVICE_MEMORY_MAX`. OOMPolicy stops
the entire attempt before restarting it, including SSH subprocesses. Logs go
to journald with per-service rate limiting. These settings limit this service;
they do not protect every other program from system-wide memory exhaustion.

Checkpoint/outbox remain at the same persistent path printed by the installer.
The service explicitly reuses that path after restarts and reinstallation;
there is no automatic state deletion or reset. Never delete the SQLite file,
its WAL/SHM files, or receiver receipts to recover from a crash. Disk-full errors
retain queued work when storage remains readable and retry; permanent storage
loss/corruption needs backups. Reboot, power loss and OOM resilience are not a
guarantee against hardware/filesystem failures or source data overwritten before
capture. Keep free disk space and independent backups of sources and state.

```sh
systemctl status cnc-watch-to-merged --no-pager
journalctl -u cnc-watch-to-merged -f
systemctl stop cnc-watch-to-merged     # deliberate stop stays stopped until start/boot
systemctl start cnc-watch-to-merged
systemctl disable --now cnc-watch-to-merged  # disable automatic boot startup
```

Use `--foreground` on Linux when you deliberately want a terminal-bound watcher.
On macOS, no-argument execution runs in the foreground. `--install` explicitly
installs a service and can persist additional CLI settings. Command-line flags still
override these settings; explicit `--file` replaces the configured `FILES` list.
Use `--from-start` to override `FROM_END=True`.

## On the controller

```sh
python3 watch_to_merged.py \
  --file /root/lulz/hasil.txt \
  --file /root/lulz/hasil-lain.txt=nama-di-merged.txt \
  --merged-dir /opt/cnc/merged \
  --state /root/lulz/.watch-state.sqlite3
```

`hasil.txt` sends to `merged/hasil.txt`. The second mapping renames its destination. Repeat `--file` as needed. Multiple sources may map to one merged filename.

## From another machine

Add an existing authorized SSH connection to the controller:

```sh
python3 watch_to_merged.py \
  --file /root/lulz/hasil.txt=hasil-gabungan.txt \
  --ssh root@CONTROLLER \
  --identity /path/to/existing-authorized-key \
  --merged-dir /opt/cnc/merged \
  --state /root/lulz/.watch-state.sqlite3
```

Or use `--ssh cnc-do-controller --ssh-config /path/to/ssh_config` on a machine with that existing alias. The SSH receiver is supplied automatically; no second script needs to be installed on the controller. Host-key verification remains enabled. The sending machine needs Python, OpenSSH and existing permission to connect/write to the controller. This script does not create SSH access.

## Behavior

- Checks every **200 ms** by default (`--interval 0.1` for 100 ms). Sends complete newly observed lines immediately on that pass, without waiting for a minimum batch size or for the file to close. Writers must flush buffered output to disk.
- Existing complete lines are sent on the first run. Add **`--from-end`** to start after existing bytes. Files created after the watcher starts are captured from their beginning.
- A partial line waits for its newline. Partial UTF-8 data survives a restart. Sources can be absent at startup; the watcher waits for them.
- Reads only incremental data while appending. When the file's modification time changes, verifies its already-read prefix to detect rewrites. Truncation/replacement rereads the new file; replacement drains the previously open file first. A rewrite may resend unchanged lines as part of the replacement. Old incomplete fragments are retained privately in the state database.
- Keeps the original sources intact. Target files are appended, preserving line order and intentional duplicate lines. Complete delivery to merged is acknowledged; queued data and byte positions are stored together in SQLite. A lost acknowledgement is safely retried using durable receiver receipts.
- Connection failures keep data in the outbox and retry with a bounded backoff. Capturing continues between retries. A partial interrupted receiver write can be recovered; conflicting external rewrites cause an explicit error and retain queued data rather than guessing.
- The existing CNC Drive watcher uploads merged files using its normal interval. “Delivered” means durably written to merged, not yet acknowledged by Google Drive.

Keep the **same `--state`** when restarting or adding watched files. Only one process may use a state file; use a separate state file for each independent watcher. Receipt files on the controller live in hidden `.watch-to-merged/`, outside the visible result filenames. Do not delete state while delivery may need retrying.

Use an output filename dedicated to these watchers rather than a file another application truncates or deletes. Do not watch the merged destination itself. No watcher can recover bytes that another process overwrote before they were observed; source rotation during a stopped watcher should retain the old source for recovery. A line is limited to 1 MiB.

Run with `--foreground` and press Ctrl+C to stop cleanly. `--once` performs one bounded capture/delivery pass and exits; it is for checks, not continuous watching. A systemd service continues after the installing terminal exits.

## Verification

```sh
python3 -m unittest infra/test_watch_to_merged.py -v
```

Tests use temporary synthetic files only: multiple files/renaming, live delivery while the source remains open, Unicode/partial lines, restarts, lost acknowledgements, replacement/truncation, newly created files, receiver crash recovery and the same stdio protocol used over SSH.

Verified on 2026-11-05: 14 local tests passed, including SIGKILL/restart with a
partial line and unchanged output after recovery. A uniquely named systemd
service with synthetic `/tmp` input/output also verified no-argument installation,
boot enablement, unchanged reinstall preserving the PID, automatic recovery from
SIGKILL, retained checkpoint/partial line, no duplicated output, effective 256 MiB
memory cap and `OOMPolicy=stop`. The temporary service and files were removed.
No production merged files or processor settings were changed. Actual system-wide
OOM and reboot/power-loss were not induced on the production host.

Earlier SSH protocol verification also delivered two synthetic files, preserved
an incomplete line until newline, reconnected, and recognized a replay without
duplicating it.
