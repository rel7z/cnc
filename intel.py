#!/usr/bin/env python3
"""Source code and HTML intelligence engine for Internet Map (PublicWWW style)."""
from datetime import datetime, timezone
import html as html_module
import json
import re
import sqlite3
from urllib.parse import urlsplit
import urllib.request
import urllib.error

from main import normalize

# Common web technology signatures (regex or substrings)
SIGNATURE_RULES = [
    ("WordPress", [
        (r'wp-content/', re.IGNORECASE),
        (r'wp-includes/', re.IGNORECASE),
        (r'<meta[^>]+name=["\']generator["\'][^>]+content=["\']WordPress', re.IGNORECASE),
    ]),
    ("Google Analytics", [
        (r'gtag\s*\(\s*["\']config["\']', re.IGNORECASE),
        (r'google-analytics\.com/(?:ga|analytics)\.js', re.IGNORECASE),
        (r'_gaq\.push', re.IGNORECASE),
        (r'["\']UA-\d+-\d+["\']', re.IGNORECASE),
        (r'["\']G-[A-Z0-9]+["\']', 0),
    ]),
    ("Google Tag Manager", [
        (r'googletagmanager\.com/gtm\.js', re.IGNORECASE),
        (r'["\']GTM-[A-Z0-9]+["\']', 0),
    ]),
    ("Google AdSense", [
        (r'adsbygoogle', re.IGNORECASE),
        (r'pagead2\.googlesyndication\.com/pagead/js/adsbygoogle\.js', re.IGNORECASE),
        (r'pub-\d{16}', re.IGNORECASE),
    ]),
    ("Cloudflare", [
        (r'cdnjs\.cloudflare\.com', re.IGNORECASE),
        (r'challenges\.cloudflare\.com/turnstile', re.IGNORECASE),
        (r'cloudflare-static', re.IGNORECASE),
    ]),
    ("Shopify", [
        (r'cdn\.shopify\.com', re.IGNORECASE),
        (r'Shopify\.theme', re.IGNORECASE),
        (r'myshopify\.com', re.IGNORECASE),
    ]),
    ("Meta / Facebook Pixel", [
        (r'connect\.facebook\.net/[a-zA-Z_]+/fbevents\.js', re.IGNORECASE),
        (r'fbq\s*\(\s*["\']init["\']', re.IGNORECASE),
    ]),
    ("React / Next.js", [
        (r'__NEXT_DATA__', 0),
        (r'data-reactroot', 0),
        (r'/_next/static/', re.IGNORECASE),
    ]),
    ("Vue / Nuxt", [
        (r'__NUXT__', 0),
        (r'data-v-[a-f0-9]{6,10}', re.IGNORECASE),
    ]),
    ("Tailwind CSS", [
        (r'tailwindcss', re.IGNORECASE),
    ]),
    ("Bootstrap", [
        (r'bootstrap(?:\.bundle)?(?:\.min)?\.(?:css|js)', re.IGNORECASE),
    ]),
    ("jQuery", [
        (r'jquery(?:\.min)?\.js', re.IGNORECASE),
    ]),
    ("FontAwesome", [
        (r'font-?awesome', re.IGNORECASE),
    ]),
    ("Stripe", [
        (r'js\.stripe\.com/v[23]', re.IGNORECASE),
    ]),
    ("PayPal", [
        (r'paypal\.com/sdk/js', re.IGNORECASE),
    ]),
    ("Hotjar", [
        (r'static\.hotjar\.com/c/hotjar-', re.IGNORECASE),
    ]),
    ("Microsoft Clarity", [
        (r'clarity\.ms/tag/', re.IGNORECASE),
    ]),
    ("WooCommerce", [
        (r'woocommerce', re.IGNORECASE),
        (r'wc-ajax=', re.IGNORECASE),
    ]),
    ("Squarespace", [
        (r'static1\.squarespace\.com', re.IGNORECASE),
        (r'squarespace-core', re.IGNORECASE),
    ]),
    ("Wix", [
        (r'wix\.com/', re.IGNORECASE),
        (r'parastorage\.com', re.IGNORECASE),
    ]),
    ("Webflow", [
        (r'assets\.website-files\.com', re.IGNORECASE),
        (r'wf-page', re.IGNORECASE),
    ]),
    ("Drupal", [
        (r'Drupal\.settings', 0),
        (r'sites/all/themes', re.IGNORECASE),
        (r'sites/default/files', re.IGNORECASE),
    ]),
    ("Joomla", [
        (r'<meta[^>]+content=["\']Joomla', re.IGNORECASE),
        (r'templates/[^"\']+/css', re.IGNORECASE),
    ]),
    ("Intercom", [
        (r'widget\.intercom\.io', re.IGNORECASE),
        (r'Intercom\s*\(\s*["\']boot["\']', re.IGNORECASE),
    ]),
    ("HubSpot", [
        (r'js\.hs-scripts\.com', re.IGNORECASE),
        (r'hbspt\.forms\.create', re.IGNORECASE),
    ]),
    ("Sentry", [
        (r'browser\.sentry-cdn\.com', re.IGNORECASE),
        (r'Sentry\.init', re.IGNORECASE),
    ]),
    ("Segment", [
        (r'cdn\.segment\.com/analytics\.js', re.IGNORECASE),
    ]),
    ("reCAPTCHA", [
        (r'google\.com/recaptcha', re.IGNORECASE),
        (r'grecaptcha', re.IGNORECASE),
    ]),
    ("hCaptcha", [
        (r'hcaptcha\.com/1/api\.js', re.IGNORECASE),
    ]),
]


