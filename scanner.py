#!/usr/bin/env python3
"""High-throughput HTML Intelligence scanner & crawler for Internet Map (PublicWWW style)."""
import argparse
import asyncio
import json
import os
from pathlib import Path
import signal
import sys
import time
from datetime import datetime, timezone

try:
    import aiohttp
except ImportError:
    aiohttp = None

from main import DEFAULT_DATABASE, connect
from intel import init_intel_tables, index_pages_batch, fetch_and_index_url

STATE_FILE = Path(__file__).resolve().parent / "data/scanner-state.json"

PRIORITY_PREFIXES = [
    "blog", "foundation", "community", "news", "church", "health", "project",
    "academy", "center", "institute", "society", "club", "charity", "world",
    "art", "music", "youth", "green", "eco", "open", "free", "global", "action",
    "hope", "life", "care", "support", "trust", "group", "team", "network",
    "press", "portal", "mag", "online", "media", "forum", "tech", "edu",
    "school", "kids", "nature", "voice", "radio", "travel", "food", "farm"
]


def is_pid_alive(pid: int) -> bool:
    if not pid or pid <= 0:
        return False
    try:
        res, _ = os.waitpid(pid, os.WNOHANG)
        if res == pid:
            return False
    except ChildProcessError:
        pass
    except OSError:
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


def get_scanner_state(state_path: Path = STATE_FILE) -> dict:
    if not state_path.exists():
        return {"running": False, "scanned": 0, "html_indexed": 0, "wordpress_found": 0, "speed": 0.0, "last_url": ""}
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
        return {"running": False, "scanned": 0, "html_indexed": 0, "wordpress_found": 0, "speed": 0.0, "last_url": ""}


def update_scanner_state(state_path: Path, state: dict):
    state_path.parent.mkdir(parents=True, exist_ok=True)
    tmp = state_path.with_suffix(".tmp")
    tmp.write_text(json.dumps(state, indent=2), encoding="utf-8")
    tmp.replace(state_path)


async def check_url(session, url: str, timeout_sec: float = 5.0):
    """Fetch website HTML with short timeout and redirect handling."""
    try:
        async with session.get(
            url,
            timeout=aiohttp.ClientTimeout(total=timeout_sec, connect=3.0),
            ssl=False,
            allow_redirects=True,
            max_redirects=3,
        ) as resp:
            status = resp.status
            headers = {k.lower(): v for k, v in resp.headers.items()}
            # Read first 180 KB of HTML (covers complete header, body, scripts, and footer)
            chunk = await resp.content.read(180000)
            text = chunk.decode("utf-8", errors="replace")
            return url, status, headers, text
    except Exception:
        return url, 0, {}, ""


def fetch_next_urls_batch(db, batch_size=100, prefix_index=0):
    """Fetch next batch of unscanned URLs, prioritizing high-value domains."""
    # First try by rotating through PRIORITY_PREFIXES
    if prefix_index < len(PRIORITY_PREFIXES):
        pfx = PRIORITY_PREFIXES[prefix_index]
        rows = db.execute("""
            SELECT s.url FROM sites s
            LEFT JOIN page_contents p ON p.url = s.url
            WHERE p.url IS NULL AND s.host LIKE ?
            LIMIT ?
        """, (f"{pfx}%", batch_size)).fetchall()
        if rows:
            return [r[0] for r in rows], prefix_index
        # If this prefix is exhausted, move to next
        prefix_index += 1

    # Fallback to general natural language domains
    rows = db.execute("""
        SELECT s.url FROM sites s
        LEFT JOIN page_contents p ON p.url = s.url
        WHERE p.url IS NULL
          AND s.host NOT LIKE '%--%'
          AND s.host NOT GLOB '[0-9-]*'
        LIMIT ?
    """, (batch_size,)).fetchall()
    if rows:
        return [r[0] for r in rows], prefix_index

    # Final fallback to any unscanned domain
    rows = db.execute("""
        SELECT s.url FROM sites s
        LEFT JOIN page_contents p ON p.url = s.url
        WHERE p.url IS NULL
        LIMIT ?
    """, (batch_size,)).fetchall()
    return [r[0] for r in rows], prefix_index


