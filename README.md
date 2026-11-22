# Internet Map

Internet Map is a high-performance web catalog and PublicWWW-style HTML source code intelligence engine for **12.25 million .org domains**, storing dataset URLs, HTML source code, forensic evidence, and technology signatures in a local SQLite catalog.

Python 3.10 or later and Node.js 20.9 or later are required. Runtime data is excluded from Git.

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

## Core Features

1. **⚡ PublicWWW Source Code Intelligence Engine**
   - Full-text search across raw HTML source code with FTS5 trigram indexing
   - Technology signature detection for 25+ frameworks (WordPress, WooCommerce, Shopify, Google Analytics GA4, Google Tag Manager, AdSense, Cloudflare Turnstile, React/Next.js, Vue/Nuxt, Tailwind CSS, Stripe, etc.)
   - Source code inspector modal with line numbers and syntax highlighting
   - Export matching search results as plain text or JSON

2. **🔍 Live Scanner & High-Throughput Catalog Crawler**
   - On-demand URL inspector: immediately fetch any website, extract technology signatures, and index into the intelligence database
   - Asynchronous crawler with configurable worker concurrency (50–200 workers) and batch limits
   - Live streaming activity feed showing real-time crawled pages, HTTP status, and detected technology
   - Safe process lifecycle management with zombie PID detection

3. **🌐 High-Speed Ingestion & 12.25M .org Domain Catalog**
   - High-throughput batched ingestion with SQLite WAL mode, `PRAGMA synchronous = NORMAL`, and `executemany` chunking (15x–30x speedup)
   - Zero-copy streaming zone importer (`org_zone.py`) directly from `.gz` without intermediate disk files
   - Hostname search and WordPress evidence filtering with responsive pagination

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
