#!/usr/bin/env python3
"""High-performance async pipeline WordPress discovery scanner for Internet Map."""
import argparse
import asyncio
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import re
import resource
import signal
import sys
import time

try:
    import aiohttp
except ImportError:
    aiohttp = None

from main import DEFAULT_DATABASE, connect
from intel import init_intel_tables, index_page

STATE_FILE = Path(__file__).resolve().parent / "data/scanner-state.json"

# Optimize OS file descriptor limits
try:
    soft, hard = resource.getrlimit(resource.RLIMIT_NOFILE)
    resource.setrlimit(resource.RLIMIT_NOFILE, (min(65535, hard), hard))
except Exception:
    pass


def is_pid_alive(pid: int) -> bool:
    if not pid or pid <= 0:
        return False
    try:
        os.kill(pid, 0)
    except OSError:
        return False
    stat_file = Path(f"/proc/{pid}/stat")
    if stat_file.exists():
        try:
            fields = stat_file.read_text().split()
            if len(fields) >= 3 and fields[2] == "Z":
                return False
        except Exception:
            pass
    return True


def init_scanner_tables(db):
    """Ensure scan_progress table and indices exist."""
    db.executescript("""
        CREATE TABLE IF NOT EXISTS scan_progress (
            url TEXT PRIMARY KEY,
            status_code INTEGER DEFAULT 0,
            has_wordpress INTEGER DEFAULT 0,
            evidence TEXT DEFAULT '[]',
            scanned_at TEXT NOT NULL
        );
        CREATE INDEX IF NOT EXISTS scan_progress_wp ON scan_progress(has_wordpress) WHERE has_wordpress = 1;
    """)


def get_scanner_state(state_path: Path = STATE_FILE) -> dict:
    if not state_path.exists():
        return {"running": False, "scanned": 0, "wordpress_found": 0, "speed": 0.0, "last_url": ""}
    try:
        data = json.loads(state_path.read_text(encoding="utf-8"))
        if not data.get("running", False):
            return data
        pid = data.get("pid")
        if pid and not is_pid_alive(pid):
            data["running"] = False
            data["speed"] = 0.0
            update_scanner_state(state_path, data)
        return data
    except Exception:
        return {"running": False, "scanned": 0, "wordpress_found": 0, "speed": 0.0, "last_url": ""}


def update_scanner_state(state_path: Path, state: dict):
    state_path.parent.mkdir(parents=True, exist_ok=True)
    tmp = state_path.with_suffix(".tmp")
    tmp.write_text(json.dumps(state, indent=2), encoding="utf-8")
    tmp.replace(state_path)


# Precompiled fast regexes
GEN_RE = re.compile(r'<meta[^>]+name=["\']generator["\'][^>]+content=["\']WordPress\s*([^"\']*)["\']', re.IGNORECASE)
THEME_RE = re.compile(r'/wp-content/themes/([a-zA-Z0-9_\-\.]+)/')
PLUGIN_RE = re.compile(r'/wp-content/plugins/([a-zA-Z0-9_\-\.]+)/')


