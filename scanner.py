#!/usr/bin/env python3
"""High-concurrency WordPress discovery scanner & crawler for Internet Map."""
import argparse
import asyncio
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import re
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
        pid = data.get("pid")
        if pid:
            try:
                os.kill(pid, 0)
                data["running"] = True
            except OSError:
                data["running"] = False
        return data
    except Exception:
        return {"running": False, "scanned": 0, "wordpress_found": 0, "speed": 0.0, "last_url": ""}


def update_scanner_state(state_path: Path, state: dict):
    state_path.parent.mkdir(parents=True, exist_ok=True)
    tmp = state_path.with_suffix(".tmp")
    tmp.write_text(json.dumps(state, indent=2), encoding="utf-8")
    tmp.replace(state_path)


def extract_wordpress_evidence(html: str, headers: dict) -> list[str]:
    """Extract WordPress forensic evidence (version, themes, plugins, REST API)."""
    evidence = []
    if not html:
        return evidence

    # Check generator meta tag
    gen_match = re.search(
        r'<meta[^>]+name=["\']generator["\'][^>]+content=["\']WordPress\s*([^"\']*)["\']',
        html,
        re.IGNORECASE,
    )
    if gen_match:
        ver = gen_match.group(1).strip()
        evidence.append(f"WordPress {ver}" if ver else "WordPress generator tag")

    # Check wp-content or wp-includes in source
    has_wp_paths = ("wp-content" in html) or ("wp-includes" in html)
    if has_wp_paths:
        if not evidence:
            evidence.append("WordPress Core (wp-content/wp-includes)")

        # Detect active Theme
        theme_matches = re.findall(r'/wp-content/themes/([a-zA-Z0-9_\-\.]+)/', html)
        if theme_matches:
            # Exclude generic assets if any, pick dominant theme
            evidence.append(f"Theme: {theme_matches[0]}")

        # Detect installed Plugins (up to 3)
        plugin_matches = list(dict.fromkeys(re.findall(r'/wp-content/plugins/([a-zA-Z0-9_\-\.]+)/', html)))
        for plugin in plugin_matches[:3]:
            evidence.append(f"Plugin: {plugin}")

    # Check REST API link or link rel
    if 'rel="https://api.w.org/"' in html or "rel='https://api.w.org/'" in html or '/wp-json/' in html:
        evidence.append("wp-json REST API")

    # Check HTTP response headers
    if headers and isinstance(headers, dict):
        server = headers.get("server", "").lower()
        x_powered = headers.get("x-powered-by", "").lower()
        link_hdr = headers.get("link", "").lower()
        if "wordpress" in x_powered or "wordpress" in server:
            evidence.append(f"Server header: {x_powered or server}")
        if "api.w.org" in link_hdr:
            evidence.append("REST API Link header")

    return list(dict.fromkeys(evidence))


async def check_url(session, url: str, timeout_sec: float = 5.0):
    """Probe a URL for HTTP response and HTML contents."""
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
            # Read first 150 KB of HTML (enough for WordPress signatures & headers)
            chunk = await resp.content.read(150000)
            text = chunk.decode("utf-8", errors="replace")
            return url, status, headers, text
    except Exception:
        return url, 0, {}, ""


async def scan_worker(database_path: Path, concurrency: int = 100, limit: int = 0, state_path: Path = STATE_FILE):
    if aiohttp is None:
        raise RuntimeError("aiohttp is required for async scanner. Install with: pip install aiohttp")

    db = connect(database_path)
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

    # Find starting cursor URL
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

    connector = aiohttp.TCPConnector(
        limit=concurrency,
        limit_per_host=2,
        ssl=False,
        ttl_dns_cache=300,
        force_close=False,
        enable_cleanup_closed=True,
    )
    headers = {
        "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36 (compatible; InternetMap/2.0)",
        "Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
        "Accept-Language": "en-US,en;q=0.8",
    }

    batch_size = max(50, concurrency * 2)
    session_processed = 0
    t_start = time.time()
    last_update = t_start

    try:
        async with aiohttp.ClientSession(connector=connector, headers=headers) as session:
            while not stop_event.is_set():
                # Query next batch using cursor pagination on indexed url
                rows = db.execute(
                    "SELECT url FROM sites WHERE url > ? ORDER BY url LIMIT ?",
                    (current_cursor, batch_size),
                ).fetchall()

                if not rows:
                    # Wrapped around or reached the end of catalog
                    if current_cursor != "":
                        current_cursor = ""
                        continue
                    print("All catalog URLs have been scanned.")
                    break

                urls = [r[0] for r in rows]
                current_cursor = urls[-1]

                tasks = [check_url(session, u) for u in urls]
                results = await asyncio.gather(*tasks)

                # Process batch results in a single transaction
                now_str = datetime.now(timezone.utc).isoformat()
                with db:
                    for url, status_code, hdrs, html_str in results:
                        evidence = extract_wordpress_evidence(html_str, hdrs)
                        has_wp = 1 if len(evidence) > 0 else 0

                        # Record in scan_progress
                        db.execute(
                            """
                            INSERT OR REPLACE INTO scan_progress(url, status_code, has_wordpress, evidence, scanned_at)
                            VALUES (?, ?, ?, ?, ?)
                            """,
                            (url, status_code, has_wp, json.dumps(evidence), now_str),
                        )

                        if has_wp:
                            wp_total += 1
                            # Record in catalog observations table
                            db.execute(
                                """
                                INSERT OR REPLACE INTO observations(import_id, url, evidence)
                                VALUES (?, ?, ?)
                                """,
                                (import_id, url, json.dumps(evidence)),
                            )
                            # Index in page_contents if HTML was retrieved
                            if html_str:
                                try:
                                    index_page(db, url, html_str, status_code=status_code, headers=hdrs, scanned_at=now_str)
                                except Exception:
                                    pass

                scanned_total += len(results)
                session_processed += len(results)

                elapsed = time.time() - t_start
                speed = session_processed / elapsed if elapsed > 0 else 0.0

                if time.time() - last_update >= 1.5:
                    state["scanned"] = scanned_total
                    state["wordpress_found"] = wp_total
                    state["speed"] = round(speed, 1)
                    state["last_url"] = current_cursor
                    update_scanner_state(state_path, state)
                    last_update = time.time()
                    print(
                        f"[*] Scanned {scanned_total:,} URLs ({round(speed, 1)} req/s) | "
                        f"WordPress: {wp_total:,} | Cursor: {current_cursor}"
                    )

                if limit > 0 and session_processed >= limit:
                    break

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
    parser.add_argument("--concurrency", type=int, default=100)
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
