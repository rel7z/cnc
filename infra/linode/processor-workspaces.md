# Worker folders, SSH and output names

Workers use `/root/processor/<tool>/` by default. For example, the registered
program `bf--scanner` runs directly in `/root/processor/bf/`. Its registered
executable, supporting files, input and newly created files are visible there.
Installing a program prepares its files without launching it. This default also
applies to new workers; `processor_workspace_root` can explicitly override it.

Only one run of a tool may use that folder on a given worker. Other workers can
run that tool concurrently, and unrelated tools have separate folders. Both the
controller admission check and an OS file lock enforce this boundary. A waiting
run retains lease renewal and observes cancellation.

Files remain after execution. Before a later batch starts, previous selected
output files move into `.cnc/history/<new-attempt-id>/` so their old contents do
not get published as results of the new batch. Registered sources and input are
refreshed from that run's bundle/input. Arbitrary additional files stay in place.
`.cnc/current.json` shows the run ID, version, working folder and retained log
path, without execution credentials. Old attempt metadata and immutable bundles
remain in the worker's existing data directory for recovery and result delivery.

## Choose files and merged names

In **Processor → Programs & outputs**, edit a setup and add one row per file:

```json
{
  "path": "x.txt",
  "merged_name": "apalah.txt",
  "drive": true
}
```

This selects `x.txt` beside the worker program and merges its complete lines
into `apalah.txt` on the controller, using the existing deduplication and Drive
delivery flow. Worker files keep their original names. An empty merged name
retains the existing `<program>__<file>.txt` convention. Files absent from the
output list are not automatically synced by the supervisor. `drive: false`
retains the existing internal-only output behavior and downstream routing.
Settings and saved templates retain these mappings; running attempts keep their
pinned settings until completed.

## SSH from the dashboard

**Processor → Workers & SSH**, the worker list, and each assigned run provide an
**SSH** button. Copy the CNC Manager command into Terminal on the owner's Mac;
it uses the existing SSH alias/key and checks the host key. It opens a shell in
the selected processor folder. Direct SSH commands and an SSH-app link are also
available for other computers with an already authorized key. No password or
private SSH key is exposed in the UI.

The CLI also supports:

```sh
python3 ~/Desktop/CNC_Manager.py worker-ssh --worker cnc-linode-worker-5 --folder /root/processor/bf
```

This connects only; it never syncs or launches private tools.
