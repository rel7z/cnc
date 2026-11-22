#!/usr/bin/env python3
"""Run the WordPress Generator feed and CNC dispatcher on the controller."""

import argparse
import concurrent.futures
import csv
import fcntl
import hashlib
import hmac
import ipaddress
import io
import json
import logging
import os
import random
import re
import shutil
import socket
import sqlite3
import threading
import time
import uuid
from contextlib import closing
from datetime import datetime, timedelta, timezone
from email.utils import parsedate_to_datetime
from http.client import IncompleteRead
from pathlib import Path
from urllib.error import HTTPError
from urllib.parse import urlsplit
from urllib.request import Request, urlopen

import wordpress_bulk as bulk
import wordpress_ct as ct
import wordpress_output as exports
import wordpress_restock as restock
import wordpress_reverse as reverse_workers
import wordpress_sources as source_policy
import wordpress_index as cc_index

ACTOR = "wordpress-generator"
SHARD_TARGETS = 10_000
MIN_SHARD_TARGETS = 10_000
MAX_CANDIDATE_PAGE_ROWS = 80_000
SHARDS_PER_WORKER = 2
MAX_THREADS_PER_SHARD = 128
LOG = logging.getLogger(ACTOR)
APIFY_SEARCH = "https://api.apify.com/v2/actors/nFJndFXA5zjCTuudP/run-sync-get-dataset-items?timeout=100"
REVERSE_IP_ENDPOINT = "https://ip.thc.org/api/v1/download"
REVERSE_IP_DNSRIFT_ENDPOINT = "https://api.dnsrift.net/reverse-ip-lookup"
REVERSE_IP_LIMIT = 50_000
REVERSE_CACHE_SECONDS = 2 * 60 * 60
MAX_REVERSE_CACHE_HOSTS = 10_000_000
POOL_MIN = 5_000_000
POOL_MAX = 5_000_000
POOL_LOW = 1_000_000
POOL_PAGE = 50_000
POOL_FAST_PAGE = 100_000
REVERSE_TARGET_SHARE = 0.8
CT_TARGET_SHARE = 0.1
AUX_TARGET_SHARE = 0.1
REVERSE_IP_RESPONSE_LIMIT = 32 << 20
TRANSIENT = (OSError, EOFError, ValueError, KeyError, IndexError, sqlite3.Error)
PRIORITY_CATALOG_SOURCES = bulk.SOURCES["archive"] | bulk.SOURCES["ranked"] | bulk.SOURCES["verified"]


def connect(path):
    return sqlite3.connect(path, timeout=10, uri=True)


def priority_sources():
    verified = bulk.SOURCES["verified"]
    return [(bulk.SOURCES[name] | verified, bulk.SOURCES[name]) for name in ("archive", "ranked")]


def initialize_catalog(path):
    with closing(connect(path)) as db, db:
        # Imports can hold a write transaction for an entire source shard.
        # Keep the dispatcher and feed readers on their committed snapshot.
        db.execute("PRAGMA journal_mode=WAL")
        if (path.parent/"commoncrawl-mode.json").exists():db.execute("DROP TRIGGER IF EXISTS commoncrawl_candidate_insert")
        else:bulk.enable_fresh_queue(db)
        for mask, source in priority_sources():
            db.execute(f"CREATE INDEX IF NOT EXISTS generator_source_{source} ON candidates(host) "
                       f"WHERE (sources & {mask}) = {source}")
        verified = bulk.SOURCES["verified"]
        db.execute(f"CREATE INDEX IF NOT EXISTS generator_previous_wordpress ON candidates(host) "
                   f"WHERE (sources & {verified}) != 0")
        reverse = bulk.SOURCES["reverse-ip"]
        db.execute(f"CREATE INDEX IF NOT EXISTS generator_mix_{reverse} ON candidates(host) WHERE (sources & {reverse}) != 0")
        random_txt = bulk.SOURCES["random-txt"]
        db.execute(f"CREATE INDEX IF NOT EXISTS generator_mix_{random_txt} ON candidates(host) WHERE (sources & {random_txt}) != 0")


def initialize(path):
    with closing(connect(path)) as db, db:
        db.execute("PRAGMA journal_mode=WAL")
        pool_exists = db.execute("SELECT 1 FROM sqlite_schema WHERE type='table' AND name='pool_policy'").fetchone()
        schedule_exists = db.execute(
            "SELECT 1 FROM sqlite_schema WHERE type='table' AND name='fresh_schedule'"
        ).fetchone()
        db.executescript("""
            BEGIN;
            CREATE TABLE IF NOT EXISTS cursors (log TEXT PRIMARY KEY, position INTEGER NOT NULL, updated INTEGER NOT NULL);
            CREATE TABLE IF NOT EXISTS fresh (host TEXT PRIMARY KEY, url TEXT NOT NULL, seen INTEGER NOT NULL);
            CREATE TABLE IF NOT EXISTS active (slot INTEGER PRIMARY KEY CHECK(slot = 1), batch TEXT NOT NULL);
            CREATE TABLE IF NOT EXISTS retries (host TEXT PRIMARY KEY, url TEXT NOT NULL, due INTEGER NOT NULL, done INTEGER NOT NULL DEFAULT 0);
            CREATE TABLE IF NOT EXISTS deferred (host TEXT PRIMARY KEY, until INTEGER NOT NULL);
            CREATE TABLE IF NOT EXISTS stats (slot INTEGER PRIMARY KEY CHECK(slot = 1), verified INTEGER NOT NULL, rate REAL);
            INSERT OR IGNORE INTO stats VALUES (1,0,NULL);
            CREATE INDEX IF NOT EXISTS fresh_seen ON fresh(seen);
            CREATE TABLE IF NOT EXISTS fresh_schedule (
                host TEXT PRIMARY KEY, ready_at INTEGER NOT NULL
            ) WITHOUT ROWID;
            CREATE INDEX IF NOT EXISTS fresh_schedule_due ON fresh_schedule(ready_at);
            CREATE TRIGGER IF NOT EXISTS fresh_schedule_insert AFTER INSERT ON fresh BEGIN
                INSERT OR REPLACE INTO fresh_schedule VALUES (
                    NEW.host, coalesce((SELECT until FROM deferred WHERE host=NEW.host),0)
                );
            END;
            CREATE TRIGGER IF NOT EXISTS fresh_schedule_delete AFTER DELETE ON fresh BEGIN
                DELETE FROM fresh_schedule WHERE host=OLD.host;
            END;
            CREATE TRIGGER IF NOT EXISTS fresh_schedule_defer AFTER INSERT ON deferred BEGIN
                UPDATE fresh_schedule SET ready_at=NEW.until WHERE host=NEW.host;
            END;
            CREATE TRIGGER IF NOT EXISTS fresh_schedule_undefer AFTER DELETE ON deferred BEGIN
                UPDATE fresh_schedule SET ready_at=0 WHERE host=OLD.host;
            END;
            CREATE TABLE IF NOT EXISTS merged_hosts (host TEXT PRIMARY KEY, url TEXT NOT NULL) WITHOUT ROWID;
            CREATE TABLE IF NOT EXISTS generator_checked (host TEXT PRIMARY KEY) WITHOUT ROWID;
            CREATE TABLE IF NOT EXISTS discovery_cursor (slot INTEGER PRIMARY KEY CHECK(slot = 1), device INTEGER NOT NULL, inode INTEGER NOT NULL, offset INTEGER NOT NULL);
            CREATE TABLE IF NOT EXISTS discovery_candidates (host TEXT PRIMARY KEY, url TEXT NOT NULL) WITHOUT ROWID;
            CREATE TABLE IF NOT EXISTS shard_leases (batch TEXT PRIMARY KEY, launched INTEGER NOT NULL DEFAULT 0);
            CREATE TABLE IF NOT EXISTS shard_hosts (
                host TEXT PRIMARY KEY, batch TEXT NOT NULL, source TEXT NOT NULL
            ) WITHOUT ROWID;
            CREATE INDEX IF NOT EXISTS shard_hosts_batch ON shard_hosts(batch);
            CREATE TABLE IF NOT EXISTS ready_candidates (
                host TEXT PRIMARY KEY, url TEXT NOT NULL, source TEXT NOT NULL
            ) WITHOUT ROWID;
            CREATE INDEX IF NOT EXISTS ready_candidates_source ON ready_candidates(source,host);
            CREATE TABLE IF NOT EXISTS source_hold(host TEXT PRIMARY KEY,url TEXT NOT NULL,source TEXT NOT NULL) WITHOUT ROWID;
            CREATE TABLE IF NOT EXISTS pool_policy (
                slot INTEGER PRIMARY KEY CHECK(slot=1), available INTEGER NOT NULL,
                started INTEGER NOT NULL DEFAULT 0, refilling INTEGER NOT NULL DEFAULT 1,
                target INTEGER NOT NULL
            );
            INSERT OR IGNORE INTO pool_policy(slot,available,target)
                SELECT 1,count(*),5000000 FROM ready_candidates;
            CREATE TRIGGER IF NOT EXISTS pool_insert AFTER INSERT ON ready_candidates BEGIN
                UPDATE pool_policy SET available=available+1 WHERE slot=1;
            END;
            CREATE TRIGGER IF NOT EXISTS pool_delete AFTER DELETE ON ready_candidates BEGIN
                UPDATE pool_policy SET available=available-1 WHERE slot=1;
            END;
            CREATE TABLE IF NOT EXISTS source_yield (
                bucket INTEGER NOT NULL, source TEXT NOT NULL, checked INTEGER NOT NULL, verified INTEGER NOT NULL,
                PRIMARY KEY (bucket, source)
            ) WITHOUT ROWID;
            CREATE TABLE IF NOT EXISTS dork_queries (query_key TEXT PRIMARY KEY, requested INTEGER NOT NULL);
            CREATE TABLE IF NOT EXISTS dork_candidates (
                host TEXT PRIMARY KEY, url TEXT NOT NULL, reverse_queued INTEGER NOT NULL DEFAULT 0
            ) WITHOUT ROWID;
            CREATE TABLE IF NOT EXISTS reverse_input_seeds(host TEXT PRIMARY KEY,source TEXT NOT NULL,due INTEGER NOT NULL);
            CREATE INDEX IF NOT EXISTS reverse_input_seeds_due ON reverse_input_seeds(due,host);
            CREATE TABLE IF NOT EXISTS reverse_ip_origins(ip TEXT PRIMARY KEY,host TEXT NOT NULL,source TEXT NOT NULL);
            INSERT OR IGNORE INTO reverse_input_seeds SELECT host,source,0 FROM ready_candidates
                WHERE source NOT IN ('reverse-ip','reverse-seed','retry') LIMIT 100000;
            CREATE TABLE IF NOT EXISTS reverse_seeds (host TEXT PRIMARY KEY, due INTEGER NOT NULL);
            CREATE INDEX IF NOT EXISTS reverse_seeds_due ON reverse_seeds(due,host);
            CREATE TABLE IF NOT EXISTS reverse_rotation (
                slot INTEGER PRIMARY KEY CHECK(slot=1), provider TEXT NOT NULL
            );
            CREATE TABLE IF NOT EXISTS reverse_lookups (ip TEXT PRIMARY KEY, looked_up INTEGER NOT NULL);
            CREATE TABLE IF NOT EXISTS reverse_candidates (host TEXT PRIMARY KEY, url TEXT NOT NULL) WITHOUT ROWID;
            CREATE TABLE IF NOT EXISTS reverse_cache_timer (
                slot INTEGER PRIMARY KEY CHECK(slot=1), expires_at INTEGER NOT NULL
            );
            CREATE TABLE IF NOT EXISTS publications (batch TEXT PRIMARY KEY, published INTEGER NOT NULL, urls INTEGER NOT NULL);
            CREATE INDEX IF NOT EXISTS publications_time ON publications(published);
            CREATE TABLE IF NOT EXISTS merged_file (
                slot INTEGER PRIMARY KEY CHECK(slot=1),
                device INTEGER NOT NULL, inode INTEGER NOT NULL, size INTEGER NOT NULL, modified INTEGER NOT NULL
            );
            CREATE TABLE IF NOT EXISTS concurrency_trials (
                batch TEXT PRIMARY KEY, threads INTEGER NOT NULL, automatic INTEGER NOT NULL,
                status TEXT NOT NULL DEFAULT 'pending', elapsed REAL, checked INTEGER, published INTEGER,
                timeouts INTEGER, limited INTEGER, archive_checked INTEGER, worker TEXT
            );
            CREATE TABLE IF NOT EXISTS source_cursors (source TEXT PRIMARY KEY, host TEXT NOT NULL);
        """)
        if not schedule_exists:
            db.execute("""
                INSERT OR IGNORE INTO fresh_schedule
                SELECT f.host,coalesce(d.until,0) FROM fresh f LEFT JOIN deferred d USING(host)
            """)
        if not any(row[1] == "reverse_queued" for row in db.execute("PRAGMA table_info(dork_candidates)")):
            db.execute("ALTER TABLE dork_candidates ADD COLUMN reverse_queued INTEGER NOT NULL DEFAULT 0")
        if not pool_exists:
            db.execute("""
                DELETE FROM ready_candidates AS r
                WHERE EXISTS (SELECT 1 FROM generator_checked g WHERE g.host=r.host)
                   OR EXISTS (SELECT 1 FROM merged_hosts m WHERE m.host=r.host)
                   OR EXISTS (SELECT 1 FROM shard_hosts s WHERE s.host=r.host)
                   OR EXISTS (SELECT 1 FROM deferred d WHERE d.host=r.host AND d.until>unixepoch())
            """)
        # Preserve the refill cycle across restarts: start below the low
        # watermark, then keep going until the target has been reached.
        db.execute("""
            UPDATE pool_policy SET target=?,refilling=CASE
                WHEN available>=? THEN 0
                WHEN available<? OR started=0 THEN 1
                ELSE refilling END
            WHERE slot=1
        """, (POOL_MIN, POOL_MIN, POOL_LOW))
        queue_dork_seeds(db, int(time.time()))


