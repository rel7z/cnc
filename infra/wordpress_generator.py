#!/usr/bin/env python3
"""Run the WordPress Generator feed and CNC dispatcher on the controller."""

import argparse
import concurrent.futures
import fcntl
import hashlib
import hmac
import json
import logging
import os
import shutil
import sqlite3
import threading
import time
import uuid
from contextlib import closing
from datetime import datetime
from pathlib import Path
from urllib.error import HTTPError
from urllib.parse import urlsplit
from urllib.request import Request, urlopen

import wordpress_bulk as bulk
import wordpress_ct as ct

ACTOR = "wordpress-generator"
LOG = logging.getLogger(ACTOR)
TRANSIENT = (OSError, EOFError, ValueError, KeyError, IndexError, sqlite3.Error)


def connect(path):
    return sqlite3.connect(path, timeout=10, uri=True)


def initialize(path):
    with closing(connect(path)) as db, db:
        db.executescript("""
            CREATE TABLE IF NOT EXISTS cursors (log TEXT PRIMARY KEY, position INTEGER NOT NULL, updated INTEGER NOT NULL);
            CREATE TABLE IF NOT EXISTS fresh (host TEXT PRIMARY KEY, url TEXT NOT NULL, seen INTEGER NOT NULL);
            CREATE TABLE IF NOT EXISTS active (slot INTEGER PRIMARY KEY CHECK(slot = 1), batch TEXT NOT NULL);
            CREATE TABLE IF NOT EXISTS retries (host TEXT PRIMARY KEY, url TEXT NOT NULL, due INTEGER NOT NULL, done INTEGER NOT NULL DEFAULT 0);
            CREATE TABLE IF NOT EXISTS deferred (host TEXT PRIMARY KEY, until INTEGER NOT NULL);
            CREATE TABLE IF NOT EXISTS stats (slot INTEGER PRIMARY KEY CHECK(slot = 1), verified INTEGER NOT NULL, rate REAL);
            INSERT OR IGNORE INTO stats VALUES (1,0,NULL);
            CREATE INDEX IF NOT EXISTS fresh_seen ON fresh(seen);
            CREATE TABLE IF NOT EXISTS merged_hosts (host TEXT PRIMARY KEY, url TEXT NOT NULL) WITHOUT ROWID;
        """)


def poll_log(state, log, catalog=None):
    ident, url, tiled = log
    size = ct.head(url, tiled)
    if size < 0:
        raise ValueError("negative CT tree size")
    with closing(connect(state)) as db:
        if catalog:
            db.execute("ATTACH DATABASE ? AS catalog", (catalog.resolve().as_uri() + "?mode=ro",))
        has_attempted = catalog and db.execute(
            "SELECT 1 FROM catalog.sqlite_schema WHERE name='attempted'",
        ).fetchone()
        saved = db.execute("SELECT position FROM cursors WHERE log=?", (ident,)).fetchone()
        position = saved[0] if saved else size
        if position > size:
            raise ValueError("CT log moved backwards")
        for _ in range(8):
            if position == size:
                with db:
                    db.execute("INSERT INTO cursors VALUES (?,?,?) ON CONFLICT(log) DO UPDATE SET updated=excluded.updated",
                               (ident, position, int(time.time())))
                break
            entries = ct.tiled_entries(url, position, size) if tiled else list(ct.dynamic_entries(url, position, min(size, position + 256)))
            if not entries:
                raise ValueError("CT page made no progress")
            position += len(entries)
            now = int(time.time())
            candidates = set()
            for entry in entries:
                for name in entry:
                    if "*" not in name and (candidate := bulk.homepage(name)):
                        candidates.add(candidate)
            with db:
                if catalog:
                    unattempted = "AND NOT EXISTS (SELECT 1 FROM catalog.attempted WHERE host=?1)" if has_attempted else ""
                    db.executemany(f"""
                        INSERT OR IGNORE INTO fresh
                        SELECT ?1,?2,?3 WHERE NOT EXISTS (SELECT 1 FROM catalog.candidates WHERE host=?1)
                        {unattempted}
                    """, ((host, homepage, now) for host, homepage in candidates))
                else:
                    db.executemany("INSERT OR IGNORE INTO fresh VALUES (?,?,?)",
                                   ((host, homepage, now) for host, homepage in candidates))
                db.execute("INSERT INTO cursors VALUES (?,?,?) ON CONFLICT(log) DO UPDATE SET position=excluded.position, updated=excluded.updated",
                           (ident, position, now))


