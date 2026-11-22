# Internet Map

Internet Map stores dataset URLs and source observations in a local SQLite catalog.
Python 3.10 or later is required. Runtime data is excluded from Git.

## Import a local .org zone snapshot

```bash
python3 internet-map/org_zone.py /path/to/org.zone.gz --source czds-org-2026-11-22
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
python3 internet-map/main.py import sites.jsonl --format jsonl --source supplied-html
python3 internet-map/main.py stats
python3 internet-map/main.py search --host example.org
python3 internet-map/main.py search --wordpress
python3 internet-map/main.py observations https://example.org/
python3 internet-map/main.py export --wordpress > wordpress-from-dataset.txt
```

WordPress classification records generator meta tags and HTML resource references.
It describes evidence in supplied HTML. Import time records when CNC received the
dataset. It does not establish the date that the source page was retrieved.

## Import existing CNC output through REST

Set CNC_API_KEY privately in the process environment.

```bash
python3 internet-map/cnc_import.py --base-url https://YOUR_CNC_HOST --remote-file /opt/cnc/merged/wordpress.txt
```

HTTPS and localhost HTTP tunnels are accepted. Redirects are rejected to keep the
Bearer key at the configured origin. The importer accepts at most 256 MiB per
response and uses temporary local staging before the database transaction.

The adapter currently retrieves existing output. Distributed job submission,
job monitoring, and live WordPress verification require further implementation.