def queue_dork_seeds(db, now):
    db.execute("""
        INSERT INTO reverse_seeds(host,due)
        SELECT host,? FROM dork_candidates WHERE reverse_queued=0
        ON CONFLICT(host) DO UPDATE SET due=min(due,excluded.due)
    """, (now,))
    db.execute("UPDATE dork_candidates SET reverse_queued=1 WHERE reverse_queued=0")


def store_dork_seeds(db, urls):
    with db:
        db.executemany("INSERT OR IGNORE INTO dork_candidates(host,url) VALUES (?,?)",
                       (candidate for url in urls if isinstance(url, str) and (candidate := bulk.homepage(url))))
        queue_dork_seeds(db, int(time.time()))


def collect_dork_seeds(state, path, previous):
    try:
        stream = path.open("rb")
    except FileNotFoundError:
        return None
    with stream:
        info = os.fstat(stream.fileno())
        signature = (info.st_dev, info.st_ino, info.st_size, info.st_mtime_ns)
        if signature == previous:
            return previous
        data = stream.read((1 << 20) + 1)
    if len(data) > 1 << 20:
        LOG.warning("Dork seed list exceeds 1 MiB")
        return signature
    with closing(connect(state)) as db:
        store_dork_seeds(db, bulk.input_urls(data.decode("utf-8-sig").splitlines(), "txt", "url"))
    return signature


def collect_discovery_results(state, path):
    """Treat the standalone runner's JSONL as candidates, not verified results."""
    with path.open("rb") as stream, closing(connect(state)) as db, db:
        info = os.fstat(stream.fileno())
        cursor = db.execute("SELECT device,inode,offset FROM discovery_cursor WHERE slot=1").fetchone()
        if cursor and cursor[:2] == (info.st_dev, info.st_ino) and cursor[2] <= info.st_size:
            stream.seek(cursor[2])
        candidates = set()
        for _ in range(8192):
            position = stream.tell()
            line = stream.readline()
            if not line.endswith(b"\n"):
                stream.seek(position)
                break
            try:
                row = json.loads(line)
                candidate = bulk.homepage(row["url"]) if isinstance(row["url"], str) else None
            except (ValueError, KeyError, TypeError):
                continue
            if candidate:
                candidates.add(candidate)
        db.executemany("INSERT OR IGNORE INTO discovery_candidates VALUES (?,?)", candidates)
        db.execute("INSERT OR REPLACE INTO discovery_cursor VALUES (1,?,?,?)",
                   (info.st_dev, info.st_ino, stream.tell()))


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
                    unattempted = "AND NOT EXISTS (SELECT 1 FROM catalog.attempted WHERE host=:host)" if has_attempted else ""
                    db.executemany(f"""
                        INSERT OR IGNORE INTO fresh
                        SELECT :host,:homepage,:seen WHERE 1 {unattempted}
                    """, ({"host": host, "homepage": homepage, "seen": now} for host, homepage in candidates))
                else:
                    db.executemany("INSERT OR IGNORE INTO fresh VALUES (?,?,?)",
                                   ((host, homepage, now) for host, homepage in candidates))
                db.execute("INSERT INTO cursors VALUES (?,?,?) ON CONFLICT(log) DO UPDATE SET position=excluded.position, updated=excluded.updated",
                           (ident, position, now))


class CTPoller:
    def __init__(self):
        self.cooldowns = {}
        self.lock = threading.Lock()

    def poll(self, state, log, catalog):
        origin = urlsplit(log[1]).netloc.lower()
        with self.lock:
            if self.cooldowns.get(origin, 0) > time.monotonic():
                return
        try:
            poll_log(state, log, catalog)
        except HTTPError as error:
            if error.code != 429:
                raise
            with error:
                delay = 300
                retry_after = error.headers.get("Retry-After", "") if error.headers else ""
                try:
                    delay = int(retry_after) if retry_after.isdecimal() else (
                        parsedate_to_datetime(retry_after).timestamp() - time.time()
                    )
                except (ValueError, OverflowError):
                    pass
            now = time.monotonic()
            with self.lock:
                previous = self.cooldowns.get(origin, 0)
                self.cooldowns[origin] = max(previous, now + max(60, delay))
                if previous <= now:
                    LOG.warning("CT origin %s rate limited, cooling down for %.0fs", origin, max(60, delay))


def feed(state, catalog):
    poller = CTPoller()
    while True:
        if not source_policy.read(state.parent)["ct"]:
            time.sleep(2)
            continue
        with closing(connect(state)) as db:
            refilling = pool_snapshot(db)[2]
        if not refilling:
            time.sleep(5)
            continue
        start = time.monotonic()
        try:
            with concurrent.futures.ThreadPoolExecutor(max_workers=6) as pool:
                futures = [pool.submit(poller.poll, state, log, catalog) for log in ct.logs()]
                for future in concurrent.futures.as_completed(futures):
                    try:
                        future.result()
                    except TRANSIENT as error:
                        LOG.warning("CT log poll failed: %s", error)
        except TRANSIENT as error:
            LOG.warning("CT list poll failed: %s", error)
        time.sleep(max(1, 60 - (time.monotonic() - start)))


def next_dork(db, daily_pages, now):
    templates = ('intitle:"My Blog"', 'intitle:"My WordPress Blog"',
                 '"Just another WordPress site"', '"powered by WordPress"')
    keys = [(template, region, days) for template in templates
            for region in (".in", ".id", ".br", ".mx", ".de", ".fr", ".jp", "")
            for days in (7, 30, 90, 0)]
    with db:
        db.execute("BEGIN IMMEDIATE")
        day_start = now - now % 86400
        count, latest = db.execute("SELECT count(*),max(requested) FROM dork_queries WHERE requested>=?",
                                   (day_start,)).fetchone()
        if count >= daily_pages or (latest is not None and now - latest < 86400 / daily_pages):
            return None
        recent = {key for key, in db.execute("SELECT query_key FROM dork_queries WHERE requested>?",
                                             (now - 7 * 86400,))}
        available = [key for key in keys if json.dumps(key) not in recent]
        if not available:
            return None
        template, region, days = key = random.choice(available)
        db.execute("INSERT OR REPLACE INTO dork_queries VALUES (?,?)", (json.dumps(key), now))
    query = template + " -site:wordpress.org"
    if region:
        query += " site:" + region
    if days:
        query += " after:" + (datetime.fromtimestamp(now, timezone.utc).date() - timedelta(days=days)).isoformat()
    return query


def dork_feed(state, token, daily_pages):
    while True:
        delay = 60
        if not (state.parent / "paused").exists():
            try:
                with closing(connect(state)) as db:
                    query = next_dork(db, daily_pages, int(time.time()))
                    if query:
                        payload = json.dumps({"queries": query, "maxPagesPerQuery": 1,
                                              "includeUnfilteredResults": True}).encode()
                        request = Request(APIFY_SEARCH, data=payload, headers={
                            "Content-Type": "application/json", "Authorization": "Bearer " + token,
                        })
                        with urlopen(request, timeout=110) as response:
                            data = response.read((4 << 20) + 1)
                        if len(data) > 4 << 20:
                            raise ValueError("oversized dork response")
                        hits = (hit["url"] for row in json.loads(data) for hit in row.get("organicResults", ()))
                        store_dork_seeds(db, hits)
            except HTTPError as error:
                with error:
                    LOG.warning("Dork provider returned HTTP %d; cooling down for 24h", error.code)
                delay = 86400
            except (TypeError, *TRANSIENT) as error:
                LOG.warning("Dork refresh deferred (%s)", type(error).__name__)
        time.sleep(delay)


