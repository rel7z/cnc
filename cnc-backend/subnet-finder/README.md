# subnet-finder

IPv4-only domain → prefix lookup → bounded address expansion → ICMP probes.

Existing commands remain compatible:

```sh
./tools/subnet-finder -l input.txt -out results.txt -c 100 -timeout 2s
```

Input is streamed rather than loaded into memory. Explicit IPv4 CIDRs are also
accepted and bypass DNS/RIPE lookup. IPv6 DNS results and IPv6 prefixes are
ignored. IPv4 ranges larger than 4,096 addresses are skipped by default;
`-max-addresses` can select a limit from 1 to 65,536. IPv4 network/broadcast
addresses are excluded except for /31 and /32 ranges.

Queues have at most 256 entries. DNS uses at most 16 workers; RIPE uses at most
four. Probe concurrency honors `-c` within a memory-based ceiling. DNS and ICMP
operations respect cancellation and timeouts. Completed results are written
immediately to the output file and stdout; output failures stop the pipeline.

Exact deduplication uses a disposable SQLite index in `$TMPDIR`. Its page cache
is limited to 8 MiB and memory mapping is disabled. Disk requirements grow with
the number of unique addresses/prefixes. The index is removed on normal exit;
CNC's isolated workspace cleanup also removes it after forced termination.
The index is never reused after a crash. Input and output must be different files.

`-memory-mb` defaults to a 128 MiB Go runtime memory target. This is a garbage
collection target, not an absolute RSS guarantee; the worker's cgroup still
provides the final safety limit. Global in-memory IP maps and full-range address
slices are no longer used.

Build and verify:

```sh
go test -race -short ./...
go test -run TestDiskDedupScalesWithoutGrowingHeap -v
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o subnet-finder .
```

Tests use injected DNS/lookup/probe fixtures and loopback ranges only. They never
scan public targets. The large fixture checks 300,000 exact keys and retained heap.

SQLite cache settings: https://www.sqlite.org/pragma.html#pragma_cache_size
