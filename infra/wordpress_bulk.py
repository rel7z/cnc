#!/usr/bin/env python3
"""Merge public WordPress candidates from HTTP Archive and CNC reverse IP exports."""

import argparse
import csv
import gzip
import hashlib
import io
import ipaddress
import json
import re
import sqlite3
import sys
from datetime import date, datetime, timezone
from tempfile import TemporaryDirectory
from contextlib import closing, nullcontext
from itertools import chain, islice
from pathlib import Path
from urllib.request import Request, urlopen
from urllib.parse import urljoin, urlsplit

SOURCES = {"archive": 1, "reverse-ip": 2, "ranked": 4, "verified": 8, "webgraph": 16, "commoncrawl": 32}
ALL_SOURCES = sum(SOURCES.values())
HOST_LABEL = re.compile(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?")
HOST_SUFFIX = re.compile(r"(?:[a-z]{2,63}|xn--[a-z0-9-]+)")
MAPTHENET_DATA = "https://www.mapthenet.org/data/"
COMMONCRAWL_BLOG = "https://commoncrawl.org/blog"
COMMONCRAWL_DATA = "https://data.commoncrawl.org/"


def open_mapthenet(filename, timeout):
    return urlopen(Request(MAPTHENET_DATA + filename, headers={"User-Agent": "WordPressGenerator/1.0"}), timeout=timeout)


def homepage(value):
    value = value.strip()
    if not value or len(value) > 2048 or any(c.isspace() or ord(c) < 32 for c in value):
        return None
    if "://" not in value:
        value = "https://" + value
    try:
        url = urlsplit(value)
        if url.scheme not in ("http", "https") or not url.hostname or url.username is not None:
            return None
        if url.port not in (None, 443 if url.scheme == "https" else 80):
            return None
        host = url.hostname.rstrip(".").encode("idna").decode("ascii").lower()
        if len(host) > 253 or "." not in host or not all(HOST_LABEL.fullmatch(part) for part in host.split(".")):
            return None
    except (ValueError, UnicodeError):
        return None
    # A DNS hostname must end in a letter-bearing label. Reject IP literals
    # and legacy numeric address forms before they ever reach the verifier.
    if not HOST_SUFFIX.fullmatch(host.rsplit(".", 1)[-1]):
        return None
    return host.removeprefix("www."), f"{url.scheme}://{host}/"


def input_urls(stream, file_format, column, status=None):
    if file_format == "cc-vertices":
        for number, line in enumerate(stream, 1):
            fields = line.rstrip("\r\n").split("\t")
            if len(fields) != 2 or not fields[0].isascii() or not fields[0].isdigit():
                raise ValueError(f"invalid Common Crawl host vertex at row {number}")
            yield ".".join(reversed(fields[1].split(".")))
        return
    if file_format == "csv":
        reader = csv.DictReader(stream)
        if column not in (reader.fieldnames or ()):
            raise ValueError(f"CSV requires a {column} column")
        if status is not None and "status" not in reader.fieldnames:
            raise ValueError("CSV status filter requires a status column")
        for row in reader:
            if status is None or row["status"] == status:
                yield row[column] or ""
        return
    if file_format == "tranco":
        for row in csv.reader(stream):
            if len(row) != 2 or not row[0].isdigit():
                raise ValueError("Tranco CSV requires rows of rank,domain")
            yield row[1]
        return
    for line in stream:
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        address, separator, domains = line.partition(": ")
        if separator:
            try:
                ipaddress.ip_address(address)
            except ValueError:
                pass
            else:
                # The optional THC IP-map format contains comma-separated domains.
                yield from domains.split(",")
                continue
        yield line


def import_urls(connection, path, source, file_format, column="url", status=None, allowed_hosts=None, summarize=True):
    counts = {"read": 0, "invalid": 0}
    if not connection.in_transaction:
        connection.execute("BEGIN IMMEDIATE")
    connection.execute("""
        CREATE TABLE IF NOT EXISTS candidates (
            host TEXT PRIMARY KEY, url TEXT NOT NULL, sources INTEGER NOT NULL, first_seen TEXT
        ) WITHOUT ROWID
    """)
    if not has_first_seen(connection):
        connection.execute("ALTER TABLE candidates ADD COLUMN first_seen TEXT")
    connection.execute("""
        CREATE INDEX IF NOT EXISTS candidates_first_seen ON candidates(first_seen, host)
        WHERE first_seen IS NOT NULL
    """)
    first_seen = datetime.now(timezone.utc).date().isoformat()

    verified_hosts = set() if allowed_hosts is not None else None

    def rows(stream):
        for value in input_urls(stream, file_format, column, status):
            counts["read"] += 1
            candidate = homepage(value)
            if allowed_hosts is not None and (candidate is None or candidate[0] not in allowed_hosts):
                raise ValueError(f"verified result row {counts['read']} does not match a batch hostname")
            if candidate is None:
                counts["invalid"] += 1
                continue
            if verified_hosts is not None:
                verified_hosts.add(candidate[0])
            yield (*candidate, SOURCES[source], first_seen)

    archive, reverse, ranked, verified = (SOURCES[key] for key in ("archive", "reverse-ip", "ranked", "verified"))
    open_file = gzip.open if Path(path).suffix.lower() == ".gz" else open
    with open_file(path, "rt", encoding="utf-8-sig", newline="") as stream:
        connection.executemany(f"""
            INSERT INTO candidates (host, url, sources, first_seen) VALUES (?, ?, ?, ?)
            ON CONFLICT(host) DO UPDATE SET
              url = CASE
                WHEN excluded.sources = {verified} AND (candidates.sources & {verified}) = 0 THEN excluded.url
                WHEN excluded.sources = {archive} AND (candidates.sources & {archive | verified}) = 0 THEN excluded.url
                ELSE candidates.url END,
              sources = candidates.sources | excluded.sources
            WHERE (candidates.sources & excluded.sources) = 0
        """, rows(stream))
    if verified_hosts is not None:
        counts["verified_hosts"] = len(verified_hosts)
    if summarize:
        counts["total_unique"] = connection.execute("SELECT count(*) FROM candidates").fetchone()[0]
        counts["reverse_ip_only"] = connection.execute(
            "SELECT count(*) FROM candidates WHERE (sources & ?) = ?",
            (archive | reverse | ranked | SOURCES["webgraph"] | SOURCES["commoncrawl"], reverse),
        ).fetchone()[0]
    return counts


def has_first_seen(connection):
    return any(row[1] == "first_seen" for row in connection.execute("PRAGMA main.table_info(candidates)"))


def mapthenet_manifest():
    with open_mapthenet("manifest.json", 30) as response:
        manifest = json.loads(response.read(64 * 1024))
    if not isinstance(manifest, dict) or not isinstance(manifest.get("generated"), str):
        raise ValueError("MapTheNet manifest is missing a valid snapshot date")
    generated = date.fromisoformat(manifest["generated"]).isoformat()
    files = manifest.get("files")
    size = files.get("domains.csv.gz") if isinstance(files, dict) else None
    if type(size) is not int or size <= 0:
        raise ValueError("MapTheNet manifest is missing a valid domains.csv.gz size")
    return generated, size


def sync_mapthenet(connection, local_snapshot=None):
    generated, size = mapthenet_manifest()
    connection.execute("""
        CREATE TABLE IF NOT EXISTS mapthenet_snapshots (
            generated TEXT NOT NULL, bytes INTEGER NOT NULL, sha256 TEXT NOT NULL,
            PRIMARY KEY (generated, bytes)
        ) WITHOUT ROWID
    """)
    previous = connection.execute(
        "SELECT sha256 FROM mapthenet_snapshots WHERE generated = ? AND bytes = ?", (generated, size),
    ).fetchone()
    if previous:
        return {"status": "unchanged", "snapshot": generated, "sha256": previous[0]}
    with TemporaryDirectory(prefix="wordpress-mapthenet-") as staging:
        path = Path(local_snapshot) if local_snapshot else Path(staging) / "domains.csv.gz"
        digest = hashlib.sha256()
        received = 0
        if local_snapshot:
            source = path.open("rb")
        else:
            source = open_mapthenet("domains.csv.gz", 60)
        with source, (path.open("wb") if not local_snapshot else nullcontext()) as output:
            for chunk in iter(lambda: source.read(1024 * 1024), b""):
                received += len(chunk)
                if received > size:
                    raise ValueError("MapTheNet snapshot is larger than its manifest")
                digest.update(chunk)
                if output is not None:
                    output.write(chunk)
        if received != size or mapthenet_manifest() != (generated, size):
            raise ValueError("MapTheNet snapshot did not match a stable manifest")
        checksum = digest.hexdigest()
        with connection:
            receipt = connection.execute(
                "INSERT OR IGNORE INTO mapthenet_snapshots VALUES (?, ?, ?)", (generated, size, checksum),
            )
            if not receipt.rowcount:
                return {"status": "unchanged", "snapshot": generated}
            counts = import_urls(connection, path, "webgraph", "csv", "domain", "ok")
        return {"status": "imported", "snapshot": generated, "sha256": checksum, **counts}


def sync_candidate_files(connection, paths, source):
    table, file_format = {
        "reverse-ip": ("reverse_ip_exports", "txt"),
        "commoncrawl": ("commoncrawl_shards", "cc-vertices"),
    }[source]
    connection.execute(f"""
        CREATE TABLE IF NOT EXISTS {table} (
            sha256 TEXT PRIMARY KEY
        ) WITHOUT ROWID
    """)
    results = []
    for path in paths:
        digest = hashlib.sha256()
        with TemporaryDirectory(prefix=f"wordpress-{source}-") as staging:
            snapshot = Path(staging) / path.name
            with path.open("rb") as input_file, snapshot.open("wb") as output:
                for chunk in iter(lambda: input_file.read(1024 * 1024), b""):
                    digest.update(chunk)
                    output.write(chunk)
            checksum = digest.hexdigest()
            with connection:
                receipt = connection.execute(f"INSERT OR IGNORE INTO {table} VALUES (?)", (checksum,))
                result = {"file": str(path), "sha256": checksum, "status": "unchanged"}
                if receipt.rowcount:
                    result.update(status="imported", **import_urls(connection, snapshot, source, file_format))
            results.append(result)
    return results


def completed_inbox(directory, suffixes):
    if directory is None:
        return []
    return sorted(path for path in directory.iterdir()
                  if not path.name.startswith(".") and path.name.endswith(suffixes)
                  and not path.is_symlink() and path.is_file())


def pull_commoncrawl(connection):
    """Import one immutable host-vertex shard from the latest published graph."""
    def fetch(url, limit):
        with urlopen(Request(url, headers={"User-Agent": "CNC WordPress Generator"}), timeout=30) as response:
            data = response.read(limit + 1)
        if len(data) > limit:
            raise ValueError("Common Crawl discovery response is too large")
        return data

    blog = fetch(COMMONCRAWL_BLOG, 4 << 20).decode()
    releases = re.findall(r'href="([^"]*/blog/host--and-domain-level-web-graphs-[a-z0-9-]+)"', blog)
    if not releases:
        raise ValueError("Common Crawl release link is unavailable")
    release = urljoin(COMMONCRAWL_BLOG, releases[0])
    if urlsplit(release).hostname not in ("commoncrawl.org", "www.commoncrawl.org", "blog.commoncrawl.org"):
        raise ValueError("unexpected Common Crawl release host")
    page = fetch(release, 2 << 20).decode()
    match = re.search(r'https://data\.commoncrawl\.org/(projects/hyperlinkgraph/[a-z0-9-]+/host/[a-z0-9-]+-host-vertices\.paths\.gz)', page)
    if not match:
        raise ValueError("Common Crawl host shard list is unavailable")
    prefix = match[1].rsplit("/", 1)[0] + "/vertices/"
    compressed = fetch(COMMONCRAWL_DATA + match[1], 64 << 10)
    with gzip.GzipFile(fileobj=io.BytesIO(compressed)) as paths:
        listing = paths.read(64 << 10).decode().splitlines()
    connection.execute("CREATE TABLE IF NOT EXISTS commoncrawl_public_shards (path TEXT PRIMARY KEY) WITHOUT ROWID")
    for shard in listing:
        if not shard.startswith(prefix) or not re.fullmatch(r"[a-zA-Z0-9_.-]+\.gz", shard.removeprefix(prefix)):
            raise ValueError("invalid Common Crawl shard path")
        if connection.execute("SELECT 1 FROM commoncrawl_public_shards WHERE path=?", (shard,)).fetchone():
            continue
        with TemporaryDirectory(prefix="wordpress-public-graph-") as staging:
            path = Path(staging) / "vertices.txt.gz"
            with urlopen(COMMONCRAWL_DATA + shard, timeout=60) as response, path.open("wb") as output:
                size = 0
                for block in iter(lambda: response.read(1024 * 1024), b""):
                    size += len(block)
                    if size > 128 << 20:
                        raise ValueError("Common Crawl shard exceeds the transfer limit")
                    output.write(block)
            with connection:
                receipt = connection.execute("INSERT OR IGNORE INTO commoncrawl_public_shards VALUES (?)", (shard,))
                if receipt.rowcount:
                    counts = import_urls(connection, path, "commoncrawl", "cc-vertices", summarize=False)
                    return {"status": "imported", "path": shard, **counts}
    return {"status": "unchanged"}


def attempted_hosts(path):
    with path.open(encoding="utf-8-sig") as stream:
        for line in input_urls(stream, "txt", "url"):
            candidate = homepage(line)
            if candidate is not None:
                yield candidate[0]


def insert_attempted(connection, hosts):
    connection.execute("CREATE TABLE IF NOT EXISTS attempted (host TEXT PRIMARY KEY) WITHOUT ROWID")
    receipt = connection.executemany("INSERT OR IGNORE INTO attempted VALUES (?)", ((host,) for host in hosts))
    return receipt.rowcount


def record_attempted(connection, paths):
    with connection:
        return insert_attempted(connection, chain.from_iterable(map(attempted_hosts, paths)))


def record_completed(connection, batch, results, processed, skipped, verified, controller_input=None, summarize=True):
    hosts = set(attempted_hosts(batch))
    accepted = set(attempted_hosts(controller_input)) if controller_input else hosts
    if not accepted <= hosts:
        raise ValueError("controller input contains a hostname outside the original batch")
    if not accepted or processed < 0 or skipped < 0 or processed + skipped != len(accepted):
        raise ValueError("scanner counts do not cover the complete batch")
    if not 0 <= verified <= processed:
        raise ValueError("invalid verified-host count")
    with connection:
        counts = import_urls(connection, results, "verified", "txt", allowed_hosts=accepted, summarize=summarize)
        if counts["verified_hosts"] != verified:
            raise ValueError(f"Merged contains {counts['verified_hosts']} verified hosts, expected {verified}")
        counts["new_attempted_hosts"] = insert_attempted(connection, sorted(hosts))
        if controller_input:
            counts["controller_rejected"] = len(hosts) - len(accepted)
    return counts


def completed_job_counts(path):
    state = json.loads(path.read_text(encoding="utf-8"))
    job, tasks = state.get("job", {}), state.get("tasks", [])
    terminal = ("completed", "completed_with_errors")
    if job.get("name") != "Tool: wordpress-bulk" or job.get("status") not in terminal:
        raise ValueError("expected a completed WordPress bulk job")
    if not tasks or len(tasks) != job.get("total_tasks") or len({t.get("id") for t in tasks}) != len(tasks):
        raise ValueError("job state does not account for all shards")
    processed = verified = 0
    for task in tasks:
        if task.get("job_id") != job.get("id") or task.get("status") not in terminal:
            raise ValueError("job state contains an incomplete or unrelated shard")
        result = task.get("result") or {}
        stderr = result.get("stderr") or ""
        targets = re.findall(r"^\[\*\] targets=(\d+) errors=", stderr, re.MULTILINE)
        outcomes = re.findall(r"^\[\*\] homepage outcomes(.*)$", stderr, re.MULTILINE)
        if result.get("exit_code") not in (0, 2) or len(targets) != 1 or len(outcomes) != 1:
            raise ValueError("shard is missing its complete homepage summary")
        found = re.search(r"(?:^| )verified=(\d+)(?: |$)", outcomes[0])
        processed += int(targets[0])
        verified += int(found[1]) if found else 0
    return processed, verified


def export_batches(connection, source, destination, exclude_files, batch_size, additional_sources=(),
                   max_batches=None, excluded_sources=(), discovered_since=None):
    destination = Path(destination).resolve()
    if destination.exists():
        raise ValueError(f"batch directory already exists: {destination}")
    connection.execute("CREATE TEMP TABLE excluded (host TEXT PRIMARY KEY) WITHOUT ROWID")
    for path in exclude_files:
        with open(path, encoding="utf-8-sig") as stream:
            hosts = (homepage(line) for line in input_urls(stream, "txt", "url"))
            connection.executemany(
                "INSERT OR IGNORE INTO excluded VALUES (?)",
                ((candidate[0],) for candidate in hosts if candidate is not None),
            )
    sources = list(dict.fromkeys((source, *additional_sources)))
    has_history = connection.execute(
        "SELECT 1 FROM main.sqlite_schema WHERE type = 'table' AND name = 'attempted'",
    ).fetchone()
    history_filter = (
        "AND NOT EXISTS (SELECT 1 FROM main.attempted WHERE attempted.host = candidates.host)"
        if has_history else ""
    )
    discovery_filter = ""
    discovery_params = ()
    if discovered_since is not None:
        if has_first_seen(connection):
            discovery_filter = "AND first_seen >= ?"
            discovery_params = (discovered_since.isoformat(),)
        else:
            discovery_filter = "AND 0"
    urls = connection.execute(f"""
        SELECT url FROM candidates
        WHERE (sources & ?) != 0 AND (sources & ?) = 0
        AND NOT EXISTS (SELECT 1 FROM excluded WHERE excluded.host = candidates.host)
        {history_filter}
        {discovery_filter}
        ORDER BY host
        LIMIT ?
    """, (
        sum(SOURCES[name] for name in sources),
        sum(SOURCES[name] for name in set(excluded_sources)),
        *discovery_params,
        -1 if max_batches is None else batch_size * max_batches,
    ))
    manifest = {"source": source, "total_urls": 0, "batches": []}
    if additional_sources:
        manifest["sources"] = sources
    if excluded_sources:
        manifest["excluded_sources"] = sorted(set(excluded_sources))
    if discovered_since is not None:
        manifest["discovered_since_utc"] = discovered_since.isoformat()
    with TemporaryDirectory(prefix=".wordpress-batches-", dir=destination.parent) as staging:
        output = Path(staging) / "output"
        output.mkdir()
        while (first := urls.fetchone()) is not None:
            filename = f"wordpress-part-{len(manifest['batches']) + 1:04d}.txt"
            digest = hashlib.sha256()
            size = 0
            with (output / filename).open("wb") as stream:
                for url, in chain((first,), islice(urls, batch_size - 1)):
                    line = (url + "\n").encode("ascii")
                    stream.write(line)
                    digest.update(line)
                    size += 1
            manifest["total_urls"] += size
            manifest["batches"].append({"file": filename, "urls": size, "sha256": digest.hexdigest()})
        (output / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
        output.rename(destination)
    return manifest["total_urls"], len(manifest["batches"])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("database")
    commands = parser.add_subparsers(dest="command", required=True)
    importer = commands.add_parser("import")
    importer.add_argument("source", choices=SOURCES)
    importer.add_argument("file")
    importer.add_argument("--format", choices=("csv", "txt", "tranco", "cc-vertices"), required=True)
    importer.add_argument("--column", default="url", help="CSV column containing URLs or domains")
    importer.add_argument("--status", help="Only import CSV rows with this crawl status")
    sync = commands.add_parser("sync-mapthenet")
    sync.add_argument("--local-snapshot", type=Path, help="Bootstrap from an existing copy of the current gzip snapshot")
    reverse = commands.add_parser("sync-reverse-ip")
    reverse.add_argument("files", type=Path, nargs="+", help="Completed CNC reverse-IP TXT exports")
    commoncrawl = commands.add_parser("sync-commoncrawl")
    commoncrawl.add_argument("files", type=Path, nargs="+", help="Downloaded Common Crawl host vertices shards")
    refresh = commands.add_parser("refresh", help="Sync MapTheNet and any supplied candidate files")
    refresh.add_argument("--reverse-ip", type=Path, action="append", default=[])
    refresh.add_argument("--commoncrawl", type=Path, action="append", default=[])
    refresh.add_argument("--reverse-ip-inbox", type=Path, help="Directory of completed reverse-IP TXT exports")
    refresh.add_argument("--commoncrawl-inbox", type=Path, help="Directory of completed host vertices shards")
    refresh.add_argument("--pull-commoncrawl", action="store_true", help="Import one shard from the latest public host graph")
    refresh.add_argument("--local-snapshot", type=Path, help="Bootstrap from an existing copy of the current gzip snapshot")
    attempted = commands.add_parser("record-attempted")
    attempted.add_argument("files", type=Path, nargs="+", help="Input TXT files of fully attempted batches")
    completed = commands.add_parser("record-completed", help="Import verified results and record a complete batch")
    completed.add_argument("batch", type=Path)
    completed.add_argument("results", type=Path)
    completed.add_argument("--processed", type=int, help="Targets accounted for by the scanner")
    completed.add_argument("--skipped", type=int, default=0, help="Input hosts rejected before the homepage check")
    completed.add_argument("--verified", type=int, help="Verified host count from the scanner summary")
    completed.add_argument("--job-state", type=Path, help="Read counts from a terminal CNC enum state.json")
    completed.add_argument("--controller-input", type=Path, help="Controller snapshot for automatic input rejection counts")
    exporter = commands.add_parser("export")
    exporter.add_argument("--source", choices=SOURCES)
    exporter.add_argument("--exclude-source", choices=SOURCES, action="append", default=[])
    batches = commands.add_parser("batches")
    batches.add_argument("source", choices=SOURCES)
    batches.add_argument("directory")
    batches.add_argument("--include-source", choices=SOURCES, action="append", default=[])
    batches.add_argument("--exclude-source", choices=SOURCES, action="append", default=[])
    batches.add_argument("--exclude-file", action="append", default=[], help="TXT of previously attempted URLs")
    batches.add_argument("--batch-size", type=int, default=250000)
    batches.add_argument("--max-batches", type=int, help="Prepare only the next N batches")
    batches.add_argument("--discovered-since", type=date.fromisoformat, metavar="YYYY-MM-DD",
                         help="Only hosts first added to the database on or after this UTC date")
    args = parser.parse_args()
    if args.command == "import" and args.status is not None and args.format != "csv":
        parser.error("--status requires CSV input")
    if args.command == "batches" and not 1 <= args.batch_size <= 250000:
        parser.error("--batch-size must be between 1 and 250000")
    if args.command == "batches" and args.max_batches is not None and not 1 <= args.max_batches <= 1000000:
        parser.error("--max-batches must be between 1 and 1000000")
    if args.command == "record-completed":
        manual = (args.processed is not None, args.verified is not None)
        if (args.job_state and any(manual)) or (not args.job_state and not all(manual)):
            parser.error("provide --job-state or both --processed and --verified")
    try:
        read_only = args.command in ("export", "batches")
        database = Path(args.database).resolve().as_uri() + "?mode=ro" if read_only else args.database
        with closing(sqlite3.connect(database, timeout=30, uri=read_only)) as connection:
            if args.command == "import":
                with connection:
                    counts = import_urls(connection, args.file, args.source, args.format, args.column, args.status)
                print(json.dumps(counts))
            elif args.command == "sync-mapthenet":
                print(json.dumps(sync_mapthenet(connection, args.local_snapshot)))
            elif args.command in ("sync-reverse-ip", "sync-commoncrawl"):
                print(json.dumps(sync_candidate_files(connection, args.files, args.command.removeprefix("sync-"))))
            elif args.command == "refresh":
                # Preserve completed local imports if the remote source is unavailable.
                reverse_files = args.reverse_ip + completed_inbox(args.reverse_ip_inbox, (".txt", ".txt.gz"))
                crawl_files = args.commoncrawl + completed_inbox(
                    args.commoncrawl_inbox, (".txt", ".txt.gz", ".tsv", ".tsv.gz"),
                )
                local = sync_candidate_files(connection, reverse_files, "reverse-ip") if reverse_files else []
                crawl = sync_candidate_files(connection, crawl_files, "commoncrawl") if crawl_files else []
                results = {"reverse_ip": local, "commoncrawl": crawl}
                failures = []
                for name, operation in (
                    ("mapthenet", lambda: sync_mapthenet(connection, args.local_snapshot)),
                    ("commoncrawl_public", lambda: pull_commoncrawl(connection) if args.pull_commoncrawl else None),
                ):
                    try:
                        results[name] = operation()
                    except (OSError, EOFError, ValueError, sqlite3.Error) as error:
                        failures.append(name)
                        results[name] = {"status": "failed", "error": str(error)}
                print(json.dumps(results))
                if failures:
                    parser.exit(1, "refresh failed for " + ", ".join(failures) + "\n")
            elif args.command == "record-attempted":
                print(json.dumps({"new_attempted_hosts": record_attempted(connection, args.files)}))
            elif args.command == "record-completed":
                processed, verified = (
                    completed_job_counts(args.job_state) if args.job_state else (args.processed, args.verified)
                )
                print(json.dumps(record_completed(
                    connection, args.batch, args.results, processed, args.skipped, verified, args.controller_input,
                )))
            elif args.command == "batches":
                total, count = export_batches(
                    connection, args.source, args.directory, args.exclude_file, args.batch_size, args.include_source,
                    args.max_batches, args.exclude_source, args.discovered_since,
                )
                print(json.dumps({"urls": total, "batches": count}))
            else:
                include = SOURCES.get(args.source, ALL_SOURCES)
                exclude = sum(SOURCES[source] for source in set(args.exclude_source))
                for url, in connection.execute(
                    "SELECT url FROM candidates WHERE (sources & ?) != 0 AND (sources & ?) = 0 ORDER BY host",
                    (include, exclude),
                ):
                    print(url)
    except (OSError, EOFError, ValueError, csv.Error, sqlite3.Error) as error:
        parser.exit(1, f"wordpress_bulk: {error}\n")


if __name__ == "__main__":
    main()
