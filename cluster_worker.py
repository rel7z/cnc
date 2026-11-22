#!/usr/bin/env python3
"""Distributed cluster worker client for Internet Map."""
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

# Maximize file descriptor limits
try:
    soft, hard = resource.getrlimit(resource.RLIMIT_NOFILE)
    resource.setrlimit(resource.RLIMIT_NOFILE, (min(65535, hard), hard))
except Exception:
    pass

GEN_RE = re.compile(r'<meta[^>]+name=["\']generator["\'][^>]+content=["\']WordPress\s*([^"\']*)["\']', re.IGNORECASE)
THEME_RE = re.compile(r'/wp-content/themes/([a-zA-Z0-9_\-\.]+)/')
PLUGIN_RE = re.compile(r'/wp-content/plugins/([a-zA-Z0-9_\-\.]+)/')


def extract_wordpress_evidence(html: str, headers: dict) -> list[str]:
    """Fast extraction of WordPress forensic evidence."""
    if not html:
        return []

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

    gen_match = GEN_RE.search(html)
    if gen_match:
        ver = gen_match.group(1).strip()
        evidence.append(f"WordPress {ver}" if ver else "WordPress generator tag")

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

    if 'rel="https://api.w.org/"' in html or "rel='https://api.w.org/'" in html or '/wp-json/' in html:
        evidence.append("wp-json REST API")

    if hdr_has_wp:
        if "wordpress" in x_powered or "wordpress" in server:
            evidence.append(f"Server header: {x_powered or server}")
        if "api.w.org" in link_hdr:
            evidence.append("REST API Link header")

    return list(dict.fromkeys(evidence))


async def check_url(session, url: str, timeout_sec: float = 2.5):
    """Probe a single URL with fast connect timeout."""
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
            chunk = await resp.content.read(45000)
            text = chunk.decode("utf-8", errors="replace")
            return url, status, headers, text
    except Exception:
        return url, 0, {}, ""


async def run_worker(master_url: str, worker_id: str, concurrency: int = 500, batch_size: int = 2000, key: str = ""):
    if aiohttp is None:
        raise RuntimeError("aiohttp is required on worker node. Install with: pip install aiohttp")

    print(f"[*] Starting Distributed Worker: {worker_id}")
    print(f"    Master URL: {master_url}")
    print(f"    Concurrency: {concurrency}")
    print(f"    Batch Size: {batch_size}")

    stop_event = asyncio.Event()

    def handle_signal():
        print(f"\n[!] Signal received, shutting down worker {worker_id}...")
        stop_event.set()

    loop = asyncio.get_running_loop()
    for sig in (signal.SIGTERM, signal.SIGINT):
        try:
            loop.add_signal_handler(sig, handle_signal)
        except NotImplementedError:
            pass

    # Resolver & Connector
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
        "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36 (compatible; InternetMapWorker/2.0)",
        "Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
        "Accept-Language": "en-US,en;q=0.8",
    }
    api_headers = {
        "Content-Type": "application/json",
    }
    if key:
        api_headers["Authorization"] = f"Bearer {key}"

    total_scanned = 0
    total_wp = 0

    async with aiohttp.ClientSession(connector=connector, headers=headers) as probe_session, \
               aiohttp.ClientSession(headers=api_headers) as master_session:

        while not stop_event.is_set():
            # 1. Lease batch from Master
            lease_url = f"{master_url.rstrip('/')}/api/v1/cluster/lease"
            lease_payload = {
                "worker_id": worker_id,
                "capacity": batch_size,
            }
            try:
                async with master_session.post(lease_url, json=lease_payload, timeout=aiohttp.ClientTimeout(total=10)) as resp:
                    if resp.status != 200:
                        err_text = await resp.text()
                        print(f"[-] Master lease error ({resp.status}): {err_text[:100]}. Waiting 5s...")
                        await asyncio.sleep(5)
                        continue
                    lease_data = await resp.json()
            except Exception as e:
                print(f"[-] Connection to master failed ({e}). Retrying in 5s...")
                await asyncio.sleep(5)
                continue

            urls = lease_data.get("urls", [])
            lease_id = lease_data.get("lease_id")

            if not urls:
                print(f"[*] No unscanned URLs remaining on master. Waiting 10s...")
                await asyncio.sleep(10)
                continue

            t0 = time.time()
            print(f"[*] Leased batch #{lease_id} ({len(urls):,} URLs). Probing with {concurrency} workers...")

            # 2. Parallel Probe
            semaphore = asyncio.Semaphore(concurrency)

            async def bound_check(u):
                async with semaphore:
                    if stop_event.is_set():
                        return u, 0, {}, ""
                    return await check_url(probe_session, u)

            tasks = [bound_check(u) for u in urls]
            probe_results = await asyncio.gather(*tasks)

            # 3. Analyze Findings
            findings = []
            wp_in_batch = 0
            for u, status_code, hdrs, html_str in probe_results:
                ev = extract_wordpress_evidence(html_str, hdrs)
                has_wp = 1 if len(ev) > 0 else 0
                if has_wp:
                    wp_in_batch += 1
                findings.append({
                    "url": u,
                    "status_code": status_code,
                    "has_wp": has_wp,
                    "evidence": ev,
                })

            elapsed = max(0.01, time.time() - t0)
            rate = len(urls) / elapsed
            total_scanned += len(urls)
            total_wp += wp_in_batch

            print(
                f"[+] Completed batch #{lease_id} in {elapsed:.1f}s ({rate:.1f} req/s) | "
                f"WordPress in batch: {wp_in_batch} | Total WP: {total_wp:,}"
            )

            # 4. Submit Results to Master
            submit_url = f"{master_url.rstrip('/')}/api/v1/cluster/submit"
            submit_payload = {
                "lease_id": lease_id,
                "worker_id": worker_id,
                "speed": round(rate, 1),
                "results": findings,
            }

            try:
                async with master_session.post(submit_url, json=submit_payload, timeout=aiohttp.ClientTimeout(total=20)) as resp:
                    if resp.status != 200:
                        print(f"[-] Warning: Master rejected submit ({resp.status}): {await resp.text()}")
                    else:
                        sub_data = await resp.json()
                        print(f"[*] Batch #{lease_id} committed to master DB: {sub_data.get('committed', 0)} rows.")
            except Exception as e:
                print(f"[-] Error submitting results to master: {e}")

    print(f"[*] Worker {worker_id} stopped cleanly. Total scanned: {total_scanned:,}, Total WP: {total_wp:,}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--master", default="http://10.104.0.2:3500", help="Master controller API URL")
    parser.add_argument("--worker-id", default="", help="Unique worker node identifier (defaults to hostname)")
    parser.add_argument("--concurrency", type=int, default=500, help="Concurrent async HTTP probes")
    parser.add_argument("--batch-size", type=int, default=2000, help="URLs leased per batch")
    parser.add_argument("--key", default=os.environ.get("INTERNET_MAP_API_KEY", "OcoAGX2Yo5kgE-yw9TKSj04GD01QlgU0703tlkXHKw0"))
    args = parser.parse_args()

    worker_id = args.worker_id or os.uname().nodename
    asyncio.run(
        run_worker(
            master_url=args.master,
            worker_id=worker_id,
            concurrency=args.concurrency,
            batch_size=args.batch_size,
            key=args.key,
        )
    )


if __name__ == "__main__":
    main()