def extract_wordpress_evidence(html: str, headers: dict) -> list[str]:
    """Fast extraction of WordPress forensic evidence."""
    if not html:
        return []

    # Quick check bailout to skip 99% of regexes on non-WordPress sites
    has_wp_keyword = ("wp-" in html) or ("WordPress" in html) or ("wordpress" in html)
    hdr_has_wp = False
    server = ""
    x_powered = ""
    link_hdr = ""

    if headers and isinstance(headers, dict):
        server = headers.get("server", "").lower()
        x_powered = headers.get("x-powered-by", "").lower()
        link_hdr = headers.get("link", "").lower()
        if "wordpress" in x_powered or "wordpress" in server or "api.w.org" in link_hdr:
            hdr_has_wp = True

    if not has_wp_keyword and not hdr_has_wp:
        return []

    evidence = []

    # Check generator meta tag
    gen_match = GEN_RE.search(html)
    if gen_match:
        ver = gen_match.group(1).strip()
        evidence.append(f"WordPress {ver}" if ver else "WordPress generator tag")

    # Check wp-content or wp-includes in source
    has_wp_paths = ("wp-content" in html) or ("wp-includes" in html)
    if has_wp_paths:
        if not evidence:
            evidence.append("WordPress Core (wp-content/wp-includes)")

        theme_matches = THEME_RE.findall(html)
        if theme_matches:
            evidence.append(f"Theme: {theme_matches[0]}")

        plugin_matches = list(dict.fromkeys(PLUGIN_RE.findall(html)))
        for plugin in plugin_matches[:3]:
            evidence.append(f"Plugin: {plugin}")

    # Check REST API link or link rel
    if 'rel="https://api.w.org/"' in html or "rel='https://api.w.org/'" in html or '/wp-json/' in html:
        evidence.append("wp-json REST API")

    if hdr_has_wp:
        if "wordpress" in x_powered or "wordpress" in server:
            evidence.append(f"Server header: {x_powered or server}")
        if "api.w.org" in link_hdr:
            evidence.append("REST API Link header")

    return list(dict.fromkeys(evidence))


PRIORITY_PREFIXES = [
    "blog", "foundation", "community", "news", "church", "health", "project",
    "academy", "center", "institute", "society", "club", "charity", "world",
    "art", "music", "youth", "green", "eco", "open", "free", "global", "action",
    "hope", "life", "care", "support", "trust", "group", "team", "network",
    "press", "portal", "mag", "online", "media", "forum", "tech", "edu",
    "school", "kids", "nature", "voice", "radio", "travel", "food", "farm"
]


def fetch_next_urls_batch(db, batch_size=100, prefix_index=0):
    """Fetch next batch of unscanned URLs, prioritizing high-value domains."""
    if prefix_index < len(PRIORITY_PREFIXES):
        pfx = PRIORITY_PREFIXES[prefix_index]
        rows = db.execute("""
            SELECT s.url FROM sites s
            LEFT JOIN scan_progress p ON p.url = s.url
            WHERE p.url IS NULL AND s.host LIKE ?
            LIMIT ?
        """, (f"{pfx}%", batch_size)).fetchall()
        if rows:
            return [r[0] for r in rows], prefix_index
        prefix_index += 1

    rows = db.execute("""
        SELECT s.url FROM sites s
        LEFT JOIN scan_progress p ON p.url = s.url
        WHERE p.url IS NULL
        LIMIT ?
    """, (batch_size,)).fetchall()
    return [r[0] for r in rows], prefix_index


async def check_url(session, url: str, timeout_sec: float = 2.5):
    """Probe a URL with ultra-fast timeout (1.2s connect / 2.5s total)."""
    try:
        async with session.get(
            url,
            timeout=aiohttp.ClientTimeout(total=timeout_sec, connect=1.2, sock_read=1.5),
            ssl=False,
            allow_redirects=True,
            max_redirects=2,
        ) as resp:
            status = resp.status
            headers = {k.lower(): v for k, v in resp.headers.items()}
            # Read first 45 KB of HTML (sufficient for meta tags and header links)
            chunk = await resp.content.read(45000)
            text = chunk.decode("utf-8", errors="replace")
            return url, status, headers, text
    except Exception:
        return url, 0, {}, ""