def reverse_domains(provider, ip):
    if provider == "thc":
        url = f"{REVERSE_IP_ENDPOINT}?ip_address={ip}&limit={REVERSE_IP_LIMIT}"
    else:
        url = f"{REVERSE_IP_DNSRIFT_ENDPOINT}?ip={ip}"
    try:
        with urlopen(Request(url, headers={"User-Agent": "CNC WordPress Generator"}), timeout=20) as response:
            data = response.read(REVERSE_IP_RESPONSE_LIMIT + 1)
    except HTTPError as error:
        if provider != "thc" or error.code != 404:
            raise
        error.close()
        data = b""
    if len(data) > REVERSE_IP_RESPONSE_LIMIT:
        raise ValueError("oversized reverse IP response")
    if provider == "dnsrift":
        payload = json.loads(data)
        if not isinstance(payload, dict) or payload.get("error") or payload.get("success") is False:
            raise ValueError("DNSRift reported an API error")
        if payload.get("ip") and payload["ip"] != ip:
            raise ValueError("DNSRift response IP does not match lookup")
        domains = payload.get("domain_list")
        if not isinstance(domains, list) or not all(isinstance(domain, str) for domain in domains):
            raise ValueError("invalid DNSRift domain list")
        yield from domains
        return
    if data:
        rows = csv.DictReader(io.StringIO(data.decode("utf-8-sig")))
        if not {"apexDomain", "subdomain"}.issubset(rows.fieldnames or ()):
            raise ValueError("invalid reverse IP CSV header")
        for row in rows:
            apex = (row["apexDomain"] or "").strip().lower().rstrip(".")
            domain = (row["subdomain"] or apex).strip().lower().rstrip(".")
            if not apex or (domain != apex and not domain.endswith("." + apex)):
                continue
            yield domain


def reverse_seed(db, host, now, cache_room=MAX_REVERSE_CACHE_HOSTS):
    if cache_room <= 0:
        return False
    try:
        resolved = {ipaddress.IPv4Address(host)}
    except ipaddress.AddressValueError:
        addresses = socket.getaddrinfo(host + ".", None, socket.AF_INET, socket.SOCK_STREAM)
        resolved = {ipaddress.IPv4Address(row[4][0]) for row in addresses}
    ips = sorted(str(ip) for ip in resolved if ip.is_global and not ip.is_multicast)
    for ip in ips:
        if db.execute("SELECT 1 FROM reverse_lookups WHERE ip=? AND looked_up>?",
                      (ip, now - 7 * 86400)).fetchone():
            continue
        last = db.execute("SELECT provider FROM reverse_rotation WHERE slot=1").fetchone()
        providers = ("thc", "dnsrift")
        if last == ("thc",):
            providers = providers[::-1]
        for provider in providers:
            try:
                found = {}
                for domain in reverse_domains(provider, ip):
                    candidate = bulk.homepage(domain)
                    if candidate and candidate[0] != host:
                        found.setdefault(*candidate)
                break
            except (csv.Error, IncompleteRead, *TRANSIENT) as error:
                if isinstance(error, HTTPError):
                    error.close()
                if provider == providers[-1]:
                    raise
                LOG.warning("Reverse IP provider %s failed (%s), trying the next provider",
                            provider, type(error).__name__)
        with db:
            insert = "INSERT OR IGNORE INTO reverse_candidates VALUES (?,?)"
            if len(found) <= cache_room:
                db.executemany(insert, found.items())
            else:
                for candidate in found.items():
                    cache_room -= db.execute(insert, candidate).rowcount
                    if not cache_room:
                        break
            db.execute("INSERT OR REPLACE INTO reverse_lookups VALUES (?,?)", (ip, now))
            db.execute("INSERT OR REPLACE INTO reverse_rotation VALUES (1,?)", (provider,))
        LOG.info("Reverse IP provider %s returned %d eligible hostnames", provider, len(found))
        # The next pass handles another address without bursting the provider.
        return False
    return True


def queue_verified_seeds(db, hosts, now):
    # Every verified origin can expand the graph, including fallback catalogs.
    # Persist the complete queue; provider pacing controls requests, not a
    # 200-host truncation which used to silently discard useful seeds.
    db.executemany("INSERT OR IGNORE INTO reverse_seeds VALUES (?,?)",
                   ((host, now) for host in hosts))


def expire_reverse_cache(db, now):
    """Candidates persist until allocation; time alone must not discard them."""
    return


def random_reverse_ip(db):
    """Sample public IPv4 addresses, excluding every successfully queried IP."""
    for _ in range(128):
        ip = ipaddress.IPv4Address(random.getrandbits(32))
        if ip.is_global and not ip.is_multicast and not db.execute(
            "SELECT 1 FROM reverse_lookups WHERE ip=?", (str(ip),)
        ).fetchone():
            return str(ip)
    return None


