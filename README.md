# Internet Map

Internet Map stores dataset URLs and source observations in a local SQLite catalog.
Python 3.10 or later and Node.js 20.9 or later are required. Runtime data is excluded from Git.

## Run the application

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