def init_intel_tables(db):
    """Ensure database schema has page_contents and FTS tables."""
    db.executescript("""
        CREATE TABLE IF NOT EXISTS page_contents (
            url TEXT PRIMARY KEY,
            host TEXT NOT NULL,
            status_code INTEGER DEFAULT 200,
            title TEXT DEFAULT '',
            html TEXT NOT NULL,
            headers_json TEXT DEFAULT '{}',
            signatures_json TEXT DEFAULT '[]',
            content_length INTEGER DEFAULT 0,
            scanned_at TEXT NOT NULL
        );
        CREATE INDEX IF NOT EXISTS page_contents_host ON page_contents(host);
        CREATE INDEX IF NOT EXISTS page_contents_scanned ON page_contents(scanned_at);
    """)
    # Check if FTS5 is available and create trigram virtual table
    try:
        db.execute("""
            CREATE VIRTUAL TABLE IF NOT EXISTS html_intel_fts USING fts5(
                url UNINDEXED,
                host UNINDEXED,
                title,
                html,
                tokenize='trigram'
            )
        """)
    except sqlite3.OperationalError:
        # FTS5 trigram not supported in this sqlite build, fallback to standard queries
        pass


def extract_title(html):
    """Extract <title> from HTML source code."""
    match = re.search(r'<title[^>]*>(.*?)</title>', html, re.IGNORECASE | re.DOTALL)
    if not match:
        return ""
    raw = match.group(1).strip()
    # Unescape HTML entities
    unescaped = html_module.unescape(raw)
    # Collapse multiple whitespace
    return re.sub(r'\s+', ' ', unescaped).strip()


def detect_signatures(html, headers=None):
    """Detect technologies and frameworks from HTML source and headers."""
    detected = set()
    for name, patterns in SIGNATURE_RULES:
        for pat, flags in patterns:
            if re.search(pat, html, flags):
                detected.add(name)
                break
    if headers and isinstance(headers, dict):
        server = headers.get("server", "")
        if "cloudflare" in server.lower():
            detected.add("Cloudflare")
    return sorted(detected)


