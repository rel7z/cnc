# Internet Map

Internet Map is a high-performance web catalog and PublicWWW-style HTML source code intelligence engine for **12.25 million .org domains**, storing dataset URLs, HTML source code, forensic evidence, and technology signatures in a local SQLite catalog.

Python 3.10 or later and Node.js 20.9 or later are required. Runtime data is excluded from Git.

## Core Features

1. **⚡ PublicWWW Source Code Intelligence Engine**
   - Full-text search across raw HTML source code with FTS5 trigram indexing (`tokenize='trigram'`)
   - Technology signature detection for 25+ frameworks (WordPress, WooCommerce, Shopify, Google Analytics GA4, Google Tag Manager, AdSense, Cloudflare Turnstile, React/Next.js, Vue/Nuxt, Tailwind CSS, Stripe, etc.)
   - PublicWWW dark code cards with line numbers, context lines, and keyword highlight marks
   - Source code inspector modal with complete raw HTML and copy-to-clipboard functionality
   - High-speed query execution (<20ms response time on 220,000+ indexed pages)

2. **🔍 Live Scanner & High-Throughput Catalog Crawler**
   - On-demand URL inspector: immediately fetch any website, extract technology signatures, and index into the intelligence database
   - Asynchronous crawler with configurable worker concurrency (50–1,000 workers) and batch limits
   - Live streaming activity feed showing real-time crawled pages, HTTP status, and detected technology
   - Safe process lifecycle management with zombie PID detection

3. **🛡️ Systemd Resource Governor & Cgroups Throttling**
   - Hardware-governed crawler profiles: **Eco** (150 workers), **Balanced** (350 workers), **High Speed** (600 workers), **Turbo** (1,000 workers)
   - Real-time Linux `/proc/{pid}/statm` RSS telemetry and CPU quota monitoring (caps crawler to 1.8 cores, leaving >2.2 cores reserved for OS/CNC)
   - Dynamic profile hot-switching with instant crawler reload without killing long-running jobs

4. **🌐 High-Speed Ingestion & 12.25M .org Domain Catalog**
   - High-throughput batched ingestion with SQLite WAL mode, `PRAGMA synchronous = NORMAL`, and `executemany` chunking (15x–30x speedup)
   - Zero-copy streaming zone importer (`org_zone.py`) directly from `.gz` without intermediate disk files
   - Hostname search and WordPress evidence filtering with responsive pagination across 498,000+ pages

## Rapid Development Workflow

Speed up development with preconfigured Makefile targets:

```bash
# 1. Install frontend dependencies
cd web && npm ci && cd ..

# 2. Seed development database with realistic domains & HTML intelligence in 0.5s
make seed

# 3. Launch local development environment with Fast Refresh & hot module reload
make dev
```

Open http://127.0.0.1:3501. When running with `make dev` (or `python3 run.py --dev`), Next.js runs in development mode with Turbopack and hot reload so code edits in `web/` appear immediately without manual builds!

### Developer Commands

- `make dev`: Launch Next.js dev server with Turbopack & Python catalog API
- `make start`: Launch production-optimized build server & API
- `make build`: Build Next.js production frontend assets
- `make test`: Run complete pytest suite (`test_api`, `test_catalog`, `test_intel`, `test_scanner`, `test_cli`)
- `make seed`: Seed development catalog with realistic domains & PublicWWW intelligence
- `make clean`: Clean temporary caches and build artifacts

## Run the application in production

From this project directory:

```bash
cd web
npm ci
npm run build
cd ..
python3 run.py
```

Open http://127.0.0.1:3501. The Next.js, React and TypeScript application
communicates with the Python catalog API through server-side route handlers.
The launcher generates the private API key automatically for both processes.
The browser receives catalog data. Closing the launcher stops both services.

The catalog groups URLs by scheme, hostname, and port; paths and queries are discarded.
Duplicate records within an import merge their WordPress evidence. All commands
default to `data/catalog.db` beside the Python source, regardless of working directory.
For `main.py`, place an explicit `--database` option before the subcommand.

The catalog starts empty. Import a dataset and click Refresh to display it.

## Import a local .org zone snapshot

```bash
python3 org_zone.py /path/to/org.zone.gz --source czds-org-2026-11-22
```

The importer reads .org NS delegation owners from plain ASCII or gzip snapshots.
It handles absolute names, relative names, inherited owners, $ORIGIN, $TTL, and
multiline records. Unsupported directives and incomplete multiline records abort
the import. Zone data provides DNS delegations. Each imported URL starts with
https:// as a catalog convention, and has no live HTTP verification attached.

## Import supplied datasets

TXT input contains one URL or hostname per line. CSV has a url column.
JSONL contains objects with a url field and an optional html string.

```bash
python3 main.py import sites.jsonl --format jsonl --source supplied-html
python3 main.py stats
python3 main.py search --host example.org
python3 main.py search --wordpress
python3 main.py observations https://example.org/
python3 main.py export --wordpress > wordpress-from-dataset.txt
```

WordPress classification records generator meta tags and HTML resource references.
It describes evidence in supplied HTML. Import time records when CNC received the
dataset. It does not establish the date that the source page was retrieved.

## Import existing CNC output through REST

Set CNC_API_KEY privately in the process environment.

```bash
python3 cnc_import.py --base-url https://YOUR_CNC_HOST --remote-file /opt/cnc/merged/wordpress.txt
```

HTTPS and localhost HTTP tunnels are accepted. Redirects are rejected to keep the
Bearer key at the configured origin. The importer accepts at most 256 MiB per
response and uses temporary local staging before the database transaction.

