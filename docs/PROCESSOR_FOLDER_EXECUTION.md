# Programs that read default files

A program does not need CNC imports or command-line arguments. Register its
entrypoint, its input filename, its supporting files, and the outputs to collect.

For example, the unrestricted simulation in `processor/examples/default-folder/`
reads `wordpress.txt` and a supporting `pass.txt`. It makes no network requests.
Its manifest is:

```json
{
  "input": "wordpress.txt",
  "files": ["pass.txt"],
  "scripts": {
    "scanner": {
      "path": "rr",
      "runtime": "binary",
      "outputs": ["results.txt"]
    }
  }
}
```

Paths in the manifest are relative to the manifest folder. `rr` is the program
filename, rather than the folder containing it. `files` declares the supporting
files copied into every job. The input is supplied by CNC, so do not add
`wordpress.txt` to `files`. An extensionless Linux executable needs
`"runtime": "binary"`.

The main configuration connects an incoming filename to that manifest:

```json
{
  "routes": {"wordpress.txt": ["folder-demo"]},
  "programs": {"folder-demo": "examples/default-folder/tool.json"}
}
```

For an owner-managed tool folder, the same mapping uses its tool ID and manifest
path, for example `"bf": "tools/bf/config.json"` with
`"wordpress.txt": ["bf"]`. That manifest must first be valid JSON and declare
the correct entrypoint and all required supporting files. Changing only the
incoming filename does not register the program.

## What happens on each job

1. The processor captures complete lines from `wordpress.txt` in its configured
   merged directory into SQLite.
2. It creates chunks of at most 1,000 lines or 1 MiB, without splitting a line.
3. The shared queue assigns each chunk when a compatible worker has capacity.
4. The worker copies the pinned entrypoint and declared supporting files into
   a new job folder. It writes that chunk as `wordpress.txt` beside `rr`.
5. It runs `./rr` without arguments, with that folder as the working directory.
6. The listener publishes completed lines from the declared `results.txt`.
   The server merges them into `folder-demo--scanner__results.txt`; the existing
   Drive uploader handles that merged output.

The source folder and shared bundle cache are not job working directories. Each
chunk has its own input and supporting-file copies. A supporting file such as
`pass.txt` stays unchanged unless the program edits its own job copy. Only the
input file is divided into chunks.

Putting `wordpress.txt` directly beside the original program does not trigger
the processor. The automatic trigger is the registered incoming filename in
the merged directory. The processor then places a copy beside the program in
each isolated job.

## Simulation verification

Run from `processor/`:

```sh
go test ./internal/distributed -run '^TestDefaultFolderTenThousandLines$' -count=1 -v
```

The test creates its own folders, SQLite database, API, input, supporting file,
and six simulated workers. It uses the actual capturer, scheduler, bundle
installer, worker runner, supervisor, publication receipts, and merge path.
It checks 10,000 unique lines, ten jobs, use of all six workers, one active job
per worker, publication before execution finishes, and unchanged source files
and bundle caches. Repeated capture must not create additional jobs.

Python runs on macOS and Linux. The compiled-binary variant runs on Linux; it
compiles the harmless `scanner.go` fixture, or uses the fixture executable
specified by `CNC_FOLDER_FIXTURE_BINARY`. No private programs are used.

The simulation does not upload to Google Drive or change production registration.
Its merged output verifies the processor side of the existing uploader boundary.
