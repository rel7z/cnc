# Clean URL exports and balanced enum batches

Local changes prepared on 2026-10-15:

- Plugin result files such as `wp-file-manager.txt` and `litespeed-cache.txt`
  now contain a single clean site URL per line, without appended component,
  version, or confidence fields.
- `plugins_name.txt` contains clean plugin directory URLs only.
- Default ports are removed according to the URL scheme. Explicit nondefault
  ports and URL paths remain intact.
- New enum jobs create one balanced shard per requested worker. Three workers
  receive three parts, with line counts differing by at most one. The old
  1,000-line / 1 MiB and proposed 16 MiB split thresholds are removed. Existing
  jobs keep their original snapshots until explicitly restarted.
- Drive merging cleans the exact legacy enum metadata layouts before
  deduplication, preventing old Drive history from restoring appended fields.

Validation passed:

- `go test -race ./...` in `cnc-backend/enum`.
- `go test . -run 'TestEnum|TestGDrive|TestDrive' -count=1` in `cnc-api`.
- Binary execution against a disposable localhost WordPress fixture, verifying
  clean output in both named plugin files and the aggregate plugin-path file.
  Structured plugin evidence was retained.
- 100,001 input lines with three workers produce three immutable shards,
  retaining every input byte and order; repeating preparation reuses identical
  shards.
- A single-worker input larger than 16 MiB remains a single shard; no artificial
  extra parts are created and no input lines are lost.

The changes are now deployed. An initial attempt to restart a different live
job was rejected before execution. Subsequent read-only checks established that
the job was already cancelled and no jobs were active. Deployment then used
idle-only activation and did not cancel, restart, or launch a scan. See
[production verification](enum-three-workers.md) for final behavior and evidence.

The `processor/tools/` subtree was not accessed.