The adapter currently retrieves existing output. Distributed job submission,
job monitoring, and live WordPress verification require further implementation.

## Classify supplied HTML on a worker

```bash
python3 worker.py --input html-records.jsonl --output observations.jsonl
python3 main.py import observations.jsonl --format observations --source worker-output
```

The worker reads existing HTML. Observation imports validate the evidence labels
produced by the worker. Source labels should identify the dataset and producing
job. Transport authentication and job scheduling are separate integrations.

## Dataset storage on DigitalOcean

Store original dataset files in /opt/internet-map/datasets/ on the controller.
The SQLite catalog is /opt/internet-map/data/catalog.db.
Both paths are persistent and separate from the web build directory.
Dataset files and runtime databases are excluded from Git.

For a .org snapshot already placed on the server:

```bash
cd /opt/internet-map
python3 org_zone.py /opt/internet-map/datasets/org.zone.gz --source czds-org --database /opt/internet-map/data/catalog.db
```

Refresh the application after import to read the updated local catalog.

## Remote Production Access & Direct Ingestion

### SSH Port Forwarding Tunnel
To access the DigitalOcean production UI and API from your local workstation:

```bash
./tunnel.sh
```

This maps:
- `http://127.0.0.1:3501` -> Internet Map Production UI
- `http://127.0.0.1:3500` -> Internet Map Production REST API
- `http://127.0.0.1:8119` -> CNC Cluster Dashboard

### Feeding Local Datasets to Server
Upload and ingest any local list or dataset directly into the production SQLite database:

```bash
python3 feed_server.py /path/to/my_domains.txt --source "custom-feed-20261122"
```


## CNC discovery ownership and API coordination

Internet Map owns the discovery code in `discovery/`, its persistent state in
`/opt/internet-map/data/discovery/`, and the main catalog in `data/catalog.db`.
The checkpointed `catalog_feed.py` admits existing catalog sites into the same
unique five-million candidate pool while respecting checked hosts and the pause
boundary (collection continues, scanner dispatch stops).
CNC remains the worker executor: the migrated algorithm submits signed jobs to
its private API and reconciles their results. The old CNC generator service is
disabled. Its original directory is a compatibility symlink to the migrated
state, preserving pool history and checkpoints rather than resetting them.

Authenticated endpoints (same Bearer key as the catalog API):

- `GET /api/v1/discovery/status`: ownership, pause state, pool and source metrics.
- `GET /api/v1/discovery/workers`: CNC worker availability, excluding SSH secrets.
- `POST /api/v1/discovery/control`: `{"action":"pause"}` or `{"action":"resume"}`;
  also accepts the CNC dashboard's `enabled`, `sources`, and `commoncrawl_mode` settings.

`infra/install-discovery.py` performs a same-volume state migration only while
scanning is paused and CNC jobs are drained. It installs one discovery service,
a catalog reconciliation timer (30 seconds), and a backup timer (six hours).
Activation failures restore the original state path. Legacy worker lease requests
return no fresh work after cutover; in-flight result submissions remain accepted.
CNC's authenticated dashboard forwards generator controls to this API over loopback.
Keep `service.env`, CNC audit keys, credentials, runtime state and datasets out of Git.

`maintenance.py sync` imports generator classifications with provenance and deduplicates
repeated imports. It does not invent HTML or generator-tag evidence.

`maintenance.py backup` uses SQLite's online backup API with a pinned read transaction,
checks integrity, and publishes immutable compressed parts plus a SHA-256 manifest
into CNC's Drive watch directory. Catalog, candidate pool and discovery state are
snapshotted separately; catalog reconciliation is idempotent after recovery.
The live SQLite/WAL files are never uploaded directly. Historical backup versions
are retained; monitor Drive quota as the catalog grows.

To restore, download a manifest and all its named parts from Drive into one folder.
From the Internet Map source directory, call:

```python
from backup import restore_snapshot
restore_snapshot("/path/to/backup.manifest.json.gz", "/path/to/restored-catalog.db")
```

Restore requires a missing destination and verifies every part, the assembled
checksum and SQLite integrity. Stop the relevant service before deliberately
replacing a live database with a verified restored copy.

Production verification (2026-11-22): authenticated bidirectional coordination and an eight-worker harmless roundtrip passed. All three database snapshots were downloaded from Drive and restored with exact hashes and SQLite integrity checks. See `docs/cnc-integration-verification-2026-11-22.json`.


## Single-file recovery backups

`maintenance.py backup` publishes one signed `.tar.gz` containing all three SQLite databases, non-secret discovery settings, leased input files, and application source. A new archive appears in the Drive inbox only after its construction finishes. The scheduled backup runs every six hours; upload transport chunks remain one Drive file.

Keep the private recovery key in CNC Manager's application-support directory and on the controller outside the archive. Never commit it or upload it with the data. The CNC release's trusted recovery helper authenticates the archive before restoring to a missing directory, checks all file hashes and SQLite integrity, and keeps discovery paused. Existing installations are preserved.

The dashboard starts backup and local-integrity checks in the background and polls their status. Its upload indicator uses the configured uploader state and exact filename/checksum; it is an upload acknowledgement, not a fresh cloud read. `verify_drive_snapshot.py /opt/internet-map/data/backup-status.json <new-directory>` verifies the cloud copy by actually downloading it, with no local-copy shortcut or fallback recovery key. Never verify by overwriting a live database.