def reverse_feed(state):
    while True:
        delay = 15
        if not (state.parent / "paused").exists():
            with closing(connect(state)) as db:
                now = int(time.time())
                try:
                    expire_reverse_cache(db, now)
                except sqlite3.Error as error:
                    LOG.warning("Reverse IP cache rotation deferred (%s)", type(error).__name__)
                cache_room = MAX_REVERSE_CACHE_HOSTS - db.execute(
                    "SELECT count(*) FROM reverse_candidates"
                ).fetchone()[0]
                if not pool_snapshot(db)[2]:
                    cache_room = 0
                seed = db.execute("""
                    SELECT s.host FROM reverse_seeds s
                    LEFT JOIN dork_candidates d ON d.host=s.host
                    WHERE s.due<=? ORDER BY d.host IS NULL,s.due,s.host LIMIT 1
                """, (now,)).fetchone() if cache_room > 0 else None
                if cache_room > 0 and (not seed or random.random() < 0.5):
                    random_ip = random_reverse_ip(db)
                    if random_ip:
                        seed = (random_ip,)
                if seed:
                    host = seed[0]
                    delay = 3
                    try:
                        finished = reverse_seed(db, host, now, cache_room)
                        if not finished:
                            # A bulk lookup costs three tokens at 0.5 tokens/s refill.
                            delay = 6
                        with db:
                            if finished:
                                db.execute("DELETE FROM reverse_seeds WHERE host=?", (host,))
                            else:
                                db.execute("UPDATE reverse_seeds SET due=? WHERE host=?", (now + 3, host))
                    except (csv.Error, IncompleteRead, *TRANSIENT) as error:
                        if isinstance(error, HTTPError):
                            error.close()
                        delay = 60
                        try:
                            with db:
                                db.execute("UPDATE reverse_seeds SET due=? WHERE host=?", (now + 3600, host))
                        except sqlite3.OperationalError:
                            # Leave the seed queued when the database cannot
                            # record the delay. The next iteration retries it.
                            LOG.warning("Could not persist reverse IP retry delay")
                        LOG.warning("Reverse IP lookup deferred (%s)", type(error).__name__)
        time.sleep(delay)


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
    fresh_query = f"""
        SELECT c.url FROM fresh c WHERE {filter_attempted} AND {ready}
        AND NOT EXISTS (
            SELECT 1 FROM catalog.candidates known
            WHERE known.host=c.host AND known.sources & {PRIORITY_CATALOG_SOURCES} != 0
        )
        ORDER BY c.seen DESC, c.rowid DESC LIMIT ?
    """
    # Reserve 10% of each batch for new certificate candidates.
    # Other sources can use every slot when the live feed is empty.
    fresh = db.execute(fresh_query, (max(1, limit // 10),)).fetchall()
    if len(fresh) == limit:
        return fresh
    remaining = limit - len(fresh)
    # Recover older detections absent from the current export through live
    # verification. A historical catalog flag is not a publication receipt.
    verified = bulk.SOURCES["verified"]
    historical = db.execute(f"""
        SELECT c.url FROM catalog.candidates c WHERE (c.sources & {verified}) != 0 AND {ready}
        AND NOT EXISTS (SELECT 1 FROM merged_hosts m WHERE m.host=c.host)
        AND NOT EXISTS (SELECT 1 FROM generator_checked g WHERE g.host=c.host)
        ORDER BY c.host LIMIT ?
    """, (remaining,)).fetchall()
    if historical:
        return fresh + historical
    # HTTP Archive has already detected WordPress on these hosts. Verify them
    # before spending worker slots on generic web and certificate candidates.
    for mask, source in priority_sources():
        rows = db.execute(f"""
            SELECT c.url FROM catalog.candidates c
            WHERE (c.sources & {mask}) = {source} AND {filter_attempted} AND {ready}
            ORDER BY c.host LIMIT ?
        """, (remaining,)).fetchall()
        if rows:
            return fresh + rows
    discovery = db.execute(f"""
        SELECT c.url FROM discovery_candidates c WHERE {filter_attempted} AND {ready}
        AND NOT EXISTS (SELECT 1 FROM fresh f WHERE f.host=c.host)
        ORDER BY c.host LIMIT ?
    """, (remaining,)).fetchall()
    if discovery:
        return fresh + discovery
    source = db.execute(fresh_query, (limit,)).fetchall()
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


def publish_merged(state, merged, sources, batch=None):
    return exports.publish(state, merged, sources, batch=batch)


def discovery_policy(db):
    """Report the primary source preference and the verified CT total."""
    ct_verified = db.execute("SELECT coalesce(sum(verified),0) FROM source_yield WHERE source='ct'").fetchone()[0]
    return ct_verified, CT_TARGET_SHARE


def mixed_candidates(db, catalog, limit):
    db.execute("ATTACH DATABASE ? AS catalog", (catalog.resolve().as_uri() + "?mode=ro",))
    attempted = db.execute("SELECT 1 FROM catalog.sqlite_schema WHERE name='attempted'").fetchone()
    eligible = """
        NOT EXISTS (SELECT 1 FROM generator_checked g WHERE g.host=c.host)
        AND NOT EXISTS (SELECT 1 FROM ready_candidates r WHERE r.host=c.host)
        AND NOT EXISTS (SELECT 1 FROM shard_hosts s WHERE s.host=c.host)
        AND NOT EXISTS (SELECT 1 FROM merged_hosts m WHERE m.host=c.host)
        AND NOT EXISTS (SELECT 1 FROM deferred d WHERE d.host=c.host AND d.until>unixepoch())
    """
    unattempted = "AND NOT EXISTS (SELECT 1 FROM catalog.attempted a WHERE a.host=c.host)" if attempted else ""
    sources = {
        "ct": ("fresh_schedule s INDEXED BY fresh_schedule_due CROSS JOIN fresh",
               "s.ready_at<=unixepoch() AND c.host=s.host", "random()"),
        "reverse-ip": ("catalog.candidates", f"(c.sources & {bulk.SOURCES['reverse-ip']}) != 0", "c.host"),
        "random-txt": ("catalog.candidates", f"(c.sources & {bulk.SOURCES['random-txt']}) != 0", "c.host"),
        "historical": ("catalog.candidates", f"(c.sources & {bulk.SOURCES['verified']}) != 0", "c.host"),
        "discovery": ("discovery_candidates", "1", "c.host"),
        "reverse-seed": ("reverse_candidates", "1", "c.host"),
        "retry": ("retries", "c.done=0 AND c.due<=unixepoch()", "c.due, c.host"),
    }
    for name, (mask, bit) in zip(("archive", "ranked"), priority_sources()):
        sources[name] = ("catalog.candidates", f"(c.sources & {mask}) = {bit}", "c.host")
    indexed_sources = {name for name, in db.execute("SELECT name FROM catalog.sqlite_schema WHERE type='index'")}
    has_recent = any(row[1] == "first_seen" for row in db.execute("PRAGMA catalog.table_info(candidates)"))
    archive_ready = db.execute("SELECT 1 FROM catalog.sqlite_schema WHERE name='archive_ready'").fetchone()
    pools = {}
    rng = random.Random()
    reverse_sources = ("reverse-ip", "reverse-seed")
    primary = (*reverse_sources, "ct")
    # Use bounded source windows; reserve 80% of each page for reverse IP.
    source_order = (*primary, *(name for name in sources if name not in primary))
    for source in source_order:
        table, condition, order = sources[source]
        attempted_clause = unattempted if source != "retry" else ""
        if source == "reverse-seed":
            rows = candidate_page(db, source, table, condition, eligible, attempted_clause, limit, True)
        elif table == "catalog.candidates":
            rows = []
            if source == "archive" and archive_ready:
                rows = db.execute(f"""
                    SELECT c.host,c.url FROM catalog.archive_ready c WHERE {eligible}
                    {attempted_clause} ORDER BY c.host LIMIT ?
                """, (limit,)).fetchall()
            if source == "archive" and has_recent and len(rows) < limit:
                recent = db.execute(f"""
                    WITH recent AS MATERIALIZED (
                        SELECT host,url,sources FROM catalog.candidates
                        WHERE first_seen IS NOT NULL
                        ORDER BY first_seen DESC,host DESC LIMIT ?
                    )
                    SELECT c.host,c.url FROM recent c WHERE {condition} AND {eligible}
                    {attempted_clause} LIMIT ?
                """, (max(1024, min(MAX_CANDIDATE_PAGE_ROWS, limit * 4)), limit)).fetchall()
                rows = list(dict.fromkeys(rows + recent))[:limit]
            if len(rows) < limit:
                sparse_source = source in ("reverse-ip", "random-txt")
                indexed = not sparse_source or f"generator_mix_{bulk.SOURCES[source]}" in indexed_sources
                older = candidate_page(db, source, table, condition, eligible, attempted_clause, limit, indexed)
                rows = list(dict.fromkeys(rows + older))[:limit]
        else:
            rows = db.execute(f"""
                SELECT c.host,c.url FROM {table} c WHERE {condition} AND {eligible}
                {attempted_clause} ORDER BY {order} LIMIT ?
            """, (limit,)).fetchall()
        if rows:
            rng.shuffle(rows)
            pools[source] = rows
    chosen = {}

    def take(names, count):
        target = min(limit, len(chosen) + count)
        while len(chosen) < target:
            available = [name for name in names if pools.get(name)]
            if not available or len(chosen) >= limit:
                return
            source = rng.choice(available)
            host, url = pools[source].pop()
            if host in chosen:
                continue
            chosen[host] = (host, url, source)

    take(reverse_sources, max(1, int(limit * REVERSE_TARGET_SHARE)))
    take(["ct"], max(1, int(limit * CT_TARGET_SHARE)))
    # Auxiliary live inputs before historical crawl/ranking snapshots.
    take(["random-txt", "discovery", "historical", "archive", "ranked"], limit - len(chosen))
    take(["retry"], min(max(1, limit // 10), limit - len(chosen)))
    take(["archive", "ranked"], limit - len(chosen))
    if len(chosen) < limit:
        fallbacks = ["commoncrawl", "webgraph"]
        rng.shuffle(fallbacks)
        for source in fallbacks:
            bit = bulk.SOURCES[source]
            for host, url in candidate_page(
                db, source, "catalog.candidates", f"(c.sources & {bit}) != 0", eligible, unattempted, limit, False,
            ):
                chosen.setdefault(host, (host, url, source))
                if len(chosen) == limit:
                    break
            if len(chosen) == limit:
                break
    take(["ct"], limit - len(chosen))
    take(reverse_sources, limit - len(chosen))
    # Retries may use otherwise idle capacity, but cannot displace fresh hosts.
    take(["retry"], limit - len(chosen))
    result = list(chosen.values())
    rng.shuffle(result)
    return result


def candidate_page(db, source, table, condition, eligible, attempted_clause, limit, indexed):
    saved = db.execute("SELECT host FROM source_cursors WHERE source=?", (source,)).fetchone()
    start = saved[0] if saved else ""
    page_condition = condition if indexed else "1"
    row_condition = "1" if indexed else condition
    columns = "c.host,c.url" if indexed else "c.host,c.url,c.sources"
    page_size = max(1024, min(MAX_CANDIDATE_PAGE_ROWS, limit * 4))
    # Bound the anti-joins even when an entire source has already been checked.
    page = db.execute(f"""
        WITH page AS MATERIALIZED (
            SELECT {columns} FROM {table} c
            WHERE c.host>? AND {page_condition}
            ORDER BY c.host LIMIT ?
        )
        SELECT c.host,c.url,
            CASE WHEN {row_condition}
                 THEN CASE WHEN {eligible} {attempted_clause} THEN 1 ELSE 0 END
                 ELSE 0 END
        FROM page c
    """, (start, page_size)).fetchall()
    rows = [(host, url) for host, url, ready in page if ready]
    if len(rows) < limit:
        next_host = page[-1][0] if len(page) == page_size else ""
        with db:
            db.execute("INSERT OR REPLACE INTO source_cursors VALUES (?,?)", (source, next_host))
    return random.sample(rows, min(limit, len(rows)))


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
        for source, destination in completed:
            batch=source.stem.removeprefix("wordpress-fresh-enum_fresh_")
            publish_merged(state, args.merged, [source], batch=batch)
            archive_output(source, destination)


def defer_batch(state, batch, path, verified=()):
    due = int(time.time()) + 86400
    with closing(connect(state)) as db, db:
        db.executemany("INSERT OR REPLACE INTO deferred VALUES (?,?)",
                       ((host, due) for host in bulk.attempted_hosts(path) if host not in verified))
        db.executemany("INSERT OR IGNORE INTO generator_checked VALUES (?)", ((host,) for host in verified))
        db.execute("UPDATE concurrency_trials SET status='failed' WHERE batch=?", (batch,))
        release_batch(db, batch)


def release_batch(db, batch):
    db.execute("DELETE FROM active WHERE batch=?", (batch,))
    db.execute("DELETE FROM shard_hosts WHERE batch=?", (batch,))
    db.execute("DELETE FROM shard_leases WHERE batch=?", (batch,))


def recover_partial_results(args, state, path, snapshot, output):
    if not snapshot.is_file() or not output.is_file():
        return set()
    accepted = set(bulk.attempted_hosts(snapshot))
    if not accepted <= set(bulk.attempted_hosts(path)):
        raise ValueError("controller input contains a hostname outside the original batch")
    with closing(connect(args.catalog)) as catalog, catalog:
        bulk.import_urls(catalog, output, "verified", "txt", allowed_hosts=accepted, summarize=False)
        verified = set(bulk.attempted_hosts(output))
        bulk.insert_attempted(catalog, verified)
    if verified:
        publish_merged(state, args.merged, [output], batch=path.stem)
    return verified


def dispatch(api, args, state):
    if getattr(args, "shards", False):
        with closing(connect(state)) as db:
            legacy = db.execute("SELECT batch FROM active").fetchone()
        if legacy:
            return reconcile_batch(api, args, state, legacy[0])
        return dispatch_shards(api, args, state)
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
            # The controller splits contiguous lines. Mix DNS/TLD and hosting
            # clusters so one worker does not get the entire slow tail.
            random.Random(batch).shuffle(rows)
            with (args.root / (batch + ".txt")).open("x", encoding="ascii") as batch_file:
                batch_file.writelines(url + "\n" for url, in rows)
                batch_file.flush()
                os.fsync(batch_file.fileno())
            with db:
                db.execute("INSERT INTO active VALUES (1,?)", (batch,))
    return reconcile_batch(api, args, state, batch)


def restage_oversized_shards(args, state):
    """Return unlaunched large batches to the pool without replaying used hosts."""
    with closing(connect(state)) as db:
        batches = db.execute("""
            SELECT l.batch FROM shard_leases l JOIN shard_hosts s USING(batch)
            WHERE l.launched=0 GROUP BY l.batch HAVING count(*)>?
        """, (args.batch_limit,)).fetchall()
        for batch, in batches:
            path = args.root / (batch + ".txt")
            urls = dict(bulk.homepage(url) for url in path.read_text(encoding="ascii").splitlines())
            hosts = db.execute("SELECT host,source FROM shard_hosts WHERE batch=?", (batch,)).fetchall()
            if set(urls) != {host for host, _ in hosts}:
                raise ValueError("pending shard input does not match its leased hosts")
            with db:
                db.execute("BEGIN IMMEDIATE")
                db.executemany("""
                    INSERT OR IGNORE INTO ready_candidates
                    SELECT :host,:url,:source
                    WHERE NOT EXISTS (SELECT 1 FROM generator_checked WHERE host=:host)
                      AND NOT EXISTS (SELECT 1 FROM merged_hosts WHERE host=:host)
                """, ({"host":host,"url":urls[host],"source":source} for host, source in hosts))
                db.execute("DELETE FROM shard_hosts WHERE batch=?", (batch,))
                db.execute("DELETE FROM shard_leases WHERE batch=?", (batch,))
            archive = args.root / "archive" / "resharded"
            archive.mkdir(parents=True, exist_ok=True)
            path.replace(archive / path.name)
            LOG.info("Restaged %d unlaunched hosts from %s for smaller shards", len(hosts), batch)


def pool_snapshot(db):
    return db.execute("SELECT available,started,refilling,target FROM pool_policy WHERE slot=1").fetchone()


def stock_candidates(db, catalog, limit, *, attached=False, policy=None):
    """Advance one catalog window without repeating every live-source query."""
    if not attached:
        db.execute("ATTACH DATABASE ? AS catalog", (catalog.resolve().as_uri()+"?mode=ro",))
    saved = db.execute("SELECT host FROM source_cursors WHERE source='stock'").fetchone()
    if saved is None:
        saved = db.execute("SELECT max(host) FROM source_cursors WHERE source IN ('commoncrawl','webgraph')").fetchone()
    start = saved[0] if saved and saved[0] else ''
    page = db.execute("""
        SELECT host,url,sources FROM catalog.candidates
        WHERE host>? ORDER BY host LIMIT ?
    """,(start,limit)).fetchall()
    next_host = page[-1][0] if len(page)==limit else ''
    excluded=set()
    if page:
        page_hosts={row[0] for row in page}
        lower,upper=page[0][0],page[-1][0]
        tables=('generator_checked','ready_candidates','shard_hosts','merged_hosts','deferred')
        if db.execute("SELECT 1 FROM catalog.sqlite_schema WHERE name='attempted'").fetchone():
            tables+=('catalog.attempted',)
        # Read each history range once in key order. Point lookups across six
        # large tables thrashed the small SQLite page cache for every host.
        for table in tables:
            condition=" AND until>unixepoch()" if table=='deferred' else ''
            excluded.update(host for host, in db.execute(
                f"SELECT host FROM {table} WHERE host>=? AND host<=?{condition}",(lower,upper)
            ) if host in page_hosts)
    priorities = tuple(name for name in ('archive','ranked','reverse-ip','commoncrawl','webgraph','random-txt') if policy is None or name in source_policy.allowed(policy))
    rows=[]
    for host,url,bits in page:
        if host not in excluded and not bits & bulk.SOURCES['verified']:
            source=next((name for name in priorities if bits & bulk.SOURCES[name]),None)
            if source:rows.append((host,url,source))
    return rows,len(page),next_host


def reverse_pool_page(db, catalog, limit):
    """Bound catalog work and reserve the live reverse supply before fallback stock."""
    db.execute("ATTACH DATABASE ? AS catalog", (catalog.resolve().as_uri()+"?mode=ro",))
    eligible="""NOT EXISTS (SELECT 1 FROM generator_checked g WHERE g.host=c.host)
      AND NOT EXISTS (SELECT 1 FROM ready_candidates r WHERE r.host=c.host)
      AND NOT EXISTS (SELECT 1 FROM shard_hosts s WHERE s.host=c.host)
      AND NOT EXISTS (SELECT 1 FROM merged_hosts m WHERE m.host=c.host)
      AND NOT EXISTS (SELECT 1 FROM deferred d WHERE d.host=c.host AND d.until>unixepoch())"""
    attempted=db.execute("SELECT 1 FROM catalog.sqlite_schema WHERE name='attempted'").fetchone()
    unattempted='AND NOT EXISTS (SELECT 1 FROM catalog.attempted a WHERE a.host=c.host)' if attempted else ''
    reverse_limit=max(1,int(limit*REVERSE_TARGET_SHARE))
    rows=[(h,u,'reverse-seed') for h,u in candidate_page(db,'reverse-seed','reverse_candidates','1',eligible,unattempted,reverse_limit,True)]
    if len(rows)<reverse_limit:
        bit=bulk.SOURCES['reverse-ip']
        indexed=db.execute("SELECT 1 FROM catalog.sqlite_schema WHERE name=?",(f'generator_mix_{bit}',)).fetchone()
        rows.extend((h,u,'reverse-ip') for h,u in candidate_page(db,'reverse-ip','catalog.candidates',f'(c.sources & {bit}) != 0',eligible,unattempted,reverse_limit-len(rows),bool(indexed)))
    chosen={h:(h,u,source) for h,u,source in rows}
    # The small auxiliary allowance is shared by CT and other sources. An empty
    # reverse feed cannot silently turn a crawl scan into a 100% crawl page.
    other_limit=limit-reverse_limit
    ct_limit=max(1,int(limit*CT_TARGET_SHARE)) if other_limit else 0
    fresh=db.execute(f"""SELECT c.host,c.url FROM fresh_schedule s INDEXED BY fresh_schedule_due
        CROSS JOIN fresh c WHERE s.ready_at<=unixepoch() AND c.host=s.host AND {eligible}
        {unattempted} ORDER BY s.ready_at,s.host LIMIT ?""",(other_limit,)).fetchall()
    for h,u in fresh[:ct_limit]:
        if h not in chosen:chosen[h]=(h,u,'ct')
    # Reuse the attached catalog; auxiliary stock keeps its bounded cursor.
    auxiliary,scanned,cursor=stock_candidates(db,catalog,max(0,other_limit-min(len(fresh),ct_limit)),attached=True) if other_limit>min(len(fresh),ct_limit) else ([],0,None)
    reverse_hosts=sum(row[2] in ('reverse-ip','reverse-seed') for row in chosen.values())
    for row in auxiliary:
        if len(chosen)-reverse_hosts>=other_limit:break
        chosen.setdefault(row[0],row)
    for h,u in fresh[ct_limit:]:
        if len(chosen)-reverse_hosts>=other_limit:break
        chosen.setdefault(h,(h,u,'ct'))
    return list(chosen.values()),scanned,cursor


def sync_sources(args,state):
    allowed=source_policy.allowed(source_policy.read(args.root),args.root)
    if getattr(args,'source_signature',None)==allowed:return
    slots=','.join('?' for _ in allowed) or "''"
    with closing(connect(state)) as db,db:
        db.execute('BEGIN IMMEDIATE')
        db.execute(f'INSERT OR IGNORE INTO source_hold SELECT * FROM ready_candidates WHERE source NOT IN ({slots})',allowed)
        moved=db.execute(f'DELETE FROM ready_candidates WHERE source NOT IN ({slots})',allowed).rowcount
        room=max(0,db.execute('SELECT target-available FROM pool_policy WHERE slot=1').fetchone()[0])
        restored=db.execute(f"""INSERT OR IGNORE INTO ready_candidates SELECT c.* FROM source_hold c
            WHERE c.source IN ({slots})
              AND NOT EXISTS(SELECT 1 FROM generator_checked g WHERE g.host=c.host)
              AND NOT EXISTS(SELECT 1 FROM shard_hosts h WHERE h.host=c.host)
              AND NOT EXISTS(SELECT 1 FROM merged_hosts m WHERE m.host=c.host)
            LIMIT ?""",(*allowed,room)).rowcount
        db.execute('DELETE FROM source_hold WHERE host IN (SELECT host FROM ready_candidates)')
        if moved or restored:db.execute('UPDATE pool_policy SET refilling=(available<target) WHERE slot=1')
    args.source_signature=allowed


def drain_catalog_queue(args,state,limit):
    """Move newly inserted CC hosts directly to verification with bookkeeping dedup only."""
    if not source_policy.read(args.root)['commoncrawl']:return 0
    with closing(connect(args.catalog)) as cat:
        if not cat.execute("SELECT 1 FROM sqlite_schema WHERE name='fresh_catalog_queue'").fetchone():return 0
        rows=cat.execute('SELECT host,url FROM fresh_catalog_queue ORDER BY host LIMIT ?',(limit,)).fetchall()
    if not rows:return 0
    with closing(connect(state)) as db,db:
        db.execute('CREATE TEMP TABLE incoming_cc(host TEXT PRIMARY KEY,url TEXT NOT NULL) WITHOUT ROWID')
        db.executemany('INSERT OR IGNORE INTO incoming_cc VALUES (?,?)',rows)
        db.commit();db.execute('BEGIN IMMEDIATE')
        room=max(0,db.execute('SELECT target-available FROM pool_policy WHERE slot=1').fetchone()[0])
        added=db.execute("""INSERT OR IGNORE INTO ready_candidates SELECT c.host,c.url,'commoncrawl' FROM incoming_cc c
            WHERE NOT EXISTS(SELECT 1 FROM generator_checked g WHERE g.host=c.host)
              AND NOT EXISTS(SELECT 1 FROM merged_hosts m WHERE m.host=c.host)
              AND NOT EXISTS(SELECT 1 FROM shard_hosts h WHERE h.host=c.host)
            ORDER BY c.host LIMIT ?""",(room,)).rowcount
        consumed=db.execute("""SELECT c.host FROM incoming_cc c
            WHERE EXISTS(SELECT 1 FROM ready_candidates r WHERE r.host=c.host)
               OR EXISTS(SELECT 1 FROM generator_checked g WHERE g.host=c.host)
               OR EXISTS(SELECT 1 FROM merged_hosts m WHERE m.host=c.host)
               OR EXISTS(SELECT 1 FROM shard_hosts h WHERE h.host=c.host)""").fetchall()
        if source_policy.read(args.root)['reverse_ip']:
            db.execute("INSERT OR IGNORE INTO reverse_input_seeds SELECT host,'commoncrawl',unixepoch() FROM incoming_cc WHERE host IN (SELECT host FROM ready_candidates)")
        db.execute('UPDATE pool_policy SET started=max(started,available>=10000),refilling=(available<target) WHERE slot=1')
    if consumed:
        with closing(connect(args.catalog)) as cat,cat:
            cat.executemany('DELETE FROM fresh_catalog_queue WHERE host=?',consumed)
    return added


def refill_pool(args, state, fast=False):
    sync_sources(args,state)
    args.pool_scan_progress = False
    # Scanning OFF preserves collection; only dispatch observes the pause flag.
    with closing(connect(state)) as db:
        with db:
            db.execute("BEGIN IMMEDIATE")
            available, started, refilling, target = pool_snapshot(db)
            if not refilling and (available < POOL_LOW or ((args.root / "paused").exists() and available < target)):
                target = POOL_MIN
                refilling = 1
                db.execute("UPDATE pool_policy SET refilling=1,target=? WHERE slot=1", (target,))
            if available >= target:
                db.execute("UPDATE pool_policy SET refilling=0,started=1 WHERE slot=1")
                return 0
        if not refilling:
            return 0
        selection_started=time.monotonic()
        if not (args.root/"commoncrawl-mode.json").exists() and (added:=drain_catalog_queue(args,state,min(POOL_FAST_PAGE,target-available)))>0:return added
        limit=min(POOL_FAST_PAGE if fast else POOL_PAGE,target-available,POOL_MAX-available)
        balanced=getattr(args,"pooled",False)
        policy=source_policy.read(args.root)
        catalog_policy=dict(policy)
        if (args.root/"commoncrawl-mode.json").exists():catalog_policy["commoncrawl"]=False
        if balanced and policy['reverse_ip']:
            rows,scanned,stock_cursor = reverse_pool_page(db,args.catalog,limit)
            rows=[row for row in rows if row[2] in source_policy.allowed(policy,args.root)]
            args.pool_scan_progress=bool(scanned)
        elif balanced:
            if catalog_policy['commoncrawl'] or catalog_policy['other_catalogs']:
                rows,scanned,stock_cursor=stock_candidates(db,args.catalog,limit-max(1,limit//10) if policy["ct"] else limit,policy=catalog_policy)
            else:rows,scanned,stock_cursor=[],0,None
            if policy['ct'] and len(rows)<limit:
                ct_rows=db.execute("""SELECT c.host,c.url,'ct' FROM fresh_schedule s INDEXED BY fresh_schedule_due
                    CROSS JOIN fresh c WHERE s.ready_at<=unixepoch() AND c.host=s.host
                    AND NOT EXISTS(SELECT 1 FROM generator_checked g WHERE g.host=c.host)
                    AND NOT EXISTS(SELECT 1 FROM ready_candidates r WHERE r.host=c.host)
                    AND NOT EXISTS(SELECT 1 FROM shard_hosts h WHERE h.host=c.host)
                    AND NOT EXISTS(SELECT 1 FROM merged_hosts m WHERE m.host=c.host)
                    ORDER BY s.ready_at,s.host LIMIT ?""",(limit-len(rows),)).fetchall()
                rows.extend(ct_rows)
            args.pool_scan_progress=bool(scanned)
        elif fast:
            rows,scanned,stock_cursor = stock_candidates(db,args.catalog,limit)
            args.pool_scan_progress = bool(scanned)
        else:
            rows = mixed_candidates(db,args.catalog,limit)
        selection_seconds=time.monotonic()-selection_started
        # Pool represents previously unattempted hosts. Explicit retries remain
        # tracked separately and cannot inflate its fresh-host count.
        rows = [row for row in rows if row[2] != "retry"]
        admission_started=time.monotonic()
        with db:
            db.execute("BEGIN IMMEDIATE")
            before, *_ = pool_snapshot(db)
            room = min(target, POOL_MAX) - before
            if balanced and policy["reverse_ip"]:
                nonreverse=db.execute("SELECT count(*) FROM ready_candidates WHERE source NOT IN ('reverse-ip','reverse-seed')").fetchone()[0]
                other_room=max(0,target-round(target*REVERSE_TARGET_SHARE)-nonreverse)
                reverse_rows=[r for r in rows if r[2] in ('reverse-ip','reverse-seed')]
                other_rows=[r for r in rows if r[2] not in ('reverse-ip','reverse-seed')][:other_room]
                rows=reverse_rows+other_rows
            admitted = rows[:max(0, room)]
            db.executemany("INSERT OR IGNORE INTO ready_candidates VALUES (?,?,?)", admitted)
            db.executemany("INSERT OR IGNORE INTO reverse_input_seeds(host,source,due) VALUES (?,?,?)",
                           ((h,source,int(time.time())) for h,_,source in admitted if source not in ('reverse-ip','reverse-seed','retry')))
            db.executemany("DELETE FROM reverse_candidates WHERE host=?", ((host,) for host, _, _ in admitted))
            available, *_ = pool_snapshot(db)
            db.execute("UPDATE pool_policy SET started=max(started,?),refilling=? WHERE slot=1",
                       (int(available >= min(POOL_MIN,SHARD_TARGETS)), int(available < target)))
            if (fast or balanced) and stock_cursor is not None:
                # Commit allocation and cursor together; a crash can safely retry.
                db.execute("INSERT OR REPLACE INTO source_cursors VALUES ('stock',?)",(stock_cursor,))
        if fast or balanced:
            LOG.info("Fast pool page: scanned=%d added=%d select=%.2fs admit=%.2fs",
                     scanned,available-before,selection_seconds,time.monotonic()-admission_started)
        return available - before


def graph_feed(args, state):
    """Advance the public graph when live inputs cannot fill the fresh pool."""
    while True:
        delay=30
        try:
            with closing(connect(state)) as db:
                available, _, refilling, _ = pool_snapshot(db)
                seed_backlog=db.execute("SELECT 1 FROM reverse_input_seeds WHERE due<=unixepoch() LIMIT 1").fetchone()
                auxiliary_backlog=db.execute("SELECT count(*) FROM ready_candidates WHERE source NOT IN ('reverse-ip','reverse-seed')").fetchone()[0]
            # Existing CT/catalog inputs are the seed slice. Do not keep growing
            # unrelated crawl stock while those inputs still await expansion.
            policy=source_policy.read(args.root)
            if policy["commoncrawl"] and refilling and available < POOL_MIN and (not policy["reverse_ip"] or (not seed_backlog and auxiliary_backlog < SHARD_TARGETS)):
                with closing(connect(args.catalog)) as catalog:
                    catalog.execute("PRAGMA busy_timeout=120000")
                    result = bulk.pull_commoncrawl(catalog)
                    LOG.info("Public graph refill: %s", json.dumps(result))
                    delay=1 if result.get("status")=="imported" else 60
        except TRANSIENT as error:
            LOG.warning("Public graph refill deferred (%s)", type(error).__name__)
        for _ in range(delay):
            time.sleep(1)
            if source_policy.read(args.root)!=policy:break


def pool_feed(args, state):
    rounds=1
    accumulated=0
    started=time.monotonic()
    while True:
        try:
            # Keep live CT/reverse/archive mixing, and amortize its expensive
            # queries over bounded catalog windows while rebuilding stock.
            fast=rounds%16!=0
            added = refill_pool(args,state,fast=fast)
            accumulated+=added
            rounds+=1
            if added and getattr(args, "shard_ready_event", None):
                args.shard_ready_event.set()
            if rounds%16==0 and accumulated:
                LOG.info("Pool restock added %d fresh candidates in %.1fs",accumulated,time.monotonic()-started)
                accumulated=0;started=time.monotonic()
        except TRANSIENT as error:
            LOG.warning("Pool refill deferred (%s)", type(error).__name__)
            added=0
            args.pool_scan_progress=False
        time.sleep(0.05 if added or getattr(args,'pool_scan_progress',False) else 0.25)


def fill_shards(args, state, target):
    if (args.root / "paused").exists():
        return
    shard_size = min(SHARD_TARGETS, args.batch_limit)
    allowed=source_policy.allowed(source_policy.read(args.root),args.root)
    if not allowed:return
    slots=','.join('?' for _ in allowed)
    with closing(connect(state)) as db:
        missing = target - db.execute(f"SELECT count(*) FROM shard_leases l WHERE launched=0 AND NOT EXISTS(SELECT 1 FROM shard_hosts h WHERE h.batch=l.batch AND h.source NOT IN ({slots}))",allowed).fetchone()[0]
        if missing <= 0:
            return
        # Production drains only the persistent pool, never source catalogs directly.
        pooled = getattr(args, "pooled", False)
        if pooled:
            available, started, _, _ = pool_snapshot(db)
            if not started or available < shard_size:
                return
        # Bound each gather so the first refill doesn't wait for a full fleet's worth of hosts.
        gather_limit = min(missing * shard_size, POOL_MIN) if pooled else min(missing * shard_size, SHARD_TARGETS)
        # Preserve small explicit batch limits for canaries.
        minimum = shard_size if pooled else MIN_SHARD_TARGETS if shard_size >= MIN_SHARD_TARGETS else 1
        if minimum == 1:
            candidates = mixed_candidates(db, args.catalog, gather_limit)
        else:
            with db:
                db.execute("""
                    DELETE FROM ready_candidates AS r
                    WHERE host IN (SELECT host FROM ready_candidates ORDER BY host LIMIT ?)
                      AND (EXISTS (SELECT 1 FROM merged_hosts m WHERE m.host=r.host)
                       OR EXISTS (SELECT 1 FROM shard_hosts s WHERE s.host=r.host)
                       OR EXISTS (SELECT 1 FROM deferred d WHERE d.host=r.host AND d.until>unixepoch())
                       OR (r.source!='retry' AND EXISTS (
                           SELECT 1 FROM generator_checked g WHERE g.host=r.host
                       )))
                """, (gather_limit,))
            staged = pool_snapshot(db)[0]
            if not pooled and staged < gather_limit:
                fresh = mixed_candidates(db, args.catalog, gather_limit - staged)
                with db:
                    db.executemany("INSERT OR IGNORE INTO ready_candidates VALUES (?,?,?)", fresh)
            candidates = db.execute(
                f"SELECT host,url,source FROM ready_candidates WHERE source IN ({slots}) ORDER BY host LIMIT ?", (*allowed,gather_limit)
            ).fetchall()
        shard_count = min(missing, len(candidates) // minimum)
        if not shard_count:
            return
        if pooled:
            candidates = candidates[:shard_count * shard_size]
        shards = [[] for _ in range(shard_count)]
        for index, candidate in enumerate(sorted(candidates, key=lambda row: row[2])):
            shards[index % len(shards)].append(candidate)
        for rows in shards:
            if (args.root / "paused").exists():
                return
            random.shuffle(rows)
            batch = uuid.uuid4().hex
            path = args.root / (batch + ".txt")
            with path.open("x", encoding="ascii") as stream:
                stream.writelines(url + "\n" for _, url, _ in rows)
                stream.flush()
                os.fsync(stream.fileno())
            fd = os.open(args.root, os.O_RDONLY)
            try:
                os.fsync(fd)
            finally:
                os.close(fd)
            with db:
                db.execute("INSERT INTO shard_leases(batch) VALUES (?)", (batch,))
                db.executemany("INSERT INTO shard_hosts VALUES (?,?,?)",
                               ((host, batch, source) for host, _, source in rows))
                db.execute("""
                    DELETE FROM reverse_candidates
                    WHERE host IN (SELECT host FROM shard_hosts WHERE batch=?)
                """, (batch,))
                if minimum > 1:
                    db.executemany("DELETE FROM ready_candidates WHERE host=?", ((host,) for host, _, _ in rows))
            if (ready_event := getattr(args, "shard_ready_event", None)) is not None:
                ready_event.set()


def replenish_shards(args, state, target):
    executor = getattr(args, "shard_prefetch", None)
    if executor is None:
        fill_shards(args, state, target)
        return
    pending = getattr(args, "shard_prefetch_job", None)
    if pending is not None:
        if not pending.done():
            return
        try:
            pending.result()
        except TRANSIENT as error:
            LOG.warning("Shard prefetch deferred: %s", error)
    args.shard_prefetch_job = executor.submit(fill_shards, args, state, target)


def reconcile_shards_async(api, args, state):
    """Release finished worker capacity before importing its large result files."""
    completed = False
    pending = getattr(args, "reconcile_future", None)
    cooldowns = getattr(args, "reconcile_cooldowns", {})
    args.reconcile_cooldowns = cooldowns
    if pending is not None and pending.done():
        try:
            completed = bool(pending.result())
        except TRANSIENT as error:
            cooldowns[args.reconcile_batch] = time.monotonic() + 15
            LOG.warning("Shard %s reconciliation deferred: %s", args.reconcile_batch, error)
        args.reconcile_future = None
    with closing(connect(state)) as db:
        for batch, in db.execute("SELECT batch FROM shard_leases WHERE launched=1").fetchall():
            try:
                job = api.call("/api/jobs/enum_fresh_" + batch)
            except HTTPError as error:
                if error.code != 404:
                    raise
                error.close()
                reconcile_batch(api, args, state, batch)
                continue
            if job["status"] in ("completed", "completed_with_errors", "failed", "cancelled"):
                with db:
                    db.execute("UPDATE shard_leases SET launched=2 WHERE batch=? AND launched=1", (batch,))
        if getattr(args, "reconcile_future", None) is None:
            cursor=getattr(args,'reconcile_cursor',0)
            # Rotate through finished shards even when a failed import takes
            # longer than its retry cooldown. Old failures cannot monopolize it.
            for batch,rowid in db.execute("""
                SELECT batch,rowid FROM shard_leases WHERE launched=2
                ORDER BY (rowid<=?),rowid
            """,(cursor,)).fetchall():
                if cooldowns.get(batch, 0) > time.monotonic():
                    continue
                args.reconcile_cursor=rowid
                args.reconcile_batch = batch
                args.reconcile_future = args.reconcile_executor.submit(reconcile_batch, api, args, state, batch)
                break
    return completed


def dispatch_shards(api, args, state):
    # Refill the ready queue while finished shards are being reconciled.
    previous_reserve = getattr(args, "shard_reserve", 0)
    if previous_reserve and getattr(args, "shard_prefetch", None) is not None:
        replenish_shards(args, state, previous_reserve)
    if getattr(args, "reconcile_executor", None) is not None:
        completed = reconcile_shards_async(api, args, state)
    else:
        with closing(connect(state)) as db:
            launched = db.execute("SELECT batch FROM shard_leases WHERE launched=1").fetchall()
        completed = False
        for batch, in launched:
            try:
                completed = bool(reconcile_batch(api, args, state, batch)) or completed
            except TRANSIENT as error:
                LOG.warning("Shard %s reconciliation deferred: %s", batch, error)
    if (args.root / "paused").exists() or (completed and getattr(args, "once", False)):
        return completed

    workers = api.call("/api/workers") or []
    eligible = [w for w in workers if w.get("wordpress_bulk") and w.get("enum_checkpoint")
                and w.get("enum_stream_receipts") and w["status"] in ("online", "busy")]
    args.eligible_workers = len(eligible)
    reserve = sum(min(SHARDS_PER_WORKER, w["max_tasks"]) if "fresh_wordpress_slots" in w else 1
                  for w in eligible)
    args.shard_reserve = reserve
    if not reserve:
        return completed
    jobs = api.call("/api/jobs?active=1") or []
    ordinary_pending=False
    for job in jobs:
        if job['status'] not in ('pending','queued') or job['id'].startswith('enum_fresh_'):continue
        # The compact active-jobs API deliberately omits names.
        details=job if job.get('name') else api.call('/api/jobs/'+job['id'])
        name=details.get('name','') if isinstance(details,dict) else ''
        if not name.startswith(('WordPress reverse:','WordPress restock:')):
            ordinary_pending=True;break
    if ordinary_pending:
        replenish_shards(args,state,reserve)
        return completed
    available = sum(w.get("fresh_wordpress_slots", int(
        w["status"] == "online" and not w["current_load"] and not w.get("processor_attempts")
        and w.get("resources", {}).get("cpu_percent", 0) <= 90
    )) for w in eligible)
    with closing(connect(state)) as db:
        inflight = db.execute("SELECT count(*) FROM shard_leases WHERE launched=1").fetchone()[0]
        free = max(0, min(available, reserve - inflight))
        if free:
            ready = db.execute("SELECT count(*) FROM shard_leases WHERE launched=0").fetchone()[0]
            if not ready:
                replenish_shards(args, state, reserve)
        allowed=source_policy.allowed(source_policy.read(args.root),args.root)
        slots=','.join('?' for _ in allowed)
        pending = db.execute(f"SELECT batch FROM shard_leases l WHERE launched=0 AND NOT EXISTS(SELECT 1 FROM shard_hosts h WHERE h.batch=l.batch AND h.source NOT IN ({slots})) ORDER BY rowid LIMIT ?",(*allowed,free)).fetchall() if allowed else []
        for batch, in pending:
            if (args.root / "paused").exists():
                break
            with db:
                db.execute("UPDATE shard_leases SET launched=1 WHERE batch=?", (batch,))
                assign_concurrency_trial(db, batch, args)
            reconcile_batch(api, args, state, batch)
    replenish_shards(args, state, reserve)
    return completed


def assign_concurrency_trial(db, batch, args):
    quota = getattr(args, "trial_shards", 0)
    if not quota:
        return
    counts = dict(db.execute("SELECT automatic,count(*) FROM concurrency_trials GROUP BY automatic"))
    options = [mode for mode in (0, 1) if counts.get(mode, 0) < quota]
    if options:
        minimum = min(counts.get(mode, 0) for mode in options)
        automatic = random.choice([mode for mode in options if counts.get(mode, 0) == minimum])
        db.execute("INSERT OR IGNORE INTO concurrency_trials(batch,threads,automatic) VALUES (?,?,?)",
                   (batch, min(MAX_THREADS_PER_SHARD, args.threads // SHARDS_PER_WORKER)
                    if automatic else MAX_THREADS_PER_SHARD, automatic))


def reconcile_batch(api, args, state, batch):
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
        if not exports.bind_batch(state, args.merged, batch):
            return
        with closing(connect(state)) as db:
            trial = db.execute("SELECT threads,automatic FROM concurrency_trials WHERE batch=?", (batch,)).fetchone()
        threads, automatic = trial or (args.threads, not getattr(args, "fixed_concurrency", False))
        if getattr(args, "shards", False):
            threads = min(threads if trial else threads // SHARDS_PER_WORKER, MAX_THREADS_PER_SHARD)
        result = api.call("/api/tools/launch", {"tool_id": "wordpress-bulk", "fresh_batch_id": batch,
                          "input_file": str(path), "options": {"threads": threads, "timeout": 5,
                          "auto_concurrency": bool(automatic)}})
        if result["job_id"] != ident:
            raise ValueError("CNC server does not support stable fresh batch IDs")
        LOG.info("Submitted %s", ident)
        return
    if job["status"] not in ("completed", "completed_with_errors", "failed"):
        if job["status"] == "cancelled":
            if getattr(args, "once", False):
                raise RuntimeError(f"Canary {ident} ended as {job['status']}")
            defer_batch(state, batch, path)
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
        {"id": t["id"], "job_id": t["job_id"], "status": t["status"], "result": t.get("result")} for t in tasks
    ]}), encoding="utf-8")
    try:
        processed, verified = bulk.completed_job_counts(metadata, allow_all_error_job=True)
        skipped = sum(((t.get("result") or {}).get("stderr") or "").splitlines()
                      .count("[WARN] Skipping invalid HTTP target") for t in tasks)
        if job["status"] == "failed":
            if not snapshot.is_file() or processed + skipped != len(set(bulk.attempted_hosts(snapshot))):
                raise ValueError("failed scanner counts do not cover the complete batch")
    except ValueError as error:
        if getattr(args, "once", False):
            raise
        try:
            verified = recover_partial_results(args, state, path, snapshot, output)
        except ValueError as invalid:
            LOG.error("Batch %s has unusable partial results: %s", ident, invalid)
            verified = set()
        defer_batch(state, batch, path, verified)
        LOG.warning("Batch %s retained %d streamed verifications; deferred unresolved hosts: %s",
                    ident, len(verified), error)
        return
    if not verified:
        output = args.root / (batch + ".empty")
        output.touch(exist_ok=True)
    elif not output.is_file():
        # Keep the job and input for recovery. A missing artifact is not an
        # empty result and should not hold up all future discovery.
        defer_batch(state, batch, path)
        LOG.error("Batch %s completed but its result is missing; retained the input and deferred it", ident)
        return
    with closing(connect(args.catalog)) as catalog:
        bulk.record_completed(catalog, path, output, processed, skipped, verified, snapshot, summarize=False)
    verified_hosts = set(bulk.attempted_hosts(output)) if verified else set()
    added = 0
    if verified:
        added = publish_merged(state, args.merged, [output], batch=batch)
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
        db.executemany("INSERT OR IGNORE INTO generator_checked VALUES (?)", hosts)
        db.executemany("DELETE FROM fresh WHERE host=?", hosts)
        db.executemany("UPDATE retries SET done=1 WHERE host=?", hosts)
        due = int(time.time()) + 86400
        db.executemany("INSERT OR IGNORE INTO retries (host,url,due) VALUES (?,?,?)",
                       ((host, url, due) for host, url in retries))
        rate = None
        elapsed = None
        if job.get("started_at") and job.get("completed_at"):
            elapsed = (datetime.fromisoformat(job["completed_at"]) - datetime.fromisoformat(job["started_at"])).total_seconds()
            if elapsed > 0:
                rate = round(processed / elapsed, 1)
        db.execute("UPDATE stats SET verified=verified+?, rate=? WHERE slot=1", (verified, rate))
        db.execute("INSERT OR IGNORE INTO publications VALUES (?,?,?)", (batch, int(time.time()), added))
        bucket = int(time.time()) // 1800
        yields = {}
        reverse_seeds = []
        for host, source in db.execute("SELECT host,source FROM shard_hosts WHERE batch=?", (batch,)):
            if host in accepted_hosts:
                checked, hits = yields.get(source, (0, 0))
                yields[source] = (checked + 1, hits + (host in verified_hosts))
                if host in verified_hosts:
                    reverse_seeds.append(host)
        reverse_seeds = list(set(reverse_seeds) | verified_hosts)
        if reverse_seeds:
            due = int(time.time())
            queue_verified_seeds(db, reverse_seeds, due)
        db.executemany("""
            INSERT INTO source_yield VALUES (?,?,?,?)
            ON CONFLICT(bucket,source) DO UPDATE SET checked=checked+excluded.checked, verified=verified+excluded.verified
        """, ((bucket, source, checked, hits) for source, (checked, hits) in yields.items()))
        if db.execute("SELECT 1 FROM concurrency_trials WHERE batch=?", (batch,)).fetchone():
            outcomes = {}
            for task in tasks:
                stderr = task["result"].get("stderr") or ""
                for line in re.findall(r"^\[\*\] homepage outcomes(.*)$", stderr, re.MULTILINE):
                    for reason, count in re.findall(r"(timeout|http_429)=(\d+)", line):
                        outcomes[reason] = outcomes.get(reason, 0) + int(count)
            db.execute("""
                UPDATE concurrency_trials SET status='completed', elapsed=?, checked=?, published=?,
                    timeouts=?, limited=?, archive_checked=?, worker=? WHERE batch=?
            """, (elapsed, processed, added, outcomes.get("timeout", 0), outcomes.get("http_429", 0),
                  yields.get("archive", (0, 0))[0], tasks[0].get("assigned_to"), batch))
        release_batch(db, batch)
    metadata.unlink(missing_ok=True)
    path.unlink(missing_ok=True)
    if not verified:
        output.unlink(missing_ok=True)
    LOG.info("Merged %s: %d verified of %d checked", ident, verified, processed)
    return True


def publish_status(root, state, eligible_workers=None):
    now = int(time.time())
    with closing(connect(state)) as db:
        ct_verified, ct_share = discovery_policy(db)
        feed_at, = db.execute("SELECT max(updated) FROM cursors").fetchone()
        oldest, = db.execute("SELECT min(seen) FROM fresh").fetchone()
        verified, rate = db.execute("SELECT verified,rate FROM stats WHERE slot=1").fetchone()
        active = db.execute("SELECT batch FROM active UNION ALL SELECT batch FROM shard_leases WHERE launched=1").fetchone()
        ready = db.execute("SELECT count(*) FROM shard_leases WHERE launched=0").fetchone()[0]
        available, started, refilling, pool_target = pool_snapshot(db)
        export_status = exports.status(db)
        worker_restock = None
        if db.execute("SELECT 1 FROM sqlite_schema WHERE name='worker_restock_jobs'").fetchone():
            active_restock = db.execute("SELECT job_id,paths FROM worker_restock_jobs WHERE phase!='done' ORDER BY created LIMIT 1").fetchone()
            waves, hosts = db.execute("SELECT count(*),coalesce(sum(hosts),0) FROM worker_restock_jobs WHERE phase='done'").fetchone()
            worker_restock = {'active_job':active_restock[0] if active_restock else None,
                              'wave_workers':restock.wave_workers(active_restock[1]) if active_restock else 0,
                              'completed_waves':waves,'candidates_queued':hosts}
        pool_sources=dict(db.execute('SELECT source,count(*) FROM ready_candidates GROUP BY source'))
        checked_sources={source:{'checked':checked,'verified':hits} for source,checked,hits in db.execute('SELECT source,sum(checked),sum(verified) FROM source_yield GROUP BY source')}
        reverse_status=None
        if db.execute("SELECT 1 FROM sqlite_schema WHERE name='reverse_worker_jobs'").fetchone():
            current=db.execute("SELECT job_id,assignments FROM reverse_worker_jobs WHERE phase!='done' ORDER BY created LIMIT 1").fetchone()
            waves,hosts=db.execute("SELECT count(*),coalesce(sum(hosts),0) FROM reverse_worker_jobs WHERE phase='done'").fetchone()
            reverse_status={'active_job':current[0] if current else None,'wave_workers':len(json.loads(current[1])) if current else 0,'completed_waves':waves,'unique_candidates_added':hosts}
        published, = db.execute("SELECT coalesce(sum(urls),0) FROM publications WHERE published>?",
                                (now - 60,)).fetchone()
    pause = root / "paused"
    policy=source_policy.read(root)
    ct_share=(CT_TARGET_SHARE if any(policy[k] for k in ('reverse_ip','commoncrawl','other_catalogs')) else 1.0) if policy['ct'] else 0.0
    reverse_share=REVERSE_TARGET_SHARE if policy['reverse_ip'] else 0.0
    auxiliary_share=max(0,1-reverse_share-ct_share) if policy['commoncrawl'] or policy['other_catalogs'] else 0.0
    status = {"ct_verified": ct_verified, "ct_target_share": ct_share, "reverse_target_share": reverse_share, "aux_target_share": auxiliary_share, "updated_at": now, "feed_at": feed_at, "oldest_fresh_at": oldest,
              "pool_available": available, "pool_started": bool(started), "pool_refilling": bool(refilling),
              "pool_target": pool_target, "pool_min": POOL_MIN, "pool_max": POOL_MAX,
              "pool_low": POOL_LOW, "shard_targets": SHARD_TARGETS,
              "wave_targets": SHARD_TARGETS * SHARDS_PER_WORKER * (eligible_workers or 0),
              "shards_per_worker": SHARDS_PER_WORKER,
              "merged_verified": export_status['session_verified'] if export_status else verified,
              "total_verified": verified, "hostnames_per_second": rate,
              "active_job": "enum_fresh_" + active[0] if active else None, "ready_shards": ready,
              "paused_at": int(pause.stat().st_mtime) if pause.exists() else None,
              "published_per_10s": round(published / 6, 1)}
    index_status=None
    try:index_status=json.loads((root/'index-status.json').read_text())
    except FileNotFoundError:pass
    status.update(commoncrawl_mode=source_policy.mode(root),commoncrawl_max_age_years=2,index_status=index_status,sources=source_policy.read(root),pool_sources=pool_sources,source_totals=checked_sources,reverse_worker_restock=reverse_status,source_policy_scope="pool_refill",reverse_seed_origin="non_reverse_inputs")
    if export_status:
        status.update(export_status)
    if worker_restock is not None:
        status['worker_restock'] = worker_restock
    if eligible_workers is not None:
        status["eligible_workers"] = eligible_workers
    temp = root / ".status.tmp"
    temp.write_text(json.dumps(status), encoding="utf-8")
    temp.replace(root / "status.json")
    return bool(active)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path("/opt/internet-map/data/discovery"))
    parser.add_argument("--catalog", type=Path, default=Path("/opt/internet-map/data/discovery/candidates.db"))
    parser.add_argument("--data", type=Path, default=Path("/opt/cnc/data"))
    parser.add_argument("--merged", type=Path, default=Path("/opt/cnc/merged"))
    parser.add_argument("--discovery-results", type=Path, help="JSONL emitted by the standalone discovery runner")
    parser.add_argument("--batch-limit", type=int, choices=range(1, SHARD_TARGETS + 1),
                        metavar=f"1..{SHARD_TARGETS}")
    parser.add_argument("--threads", type=int, choices=range(64, 1025), metavar="64..1024", default=64)
    parser.add_argument("--fixed-concurrency", action="store_true", help="Disable adaptive tuning for a benchmark control")
    parser.add_argument("--shards", action="store_true",
                        help=f"Dispatch independent {SHARD_TARGETS:,}-host batches from a persistent 5M pool")
    parser.add_argument("--once", action="store_true", help="Reconcile a single batch and exit")
    args = parser.parse_args()
    if args.batch_limit is None:
        args.batch_limit = SHARD_TARGETS if args.shards else 10_000
    args.pooled = args.shards
    args.trial_shards = max(0, min(16, int(os.environ.get("WP_CONCURRENCY_TRIAL_SHARDS", "0"))))
    logging.basicConfig(level=logging.INFO)
    api = CNC(os.environ["GO_API_URL"], os.environ["CNC_DASHBOARD_AUDIT_SECRET"])
    if not args.catalog.is_file():
        parser.error("existing candidate catalog is required")
    lock = (args.root / "generator.lock").open("a")
    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    state = args.root / "generator-state.db"
    initialize(state)
    initialize_catalog(args.catalog)
    exports.synchronize(state, args.merged)
    if args.shards:
        restage_oversized_shards(args, state)
        args.eligible_workers = sum(
            bool(w.get("wordpress_bulk") and w.get("enum_checkpoint") and w.get("enum_stream_receipts")
                 and w["status"] in ("online", "busy")) for w in (api.call("/api/workers") or [])
        )
        publish_status(args.root, state, args.eligible_workers)
    archive_legacy_outputs(args, state)
    reader = threading.Thread(target=feed, args=(state, args.catalog), daemon=True)
    reader.start()
    daily_pages = max(0, min(24, int(os.environ.get("WP_DORK_DAILY_PAGES", "0"))))
    token = os.environ.get("APIFY_TOKEN", "").strip()
    if args.shards and daily_pages and token:
        threading.Thread(target=dork_feed, args=(state, token, daily_pages), daemon=True).start()
    pool_reader = None
    if args.shards:
        args.shard_prefetch = concurrent.futures.ThreadPoolExecutor(max_workers=1)
        args.reconcile_executor = concurrent.futures.ThreadPoolExecutor(max_workers=1)
        args.shard_ready_event = threading.Event()
        # Managed workers expand the non-reverse input seeds; no random-IP feed.
        pool_reader = threading.Thread(target=pool_feed, args=(args, state), daemon=True)
        pool_reader.start()
        threading.Thread(target=cc_index.run, args=(args,state), daemon=True).start()
        threading.Thread(target=reverse_workers.run, args=(api, args, state), daemon=True).start()
        threading.Thread(target=restock.run, args=(api, args, state), kwargs={"allow_new":True}, daemon=True).start()
    dork_seed_signature = None
    while True:
        if args.shards:
            args.shard_ready_event.clear()
        if not reader.is_alive():
            raise RuntimeError("CT reader stopped")
        if pool_reader is not None and not pool_reader.is_alive():
            raise RuntimeError("Pool reader stopped")
        completed = False
        try:
            exports.synchronize(state, args.merged)
        except TRANSIENT as error:
            LOG.warning("Output session update deferred: %s", error)
        if args.shards and not (args.root / "paused").exists():
            try:
                dork_seed_signature = collect_dork_seeds(state, args.root / "dork-seeds.txt", dork_seed_signature)
            except TRANSIENT as error:
                LOG.warning("Dork seed import deferred: %s", error)
        try:
            if args.discovery_results and not (args.root / "paused").exists() and args.discovery_results.is_file():
                collect_discovery_results(state, args.discovery_results)
            completed = dispatch(api, args, state)
            if completed and args.once:
                return
        except TRANSIENT as error:
            LOG.warning("Dispatch deferred: %s", error)
        active = False
        try:
            active = publish_status(args.root, state, getattr(args, "eligible_workers", None))
        except TRANSIENT as error:
            LOG.warning("Status update deferred: %s", error)
        if not completed:
            # A background refill can finish while every worker is idle.
            dispatch_ready = args.shards and (
                active or (getattr(args, "eligible_workers", 0) and not (args.root / "paused").exists())
            )
            delay = 1 if dispatch_ready else 5 if args.shards or active else 15
            if args.shards:
                args.shard_ready_event.wait(delay)
            else:
                time.sleep(delay)


if __name__ == "__main__":
    main()