def feed(state, catalog):
    while True:
        if (state.parent / "paused").exists():
            time.sleep(2)
            continue
        start = time.monotonic()
        try:
            with concurrent.futures.ThreadPoolExecutor(max_workers=6) as pool:
                for future in concurrent.futures.as_completed([pool.submit(poll_log, state, log, catalog) for log in ct.logs()]):
                    try:
                        future.result()
                    except TRANSIENT as error:
                        LOG.warning("CT log poll failed: %s", error)
        except TRANSIENT as error:
            LOG.warning("CT list poll failed: %s", error)
        time.sleep(max(1, 60 - (time.monotonic() - start)))


class CNC:
    def __init__(self, base, secret):
        endpoint = urlsplit(base)
        if endpoint.scheme not in ("http", "https") or not endpoint.hostname or endpoint.username or len(secret) < 32:
            raise ValueError("invalid CNC API or audit key")
        self.base = base.rstrip("/")
        self.secret = secret.encode()

    def call(self, path, payload=None):
        headers = {}
        body = None
        if payload is not None:
            body = json.dumps(payload).encode()
            stamp = str(time.time_ns() // 1_000_000)
            message = f"{ACTOR}\n{stamp}\nPOST\n{path}\n{hashlib.sha256(body).hexdigest()}".encode()
            headers = {"Content-Type": "application/json", "X-CNC-Actor": ACTOR, "X-CNC-Audit-Time": stamp,
                       "X-CNC-Audit-Signature": hmac.new(self.secret, message, "sha256").hexdigest()}
        with urlopen(Request(self.base + path, data=body, headers=headers), timeout=20) as response:
            return json.load(response)


def candidates(db, catalog, limit):
    db.execute("ATTACH DATABASE ? AS catalog", (catalog.resolve().as_uri() + "?mode=ro",))
    exists = db.execute("SELECT 1 FROM catalog.sqlite_schema WHERE name='attempted'").fetchone()
    filter_attempted = "NOT EXISTS (SELECT 1 FROM catalog.attempted a WHERE a.host=c.host)" if exists else "1"
    ready = "NOT EXISTS (SELECT 1 FROM deferred d WHERE d.host=c.host AND d.until>unixepoch())"
    source = db.execute(f"""
        SELECT c.url FROM fresh c WHERE {filter_attempted} AND {ready}
        AND NOT EXISTS (SELECT 1 FROM catalog.candidates known WHERE known.host=c.host)
        ORDER BY c.rowid LIMIT ?
    """, (limit,)).fetchall()
    if source:
        return source
    if any(row[1] == "first_seen" for row in db.execute("PRAGMA catalog.table_info(candidates)")):
        recent = time.strftime("%Y-%m-%d", time.gmtime(time.time() - 7 * 86400))
        source = db.execute(f"""
            SELECT c.url FROM catalog.candidates c WHERE {filter_attempted} AND {ready}
            AND (c.sources & 8)=0 AND c.first_seen>=? ORDER BY c.first_seen,c.host LIMIT ?
        """, (recent, limit)).fetchall()
        if source:
            return source
    retry = db.execute(f"SELECT c.url FROM retries c WHERE c.done=0 AND c.due<=unixepoch() AND {ready} "
                       "ORDER BY c.due,c.host LIMIT ?", (limit,)).fetchall()
    if retry:
        return retry
    for mask in (bulk.SOURCES["webgraph"] | bulk.SOURCES["reverse-ip"],
                 bulk.ALL_SOURCES):
        rows = db.execute(f"""
            SELECT c.url FROM catalog.candidates c WHERE {filter_attempted} AND {ready}
            AND (c.sources & 8)=0 AND (c.sources & ?) != 0 ORDER BY c.host LIMIT ?
        """, (mask, limit)).fetchall()
        if rows:
            return rows
    return []


def publish_merged(state, merged, sources):
    target = merged / "wordpress.txt"
    with closing(connect(state)) as db:
        with db:
            for path in (target, *sources):
                if not path.is_file():
                    continue
                with path.open(encoding="utf-8-sig") as stream:
                    db.executemany("INSERT OR IGNORE INTO merged_hosts VALUES (?,?)",
                                   (candidate for line in stream if (candidate := bulk.homepage(line))))
        temporary = merged / ".wordpress-generator.tmp"
        with temporary.open("w", encoding="ascii") as stream:
            for url, in db.execute("SELECT url FROM merged_hosts ORDER BY host"):
                stream.write(url + "\n")
            stream.flush()
            os.fsync(stream.fileno())
        temporary.replace(target)
        fd = os.open(merged, os.O_RDONLY)
        try:
            os.fsync(fd)
        finally:
            os.close(fd)


def archive_output(source, destination):
    temporary = destination.with_suffix(".tmp")
    with source.open("rb") as original, temporary.open("wb") as copy:
        shutil.copyfileobj(original, copy)
        copy.flush()
        os.fsync(copy.fileno())
    temporary.replace(destination)
    fd = os.open(destination.parent, os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)
    source.unlink()


def archive_legacy_outputs(args, state):
    completed = []
    archive = args.root / "archive"
    archive.mkdir(mode=0o700, exist_ok=True)
    for path in args.merged.glob("wordpress-fresh-enum_fresh_*.txt"):
        job = path.stem.removeprefix("wordpress-fresh-")
        snapshot = args.data / "enum-jobs" / job / "state.json"
        if not snapshot.is_file():
            continue
        if json.loads(snapshot.read_text())["job"]["status"] in ("completed", "completed_with_errors"):
            _, verified = bulk.completed_job_counts(snapshot)
            hosts = set(bulk.attempted_hosts(path))
            if len(hosts) == verified and hosts <= set(bulk.attempted_hosts(snapshot.parent / "input.txt")):
                completed.append((path, archive / path.name))
    if completed:
        publish_merged(state, args.merged, [source for source, _ in completed])
        for source, destination in completed:
            archive_output(source, destination)


def dispatch(api, args, state):
    with closing(connect(state)) as db:
        row = db.execute("SELECT batch FROM active WHERE slot=1").fetchone()
        if row:
            batch = row[0]
        else:
            if (args.root / "paused").exists():
                return
            jobs = api.call("/api/jobs") or []
            if any(j["status"] in ("pending", "queued", "running") for j in jobs):
                return
            workers = api.call("/api/workers") or []
            idle = sum(w.get("wordpress_bulk") and w.get("enum_checkpoint") and w.get("enum_stream_receipts")
                       and w["status"] == "online" and not w["current_load"] and not w.get("processor_attempts")
                       for w in workers)
            if not idle:
                return
            rows = candidates(db, args.catalog, min(args.batch_limit, 5000 * idle))
            if not rows:
                return
            batch = uuid.uuid4().hex
            with (args.root / (batch + ".txt")).open("x", encoding="ascii") as batch_file:
                batch_file.writelines(url + "\n" for url, in rows)
                batch_file.flush()
                os.fsync(batch_file.fileno())
            with db:
                db.execute("INSERT INTO active VALUES (1,?)", (batch,))
    path = args.root / (batch + ".txt")
    ident = "enum_fresh_" + batch
    try:
        job = api.call("/api/jobs/" + ident)
    except HTTPError as error:
        if error.code != 404:
            raise
        error.close()
        if (args.root / "paused").exists():
            return
        result = api.call("/api/tools/launch", {"tool_id": "wordpress-bulk", "fresh_batch_id": batch,
                          "input_file": str(path), "options": {"threads": args.threads, "timeout": 5,
                          "auto_concurrency": not getattr(args, "fixed_concurrency", False)}})
        if result["job_id"] != ident:
            raise ValueError("CNC server does not support stable fresh batch IDs")
        LOG.info("Submitted %s", ident)
        return
    if job["status"] not in ("completed", "completed_with_errors"):
        if job["status"] in ("failed", "cancelled"):
            if getattr(args, "once", False):
                raise RuntimeError(f"Canary {ident} ended as {job['status']}")
            with closing(connect(state)) as db, db:
                db.executemany("INSERT OR REPLACE INTO deferred VALUES (?,?)",
                               ((host, int(time.time()) + 86400) for host in bulk.attempted_hosts(path)))
                db.execute("DELETE FROM active WHERE slot=1 AND batch=?", (batch,))
            LOG.error("Batch %s ended as %s; its input is retained and deferred for one day", ident, job["status"])
        return
    tasks = api.call("/api/jobs/" + ident + "/tasks")
    snapshot = args.data / "enum-jobs" / ident / "input.txt"
    expected_name = f"wordpress-fresh-{ident}.txt"
    if job.get("output_file") != expected_name:
        raise ValueError("unexpected CNC result name")
    output = snapshot.parent / expected_name
    legacy = args.merged / expected_name
    archived = args.root / "archive" / expected_name
    if not output.is_file():
        output = archived if archived.is_file() else legacy
    metadata = args.root / (batch + ".json")
    metadata.write_text(json.dumps({"job": job, "tasks": [
        {"id": t["id"], "job_id": t["job_id"], "status": t["status"], "result": t["result"]} for t in tasks
    ]}), encoding="utf-8")
    processed, verified = bulk.completed_job_counts(metadata)
    if not verified:
        output = args.root / (batch + ".empty")
        output.touch(exist_ok=True)
    skipped = sum((t["result"].get("stderr") or "").splitlines().count("[WARN] Skipping invalid HTTP target") for t in tasks)
    with closing(connect(args.catalog)) as catalog:
        bulk.record_completed(catalog, path, output, processed, skipped, verified, snapshot, summarize=False)
    if verified:
        publish_merged(state, args.merged, [output])
        if output == legacy:
            archived.parent.mkdir(mode=0o700, exist_ok=True)
            archive_output(legacy, archived)
    retry_path = snapshot.parent / "wordpress-retry.txt"
    accepted_hosts = set(bulk.attempted_hosts(snapshot))
    retries = set()
    if retry_path.is_file():
        with retry_path.open(encoding="ascii") as stream:
            for line in stream:
                if (candidate := bulk.homepage(line)) and candidate[0] in accepted_hosts:
                    retries.add(candidate)
    with closing(connect(state)) as db, db:
        hosts = [(host,) for host in bulk.attempted_hosts(path)]
        db.executemany("DELETE FROM fresh WHERE host=?", hosts)
        db.executemany("UPDATE retries SET done=1 WHERE host=?", hosts)
        due = int(time.time()) + 86400
        db.executemany("INSERT OR IGNORE INTO retries (host,url,due) VALUES (?,?,?)",
                       ((host, url, due) for host, url in retries))
        rate = None
        if job.get("started_at") and job.get("completed_at"):
            elapsed = (datetime.fromisoformat(job["completed_at"]) - datetime.fromisoformat(job["started_at"])).total_seconds()
            if elapsed > 0:
                rate = round(processed / elapsed, 1)
        db.execute("UPDATE stats SET verified=verified+?, rate=? WHERE slot=1", (verified, rate))
        db.execute("DELETE FROM active WHERE slot=1 AND batch=?", (batch,))
    metadata.unlink(missing_ok=True)
    path.unlink(missing_ok=True)
    if not verified:
        output.unlink(missing_ok=True)
    LOG.info("Merged %s: %d verified of %d checked", ident, verified, processed)
    return True


def publish_status(root, state):
    now = int(time.time())
    with closing(connect(state)) as db:
        feed_at, = db.execute("SELECT max(updated) FROM cursors").fetchone()
        oldest, = db.execute("SELECT min(seen) FROM fresh").fetchone()
        verified, rate = db.execute("SELECT verified,rate FROM stats WHERE slot=1").fetchone()
        active = db.execute("SELECT batch FROM active WHERE slot=1").fetchone()
    status = {"updated_at": now, "feed_at": feed_at, "oldest_fresh_at": oldest,
              "merged_verified": verified, "hostnames_per_second": rate,
              "active_job": "enum_fresh_" + active[0] if active else None}
    temp = root / ".status.tmp"
    temp.write_text(json.dumps(status), encoding="utf-8")
    temp.replace(root / "status.json")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path("/opt/cnc/wordpress-generator"))
    parser.add_argument("--catalog", type=Path, default=Path("/opt/cnc/wordpress-generator/candidates.db"))
    parser.add_argument("--data", type=Path, default=Path("/opt/cnc/data"))
    parser.add_argument("--merged", type=Path, default=Path("/opt/cnc/merged"))
    parser.add_argument("--batch-limit", type=int, choices=range(1, 10001), metavar="1..10000", default=10000)
    parser.add_argument("--threads", type=int, choices=range(64, 257), metavar="64..256", default=64)
    parser.add_argument("--fixed-concurrency", action="store_true", help="Disable adaptive tuning for a benchmark control")
    parser.add_argument("--once", action="store_true", help="Reconcile a single batch and exit")
    args = parser.parse_args()
    logging.basicConfig(level=logging.INFO)
    api = CNC(os.environ["GO_API_URL"], os.environ["CNC_DASHBOARD_AUDIT_SECRET"])
    if not args.catalog.is_file():
        parser.error("existing candidate catalog is required")
    lock = (args.root / "generator.lock").open("a")
    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    state = args.root / "generator-state.db"
    initialize(state)
    archive_legacy_outputs(args, state)
    reader = threading.Thread(target=feed, args=(state, args.catalog), daemon=True)
    reader.start()
    while True:
        if not reader.is_alive():
            raise RuntimeError("CT reader stopped")
        try:
            completed = dispatch(api, args, state)
            publish_status(args.root, state)
            if completed and args.once:
                return
        except TRANSIENT as error:
            LOG.warning("Dispatch deferred: %s", error)
        time.sleep(15)


if __name__ == "__main__":
    main()
