# Temporary worker tasks

Every shell task (spread and broadcast) now runs in a fresh directory under
the worker's `data_dir/shell-runs/`. The change applies to every tool using
the common shell runner; it is not specific to dork2.

- `{input}` points to the current task's downloaded chunk.
- The command's working directory and `HOME` are the task directory.
- `TMPDIR`, `TMP`, and `TEMP` point to a task-local `tmp/` directory.
- Relative output paths, including `~/results.txt`, are watched inside that
  task directory. Parent traversal, absolute declared output paths, input/output
  conflicts, and output paths under shared `tools/` are rejected.
- Installed tools remain available through `./tools/` and `PATH`.
- Declared file output is the source of live result records, avoiding duplicate
  delivery when a tool prints the same results to stdout. If no output file is
  created, captured stdout is delivered at exit as a compatibility fallback.
- Output is checked every 200 ms. The runner completes its final read and stream
  flush before removing the workspace, including after failure or cancellation.
- Cancellation and timeouts also cover input downloads.

Existing files in a worker's original home or working directory are neither
copied into tasks nor watched for results. They are not deleted by deployment.
Thus an empty new run cannot replay an old `x.txt` or `results.txt`.

This is job-file isolation, not an operating-system sandbox. Tools and dependencies
are shared executable references. Custom commands using explicit absolute paths,
or programs writing beside their installed executable, can still access shared
locations. Use task-relative input and output paths for automatic isolation.
Programs requiring script-directory file isolation should use the processor's
folder manifest runner, which already gives each job an isolated bundle copy.

Processor and enum jobs retain their separate existing isolation and publication
flows. Shell job scheduling and resource limits are unchanged. This update reduces
duplicate result traffic and result-watcher latency; it does not increase Apify
account capacity or a tool's internal request concurrency.

Verification uses dummy shell programs, concurrent identical filenames, stale
files, live output, fast exit, failure, and cancellation. No private tool content
is required.