async def scan_worker(database_path: Path, concurrency: int = 100, limit: int = 0, state_path: Path = STATE_FILE):
    if aiohttp is None:
        raise RuntimeError("aiohttp is required for async scanner. Install with: pip install aiohttp")

    db = connect(database_path)
    init_intel_tables(db)

    now_iso = datetime.now(timezone.utc).isoformat()
    with db:
        import_id = db.execute(
            "INSERT INTO imports(source, imported_at) VALUES (?, ?)",
            (f"intel-crawler-{now_iso[:10]}", now_iso)
        ).lastrowid

    # Existing counts
    total_indexed = db.execute("SELECT count(*) FROM page_contents").fetchone()[0]
    total_wp = db.execute("SELECT count(*) FROM page_contents WHERE signatures_json LIKE '%WordPress%'").fetchone()[0]

    state = {
        "running": True,
        "pid": os.getpid(),
        "started_at": now_iso,
        "scanned": 0,
        "html_indexed": total_indexed,
        "wordpress_found": total_wp,
        "speed": 0.0,
        "last_url": "",
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

    connector = aiohttp.TCPConnector(
        limit=concurrency,
        limit_per_host=2,
        ssl=False,
        ttl_dns_cache=300,
        force_close=False,
    )
    headers = {
        "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36 (compatible; InternetMapIntel/1.0)",
        "Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
        "Accept-Language": "en-US,en;q=0.5",
    }

    batch_size = concurrency
    total_processed = 0
    prefix_idx = 0
    t_start = time.time()
    last_update = t_start

    print(f"[*] Starting high-throughput HTML crawler (concurrency={concurrency}, limit={limit or 'unlimited'})...", flush=True)

    try:
        async with aiohttp.ClientSession(connector=connector, headers=headers) as session:
            while not stop_event.is_set():
                urls, prefix_idx = fetch_next_urls_batch(db, batch_size=batch_size, prefix_index=prefix_idx)
                if not urls:
                    print("[+] All catalog domains have been scanned!", flush=True)
                    break

                tasks = [check_url(session, u) for u in urls]
                results = await asyncio.gather(*tasks)

                # Prepare records for batch write
                valid_records = []
                now_str = datetime.now(timezone.utc).isoformat()
                wp_batch_urls = []

                for url, status_code, hdrs, html_str in results:
                    if status_code in (200, 301, 302, 403) and html_str and len(html_str) > 100:
                        valid_records.append({
                            "url": url,
                            "html": html_str,
                            "status_code": status_code,
                            "headers": hdrs,
                            "scanned_at": now_str,
                        })

                if valid_records:
                    meta_list = index_pages_batch(db, valid_records)
                    for meta in meta_list:
                        if "WordPress" in meta["signatures"]:
                            wp_batch_urls.append(meta["url"])

                    total_indexed += len(meta_list)
                    total_wp += len(wp_batch_urls)

                    if wp_batch_urls:
                        with db:
                            for wp_url in wp_batch_urls:
                                db.execute("""
                                    INSERT OR REPLACE INTO observations(import_id, url, evidence)
                                    VALUES (?, ?, ?)
                                """, (import_id, wp_url, json.dumps(["WordPress detected by HTML Intel"])))

                total_processed += len(results)
                elapsed = time.time() - t_start
                speed = total_processed / elapsed if elapsed > 0 else 0.0

                if time.time() - last_update >= 2.0:
                    state["scanned"] = total_processed
                    state["html_indexed"] = total_indexed
                    state["wordpress_found"] = total_wp
                    state["speed"] = round(speed, 1)
                    state["last_url"] = urls[-1]
                    update_scanner_state(state_path, state)
                    last_update = time.time()
                    print(f"[*] Crawled: {total_processed} | Active HTML: {total_indexed} | WordPress: {total_wp} | Speed: {round(speed, 1)} req/s", flush=True)

                if limit > 0 and total_processed >= limit:
                    break

    finally:
        state["running"] = False
        state["speed"] = 0.0
        update_scanner_state(state_path, state)
        db.close()
        print(f"[+] Crawler stopped. Total processed: {total_processed}, HTML indexed: {total_indexed}, WordPress found: {total_wp}", flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("url", nargs="?", help="Optional single URL to scan immediately")
    parser.add_argument("--database", type=Path, default=DEFAULT_DATABASE)
    parser.add_argument("--concurrency", type=int, default=100)
    parser.add_argument("--limit", type=int, default=0, help="Stop after scanning N URLs (0 = continuous)")
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
        try:
            res = fetch_and_index_url(db, args.url)
            print(f"[+] Success! URL: {res['url']}")
            print(f"    Host: {res['host']}")
            print(f"    Title: {res['title']}")
            print(f"    Status: {res['status_code']}")
            print(f"    Size: {res['content_length']} bytes")
            print(f"    Signatures: {', '.join(res['signatures']) or 'None'}")
        except Exception as e:
            print(f"[-] Error: {e}")
            sys.exit(1)
        finally:
            db.close()
        return

    asyncio.run(scan_worker(args.database, concurrency=args.concurrency, limit=args.limit, state_path=state_path))


if __name__ == "__main__":
    main()
