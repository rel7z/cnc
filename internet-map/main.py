#!/usr/bin/env python3
"""Internet Map dataset catalog."""
import argparse
import csv
import json
import sys
from pathlib import Path
import sqlite3
from urllib.parse import urlsplit
from datetime import datetime, timezone


def normalize(value):
    value = value.strip()
    if not value or any(c.isspace() for c in value):
        raise ValueError("Invalid URL")
    parsed = urlsplit(value if "://" in value else "https://" + value)
    if parsed.scheme not in ("http", "https") or not parsed.hostname or parsed.username or parsed.password:
        raise ValueError("Expected a public web URL without credentials")
    host = parsed.hostname.rstrip(".").encode("idna").decode().lower()
    if not host or ":" in host:
        raise ValueError("Expected a hostname")
    port = parsed.port
    authority = host
    if port and port != (443 if parsed.scheme == "https" else 80):
        authority += ":" + str(port)
    return host, parsed.scheme + "://" + authority + "/"


def records(path, kind):
    with path.open(encoding="utf-8-sig") as stream:
        if kind == "csv":
            yield from csv.DictReader(stream)
        elif kind == "jsonl":
            for line in stream:
                if line.strip():
                    record = json.loads(line)
                    if not isinstance(record, dict):
                        raise ValueError("JSONL records must be objects")
                    yield record
        else:
            for line in stream:
                if line.strip():
                    yield {"url": line.strip()}


def wordpress_evidence(html):
    text = html.lower()
    evidence = []
    if "/wp-content/" in text:
        evidence.append("wp-content reference")
    if "/wp-includes/" in text:
        evidence.append("wp-includes reference")
    return evidence


def connect(path):
    path.parent.mkdir(parents=True, exist_ok=True)
    db = sqlite3.connect(path)
    db.execute("PRAGMA foreign_keys=ON")
    db.executescript("""
        CREATE TABLE IF NOT EXISTS imports (
            id INTEGER PRIMARY KEY, source TEXT NOT NULL,
            imported_at TEXT NOT NULL, accepted INTEGER NOT NULL DEFAULT 0,
            rejected INTEGER NOT NULL DEFAULT 0
        );
        CREATE TABLE IF NOT EXISTS sites (
            url TEXT PRIMARY KEY, host TEXT NOT NULL
        );
        CREATE TABLE IF NOT EXISTS observations (
            import_id INTEGER NOT NULL REFERENCES imports(id),
            url TEXT NOT NULL REFERENCES sites(url),
            evidence TEXT NOT NULL,
            PRIMARY KEY(import_id, url)
        );
        CREATE INDEX IF NOT EXISTS observations_url ON observations(url);
    """)
    return db


def ingest(db, args):
    now = datetime.now(timezone.utc).isoformat()
    accepted = rejected = 0
    with db:
        ident = db.execute("INSERT INTO imports(source,imported_at) VALUES (?,?)",
                           (args.source, now)).lastrowid
        for record in records(args.input, args.format):
            try:
                value = record.get("url")
                if not isinstance(value, str):
                    raise ValueError("Missing URL")
                host, url = normalize(value)
            except (ValueError, UnicodeError):
                rejected += 1
                continue
            html = record.get("html", "")
            if not isinstance(html, str):
                raise ValueError("HTML must be a string")
            evidence = wordpress_evidence(html)
            db.execute("INSERT OR IGNORE INTO sites VALUES (?,?)", (url, host))
            result = db.execute("INSERT OR IGNORE INTO observations VALUES (?,?,?)",
                                (ident, url, json.dumps(evidence)))
            accepted += result.rowcount
        db.execute("UPDATE imports SET accepted=?,rejected=? WHERE id=?",
                   (accepted, rejected, ident))
    print(f"Imported {accepted} unique URLs from {args.source}. Rejected {rejected} rows.")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--database", type=Path, default=Path("internet-map/data/catalog.db"))
    commands = parser.add_subparsers(dest="command", required=True)
    load = commands.add_parser("import")
    load.add_argument("input", type=Path)
    load.add_argument("--format", choices=("txt", "csv", "jsonl"), required=True)
    load.add_argument("--source", required=True)
    search = commands.add_parser("search")
    search.add_argument("--host", default="")
    search.add_argument("--wordpress", action="store_true")
    search.add_argument("--limit", type=int, default=100)
    export = commands.add_parser("export")
    export.add_argument("--wordpress", action="store_true")
    inspect = commands.add_parser("observations")
    inspect.add_argument("url")
    commands.add_parser("stats")
    args = parser.parse_args()
    db = connect(args.database)
    try:
        if args.command == "import":
            ingest(db, args)
        elif args.command == "stats":
            print("Unique URLs", db.execute("SELECT count(*) FROM sites").fetchone()[0])
            print("Imports", db.execute("SELECT count(*) FROM imports").fetchone()[0])
        elif args.command == "observations":
            _, url = normalize(args.url)
            for source, observed, evidence in db.execute("""
                SELECT i.source,i.imported_at,o.evidence FROM observations o
                JOIN imports i ON i.id=o.import_id WHERE o.url=?
                ORDER BY i.id
            """, (url,)):
                print(json.dumps({"url": url, "source": source, "imported_at": observed,
                                  "wordpress_evidence": json.loads(evidence)}))
        elif args.command == "export":
            query = "SELECT s.url FROM sites s"
            if args.wordpress:
                query += " WHERE EXISTS(SELECT 1 FROM observations o WHERE o.url=s.url AND o.evidence!='[]')"
            for url, in db.execute(query + " ORDER BY s.url"):
                sys.stdout.write(url + "\n")
        else:
            if not 1 <= args.limit <= 10000:
                parser.error("Limit must be between 1 and 10000")
            sql = "SELECT s.url FROM sites s WHERE instr(s.host,?)>0"
            if args.wordpress:
                sql += " AND EXISTS(SELECT 1 FROM observations o WHERE o.url=s.url AND o.evidence!='[]')"
            for row in db.execute(sql + " ORDER BY s.url LIMIT ?", (args.host.lower(), args.limit)):
                print(row[0])
    finally:
        db.close()


if __name__ == "__main__":
    main()