def extract_snippets(html, query, max_snippets=4, context_lines=1, max_line_len=300):
    """Find line occurrences of query in HTML with context (PublicWWW style)."""
    if not query:
        return {"match_count": 0, "snippets": []}
    lines = html.splitlines()
    matches = []
    q_lower = query.lower()
    total_count = 0
    for idx, line in enumerate(lines):
        line_lower = line.lower()
        pos = line_lower.find(q_lower)
        if pos != -1:
            total_count += 1
            if len(matches) < max_snippets:
                start_ctx = max(0, idx - context_lines)
                end_ctx = min(len(lines), idx + context_lines + 1)
                ctx_lines = []
                for c_idx in range(start_ctx, end_ctx):
                    raw_c_line = lines[c_idx]
                    # truncate very long lines to preserve readability
                    if len(raw_c_line) > max_line_len:
                        c_line = raw_c_line[:max_line_len] + "…"
                    else:
                        c_line = raw_c_line
                    ctx_lines.append({
                        "line_number": c_idx + 1,
                        "content": c_line,
                        "is_match": (c_idx == idx),
                    })
                matches.append({
                    "line_number": idx + 1,
                    "matched_line": (line[:max_line_len] + "…") if len(line) > max_line_len else line,
                    "context": ctx_lines,
                })
    return {"match_count": total_count, "snippets": matches}


def index_page(db, url_val, html_content, status_code=200, headers=None, scanned_at=None):
    """Index a webpage's HTML into the intelligence database."""
    host, url = normalize(url_val)
    if scanned_at is None:
        scanned_at = datetime.now(timezone.utc).isoformat()
    title = extract_title(html_content)
    signatures = detect_signatures(html_content, headers)
    headers_str = json.dumps(headers or {})
    sigs_str = json.dumps(signatures)
    content_len = len(html_content.encode("utf-8", errors="replace"))

    with db:
        # Also ensure site is registered in sites table
        db.execute("INSERT OR IGNORE INTO sites VALUES (?, ?)", (url, host))
        db.execute("""
            INSERT INTO page_contents(url, host, status_code, title, html, headers_json, signatures_json, content_length, scanned_at)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
            ON CONFLICT(url) DO UPDATE SET
                host=excluded.host,
                status_code=excluded.status_code,
                title=excluded.title,
                html=excluded.html,
                headers_json=excluded.headers_json,
                signatures_json=excluded.signatures_json,
                content_length=excluded.content_length,
                scanned_at=excluded.scanned_at
        """, (url, host, status_code, title, html_content, headers_str, sigs_str, content_len, scanned_at))

        # Update FTS5 if table exists
        try:
            db.execute("DELETE FROM html_intel_fts WHERE url=?", (url,))
            db.execute("INSERT INTO html_intel_fts(url, host, title, html) VALUES (?, ?, ?, ?)",
                       (url, host, title, html_content))
        except sqlite3.OperationalError:
            pass

    return {
        "url": url,
        "host": host,
        "status_code": status_code,
        "title": title,
        "signatures": signatures,
        "content_length": content_len,
        "scanned_at": scanned_at,
    }


def index_pages_batch(db, records):
    """Index a batch of pages in a single SQLite transaction for max throughput."""
    if not records:
        return []
    results = []
    with db:
        for rec in records:
            url_val = rec["url"]
            html_content = rec["html"]
            status_code = rec.get("status_code", 200)
            headers = rec.get("headers")
            scanned_at = rec.get("scanned_at") or datetime.now(timezone.utc).isoformat()

            try:
                host, url = normalize(url_val)
            except Exception:
                continue

            title = extract_title(html_content)
            signatures = detect_signatures(html_content, headers)
            headers_str = json.dumps(headers or {})
            sigs_str = json.dumps(signatures)
            content_len = len(html_content.encode("utf-8", errors="replace"))

            db.execute("INSERT OR IGNORE INTO sites VALUES (?, ?)", (url, host))
            db.execute("""
                INSERT INTO page_contents(url, host, status_code, title, html, headers_json, signatures_json, content_length, scanned_at)
                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
                ON CONFLICT(url) DO UPDATE SET
                    host=excluded.host,
                    status_code=excluded.status_code,
                    title=excluded.title,
                    html=excluded.html,
                    headers_json=excluded.headers_json,
                    signatures_json=excluded.signatures_json,
                    content_length=excluded.content_length,
                    scanned_at=excluded.scanned_at
            """, (url, host, status_code, title, html_content, headers_str, sigs_str, content_len, scanned_at))

            try:
                db.execute("DELETE FROM html_intel_fts WHERE url=?", (url,))
                db.execute("INSERT INTO html_intel_fts(url, host, title, html) VALUES (?, ?, ?, ?)",
                           (url, host, title, html_content))
            except sqlite3.OperationalError:
                pass

            results.append({
                "url": url,
                "host": host,
                "status_code": status_code,
                "title": title,
                "signatures": signatures,
                "content_length": content_len,
                "scanned_at": scanned_at,
            })
    return results