async def scan_worker(database_path: Path, concurrency: int = 500, limit: int = 0, state_path: Path = STATE_FILE):
    if aiohttp is None:
        raise RuntimeError("aiohttp is required for async scanner. Install with: pip install aiohttp")

    db = connect(database_path)
    # SQLite optimization pragmas
    db.execute("PRAGMA synchronous = NORMAL")
    db.execute("PRAGMA cache_size = -64000")
    db.execute("PRAGMA temp_store = MEMORY")
    db.execute("PRAGMA busy_timeout = 15000")

    init_intel_tables(db)
    init_scanner_tables(db)

    now_iso = datetime.now(timezone.utc).isoformat()
    with db:
        import_id = db.execute(
            "INSERT INTO imports(source, imported_at) VALUES (?, ?)",
            (f"wordpress-scanner-{now_iso[:10]}", now_iso),
        ).lastrowid

    # Existing counts
    scanned_total = db.execute("SELECT count(*) FROM scan_progress").fetchone()[0]
    wp_total = db.execute("SELECT count(*) FROM scan_progress WHERE has_wordpress = 1").fetchone()[0]

    last_url_row = db.execute("SELECT coalesce(max(url), '') FROM scan_progress").fetchone()
    current_cursor = last_url_row[0] if last_url_row else ""

    state = {
        "running": True,
        "pid": os.getpid(),
        "started_at": now_iso,
        "scanned": scanned_total,
        "wordpress_found": wp_total,
        "speed": 0.0,
        "last_url": current_cursor,
    }
    update_scanner_state(state_path, state)

    stop_event = asyncio.Event()

    def handle_signal():
        stop_event.set()

    loop = asyncio.get_running_loop()
    for sig in (signal.SIGTERM, signal.SIGINT):
        try:
            loop.add_signal_handler(sig, handle_signal)
        except NotImplementedError:
            pass

    # Use aiodns async resolver if available
    resolver = None
    try:
        resolver = aiohttp.AsyncResolver(nameservers=["10.104.15.254", "1.1.1.1", "8.8.8.8"])
    except Exception:
        pass

    connector = aiohttp.TCPConnector(
        limit=concurrency,
        limit_per_host=1,
        resolver=resolver,
        ssl=False,
        ttl_dns_cache=600,
        force_close=False,
        enable_cleanup_closed=True,
    )
    headers = {
        "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36 (compatible; InternetMap/2.0)",
        "Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
        "Accept-Language": "en-US,en;q=0.8",
    }

    url_queue = asyncio.Queue(maxsize=concurrency * 3)
    result_queue = asyncio.Queue(maxsize=concurrency * 3)

    session_processed = 0
    t_start = time.time()
    last_update = t_start

    # Producer coroutine: fills URL queue from DB cursor
    async def producer():
        nonlocal current_cursor
        batch_fetch = max(500, concurrency * 2)
        while not stop_event.is_set():
            if url_queue.qsize() < concurrency:
                # Fetch next batch from SQLite
                rows = db.execute(
                    "SELECT url FROM sites WHERE url > ? ORDER BY url LIMIT ?",
                    (current_cursor, batch_fetch),
                ).fetchall()

                if not rows:
                    if current_cursor != "":
                        current_cursor = ""
                        continue
                    break

                for r in rows:
                    if stop_event.is_set():
                        break
                    await url_queue.put(r[0])
                    current_cursor = r[0]

            await asyncio.sleep(0.05)

    # Worker coroutine: processes individual URLs continuously
    async def worker(session):
        while not stop_event.is_set():
            try:
                url = await asyncio.wait_for(url_queue.get(), timeout=0.5)
            except asyncio.TimeoutError:
                continue
            except asyncio.CancelledError:
                break

            try:
                res = await check_url(session, url)
                await result_queue.put(res)
            except Exception:
                await result_queue.put((url, 0, {}, ""))
            finally:
                url_queue.task_done()

    # Writer coroutine: batch-commits results to SQLite and updates state
    async def writer():
        nonlocal scanned_total, wp_total, session_processed, last_update
        buffer = []

        while not stop_event.is_set() or not result_queue.empty():
            try:
                item = await asyncio.wait_for(result_queue.get(), timeout=0.2)
                buffer.append(item)
                result_queue.task_done()
            except asyncio.TimeoutError:
                pass

            now = time.time()
            if buffer and (len(buffer) >= 200 or now - last_update >= 1.0 or stop_event.is_set()):
                batch_to_write = buffer
                buffer = []
                now_str = datetime.now(timezone.utc).isoformat()

                with db:
                    for url, status_code, hdrs, html_str in batch_to_write:
                        evidence = extract_wordpress_evidence(html_str, hdrs)
                        has_wp = 1 if len(evidence) > 0 else 0

                        db.execute(
                            """
                            INSERT OR REPLACE INTO scan_progress(url, status_code, has_wordpress, evidence, scanned_at)
                            VALUES (?, ?, ?, ?, ?)
                            """,
                            (url, status_code, has_wp, json.dumps(evidence), now_str),
                        )

                        if has_wp:
                            wp_total += 1
                            db.execute(
                                """
                                INSERT OR REPLACE INTO observations(import_id, url, evidence)
                                VALUES (?, ?, ?)
                                """,
                                (import_id, url, json.dumps(evidence)),
                            )
                            if html_str:
                                try:
                                    index_page(db, url, html_str, status_code=status_code, headers=hdrs, scanned_at=now_str)
                                except Exception:
                                    pass

                scanned_total += len(batch_to_write)
                session_processed += len(batch_to_write)

                elapsed = now - t_start
                speed = session_processed / elapsed if elapsed > 0 else 0.0

                if now - last_update >= 1.0:
                    state["scanned"] = scanned_total
                    state["wordpress_found"] = wp_total
                    state["speed"] = round(speed, 1)
                    state["last_url"] = current_cursor
                    update_scanner_state(state_path, state)
                    last_update = now
                    print(
                        f"[*] High-Speed Scanner: {scanned_total:,} URLs scanned ({round(speed, 1)} req/s) | "
                        f"WordPress: {wp_total:,} | Cursor: {current_cursor}"
                    )

                if limit > 0 and session_processed >= limit:
                    stop_event.set()
                    break

    try:
        async with aiohttp.ClientSession(connector=connector, headers=headers) as session:
            producer_task = asyncio.create_task(producer())
            writer_task = asyncio.create_task(writer())
            worker_tasks = [asyncio.create_task(worker(session)) for _ in range(concurrency)]

            while not stop_event.is_set():
                if limit > 0 and session_processed >= limit:
                    break
                await asyncio.sleep(0.5)

            stop_event.set()
            producer_task.cancel()
            for wt in worker_tasks:
                wt.cancel()

            # Finish remaining writer items
            try:
                await asyncio.wait_for(writer_task, timeout=3.0)
            except Exception:
                pass

    finally:
        state["running"] = False
        state["speed"] = 0.0
        state["scanned"] = scanned_total
        state["wordpress_found"] = wp_total
        update_scanner_state(state_path, state)
        db.close()
        print(f"Scanner finished. Processed: {session_processed}, WordPress found: {wp_total}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("url", nargs="?", help="Optional single URL to scan immediately")
    parser.add_argument("--database", type=Path, default=DEFAULT_DATABASE)
    parser.add_argument("--concurrency", type=int, default=500)
    parser.add_argument("--limit", type=int, default=0, help="Stop after scanning N URLs (0 = unlimited)")
    parser.add_argument("--status", action="store_true", help="Print current scanner status")
    args = parser.parse_args()

    state_path = args.database.parent / "scanner-state.json"

    if args.status:
        st = get_scanner_state(state_path)
        print(json.dumps(st, indent=2))
        return

    if args.url:
        print(f"[*] Scanning {args.url} ...")
        db = connect(args.database)
        init_intel_tables(db)
        init_scanner_tables(db)
        try:
            import urllib.request
            req = urllib.request.Request(args.url, headers={"User-Agent": "Mozilla/5.0"})
            with urllib.request.urlopen(req, timeout=8) as resp:
                status = resp.status
                headers = dict(resp.headers)
                html = resp.read().decode("utf-8", errors="replace")
            evidence = extract_wordpress_evidence(html, headers)
            print(f"[+] URL: {args.url}")
            print(f"    Status: {status}")
            print(f"    WordPress Evidence: {evidence or 'None'}")
        except Exception as e:
            print(f"[-] Error: {e}")
            sys.exit(1)
        finally:
            db.close()
        return

    asyncio.run(
        scan_worker(
            args.database,
            concurrency=args.concurrency,
            limit=args.limit,
            state_path=state_path,
        )
    )


if __name__ == "__main__":
    main()