def search_intel(db, query="", signature="", page=1, limit=25):
    """Search for HTML snippets, keywords, and technology signatures across indexed web pages."""
    query = (query or "").strip()
    signature = (signature or "").strip()

    if not query and not signature:
        return {"query": "", "signature": "", "total": 0, "page": page, "limit": limit, "items": []}

    offset = (page - 1) * limit
    db.row_factory = sqlite3.Row
    items = []
    total = 0

    if not query and signature:
        # Search by signature alone
        sig_pattern = f'%"{signature}"%'
        count_row = db.execute(
            "SELECT count(*) FROM page_contents WHERE signatures_json LIKE ?",
            (sig_pattern,)
        ).fetchone()
        total = count_row[0] if count_row else 0
        rows = db.execute("""
            SELECT url, host, title, status_code, content_length, scanned_at,
                   signatures_json, html
            FROM page_contents
            WHERE signatures_json LIKE ?
            ORDER BY scanned_at DESC
            LIMIT ? OFFSET ?
        """, (sig_pattern, limit, offset)).fetchall()

        for r in rows:
            sigs = json.loads(r["signatures_json"]) if r["signatures_json"] else []
            # Preview first 3 lines of HTML as snippet
            lines = [line.strip() for line in r["html"].splitlines()[:10] if line.strip()][:3]
            snips = [{
                "line_number": idx + 1,
                "matched_line": line[:150],
                "context": [{"line_number": idx + 1, "content": line[:150], "is_match": True}]
            } for idx, line in enumerate(lines)]
            items.append({
                "url": r["url"],
                "host": r["host"],
                "title": r["title"],
                "status_code": r["status_code"],
                "content_length": r["content_length"],
                "scanned_at": r["scanned_at"],
                "signatures": sigs,
                "match_count": 1,
                "snippets": snips,
            })

    elif query and signature:
        # Search by both query text and signature
        sig_pattern = f'%"{signature}"%'
        count_row = db.execute(
            "SELECT count(*) FROM page_contents WHERE instr(lower(html), lower(?)) > 0 AND signatures_json LIKE ?",
            (query, sig_pattern)
        ).fetchone()
        total = count_row[0] if count_row else 0
        rows = db.execute("""
            SELECT url, host, title, status_code, content_length, scanned_at,
                   signatures_json, html
            FROM page_contents
            WHERE instr(lower(html), lower(?)) > 0 AND signatures_json LIKE ?
            ORDER BY scanned_at DESC
            LIMIT ? OFFSET ?
        """, (query, sig_pattern, limit, offset)).fetchall()

        for r in rows:
            snips = extract_snippets(r["html"], query)
            sigs = json.loads(r["signatures_json"]) if r["signatures_json"] else []
            items.append({
                "url": r["url"],
                "host": r["host"],
                "title": r["title"],
                "status_code": r["status_code"],
                "content_length": r["content_length"],
                "scanned_at": r["scanned_at"],
                "signatures": sigs,
                "match_count": snips["match_count"],
                "snippets": snips["snippets"],
            })

    else:
        # Search by query text (using FTS5 trigram or fallback)
        used_fts = False
        try:
            escaped_query = '"' + query.replace('"', '""') + '"'
            count_row = db.execute(
                "SELECT count(*) FROM html_intel_fts WHERE html_intel_fts MATCH ?",
                (escaped_query,)
            ).fetchone()
            if count_row:
                total = count_row[0]
                rows = db.execute("""
                    SELECT p.url, p.host, p.title, p.status_code, p.content_length, p.scanned_at,
                           p.signatures_json, p.html
                    FROM html_intel_fts f
                    JOIN page_contents p ON p.url = f.url
                    WHERE html_intel_fts MATCH ?
                    ORDER BY f.rowid DESC
                    LIMIT ? OFFSET ?
                """, (escaped_query, limit, offset)).fetchall()
                used_fts = True
        except sqlite3.OperationalError:
            used_fts = False

        if not used_fts:
            count_row = db.execute(
                "SELECT count(*) FROM page_contents WHERE instr(lower(html), lower(?)) > 0",
                (query,)
            ).fetchone()
            total = count_row[0] if count_row else 0
            rows = db.execute("""
                SELECT url, host, title, status_code, content_length, scanned_at,
                       signatures_json, html
                FROM page_contents
                WHERE instr(lower(html), lower(?)) > 0
                ORDER BY scanned_at DESC
                LIMIT ? OFFSET ?
            """, (query, limit, offset)).fetchall()

        for r in rows:
            snips = extract_snippets(r["html"], query)
            sigs = json.loads(r["signatures_json"]) if r["signatures_json"] else []
            items.append({
                "url": r["url"],
                "host": r["host"],
                "title": r["title"],
                "status_code": r["status_code"],
                "content_length": r["content_length"],
                "scanned_at": r["scanned_at"],
                "signatures": sigs,
                "match_count": snips["match_count"],
                "snippets": snips["snippets"],
            })

    return {
        "query": query,
        "signature": signature,
        "total": total,
        "page": page,
        "limit": limit,
        "has_more": (offset + len(items)) < total,
        "items": items,
    }


def get_signatures_summary(db):
    """Return count of occurrences of each technology signature across page_contents."""
    db.row_factory = sqlite3.Row
    try:
        rows = db.execute("SELECT signatures_json FROM page_contents WHERE signatures_json != '[]'").fetchall()
    except sqlite3.OperationalError:
        return []

    counts = {}
    for r in rows:
        try:
            sigs = json.loads(r["signatures_json"])
            for s in sigs:
                counts[s] = counts.get(s, 0) + 1
        except Exception:
            pass

    all_rule_names = [rule[0] for rule in SIGNATURE_RULES]
    for name in all_rule_names:
        if name not in counts:
            counts[name] = 0

    return sorted([{"name": k, "count": v} for k, v in counts.items()], key=lambda x: (-x["count"], x["name"]))


def fetch_and_index_url(db, target_url, timeout=10, max_size=2 * 1024 * 1024):
    """Fetch live web page HTML and index into intelligence database."""
    _, norm_url = normalize(target_url)
    req = urllib.request.Request(
        norm_url,
        headers={
            "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36 (compatible; InternetMapIntel/1.0)",
            "Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
            "Accept-Language": "en-US,en;q=0.5",
        }
    )
    status_code = 200
    headers = {}
    try:
        with urllib.request.urlopen(req, timeout=timeout) as response:
            status_code = response.status
            for k, v in response.headers.items():
                headers[k.lower()] = v
            content_bytes = response.read(max_size)
    except urllib.error.HTTPError as e:
        status_code = e.code
        for k, v in e.headers.items():
            headers[k.lower()] = v
        content_bytes = e.read(max_size)
    except Exception as e:
        raise RuntimeError(f"Failed to fetch {norm_url}: {e}")

    # Determine encoding
    encoding = "utf-8"
    content_type = headers.get("content-type", "")
    if "charset=" in content_type.lower():
        encoding = content_type.lower().split("charset=")[-1].split(";")[0].strip()

    try:
        html_str = content_bytes.decode(encoding, errors="replace")
    except Exception:
        html_str = content_bytes.decode("utf-8", errors="replace")

    return index_page(db, norm_url, html_str, status_code=status_code, headers=headers)
