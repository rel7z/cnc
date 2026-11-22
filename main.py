#!/usr/bin/env python3
"""Internet Map dataset catalog."""
import argparse
import csv
from html.parser import HTMLParser
import json
import sys
from pathlib import Path
import sqlite3
from urllib.parse import urlsplit
from datetime import datetime, timezone


DEFAULT_DATABASE = Path(__file__).resolve().parent / "data/catalog.db"

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
        elif kind in ("jsonl", "observations"):
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


class WordPressHTML(HTMLParser):
    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.evidence = set()

    def handle_starttag(self, tag, attrs):
        attributes = dict(attrs)
        if tag == "meta" and (attributes.get("name") or "").lower() == "generator":
            generator = (attributes.get("content") or "").strip().lower()
            if generator == "wordpress" or generator.startswith("wordpress "):
                self.evidence.add("WordPress generator tag")
        for attribute in ("src", "href"):
            value = attributes.get(attribute) or ""
            try:
                path = urlsplit(value).path.lower()
            except ValueError:
                continue
            for marker in ("wp-content", "wp-includes"):
                if "/" + marker + "/" in "/" + path.lstrip("/"):
                    self.evidence.add(marker + " resource reference")

    handle_startendtag = handle_starttag


def wordpress_evidence(html):
    parser = WordPressHTML()
    parser.feed(html)
    parser.close()
    return sorted(parser.evidence)


def connect(path):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    db = sqlite3.connect(path)
    db.execute("PRAGMA foreign_keys=ON")
    db.execute("PRAGMA journal_mode=WAL")
    db.execute("PRAGMA synchronous=NORMAL")
    db.execute("PRAGMA cache_size=-64000")
    db.execute("PRAGMA temp_store=MEMORY")
    db.executescript("""
        CREATE TABLE IF NOT EXISTS imports (
            id INTEGER PRIMARY KEY, source TEXT NOT NULL,
            imported_at TEXT NOT NULL, accepted INTEGER NOT NULL DEFAULT 0,
            rejected INTEGER NOT NULL DEFAULT 0
        );
        CREATE TABLE IF NOT EXISTS sites (
            url TEXT PRIMARY KEY, host TEXT NOT NULL
        );
        CREATE INDEX IF NOT EXISTS sites_host ON sites(host);
        CREATE TABLE IF NOT EXISTS observations (
            import_id INTEGER NOT NULL REFERENCES imports(id),
            url TEXT NOT NULL REFERENCES sites(url),
            evidence TEXT NOT NULL,
            PRIMARY KEY(import_id, url)
        );
        CREATE INDEX IF NOT EXISTS observations_url ON observations(url);
        CREATE INDEX IF NOT EXISTS observations_wp_url ON observations(url) WHERE evidence != '[]';
    """)
    return db


BATCH_SIZE = 25000


def ingest(db, args, stream_records=None):
    now = datetime.now(timezone.utc).isoformat()
    accepted = rejected = 0
    t0 = datetime.now().timestamp()
    last_log = t0
    source_records = stream_records if stream_records is not None else records(args.input, getattr(args, "format", "txt"))

    with db:
        ident = db.execute("INSERT INTO imports(source,imported_at) VALUES (?,?)",
                           (args.source, now)).lastrowid

        sites_batch = []
        obs_batch = []
        import_urls = {}

        def flush():
            if sites_batch:
                db.executemany("INSERT OR IGNORE INTO sites VALUES (?,?)", sites_batch)
                sites_batch.clear()
            if obs_batch:
                db.executemany("INSERT OR IGNORE INTO observations VALUES (?,?,?)", obs_batch)
                obs_batch.clear()

        for record in source_records:
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

            if getattr(args, "format", "") == "observations":
                supplied = record.get("wordpress_evidence")
                allowed = {"WordPress generator tag", "wp-content resource reference", "wp-includes resource reference"}
                if not isinstance(supplied, list) or any(not isinstance(item, str) or item not in allowed for item in supplied):
                    raise ValueError("Invalid worker evidence")
                evidence = sorted(set(supplied))

            if url in import_urls:
                if evidence:
                    prev = import_urls[url]
                    merged = sorted(set(prev) | set(evidence))
                    if merged != prev:
                        import_urls[url] = merged
                        updated_in_batch = False
                        for idx in range(len(obs_batch) - 1, -1, -1):
                            if obs_batch[idx][1] == url:
                                obs_batch[idx] = (ident, url, json.dumps(merged))
                                updated_in_batch = True
                                break
                        if not updated_in_batch:
                            db.execute("UPDATE observations SET evidence=? WHERE import_id=? AND url=?",
                                       (json.dumps(merged), ident, url))
            else:
                import_urls[url] = evidence
                sites_batch.append((url, host))
                obs_batch.append((ident, url, json.dumps(evidence)))
                accepted += 1

            if len(sites_batch) >= BATCH_SIZE:
                flush()
                now_t = datetime.now().timestamp()
                if now_t - last_log >= 3.0:
                    speed = accepted / (now_t - t0) if now_t > t0 else 0
                    print(f"[*] Ingested {accepted:,} records ({int(speed):,} rec/s)...", flush=True)
                    last_log = now_t

        flush()
        db.execute("UPDATE imports SET accepted=?,rejected=? WHERE id=?",
                   (accepted, rejected, ident))

    print(f"Imported {accepted} unique URLs from {args.source}. Rejected {rejected} rows.")


def seed_catalog(db, count=250):
    """Seed the catalog and intel database with rich realistic datasets for fast development."""
    from intel import init_intel_tables, index_pages_batch
    init_intel_tables(db)

    CATEGORIES = ["foundation", "community", "charity", "news", "health", "tech", "blog", "center", "project", "open"]
    TOPICS = ["ocean", "wildlife", "climate", "code", "kids", "peace", "science", "arts", "future", "solar", "food", "data", "music", "books"]

    FAMOUS_ORGS = [
        ("wikipedia.org", "Wikipedia, the free encyclopedia", "<html><head><title>Wikipedia, the free encyclopedia</title></head><body><h1>Welcome to Wikipedia</h1><script>gtag('config', 'G-WIKI123');</script></body></html>"),
        ("wordpress.org", "Blog Tool, Publishing Platform, and CMS - WordPress.org", "<html><head><title>WordPress.org</title><meta name=\"generator\" content=\"WordPress 6.4.3\" /><link rel=\"stylesheet\" href=\"/wp-content/themes/pub/wporg/style.css\" /></head><body>WordPress Foundation</body></html>"),
        ("eff.org", "Electronic Frontier Foundation | Defending Freedom in the Digital World", "<html><head><title>Electronic Frontier Foundation</title><script src=\"https://challenges.cloudflare.com/turnstile/v0/api.js\"></script></head><body>EFF Action Center</body></html>"),
        ("archive.org", "Internet Archive: Digital Library of Free Books, Movies, Music & Wayback Machine", "<html><head><title>Internet Archive</title><script>gtag('config', 'UA-99999');</script></head><body>Wayback Machine</body></html>"),
        ("mozilla.org", "Internet for people, not profit — Mozilla", "<html><head><title>Mozilla</title><script src=\"https://www.googletagmanager.com/gtm.js?id=GTM-MOZ1\"></script></head><body class=\"tailwindcss\">Mozilla Foundation</body></html>"),
        ("apache.org", "Welcome to The Apache Software Foundation!", "<html><head><title>The Apache Software Foundation</title></head><body>Apache HTTP Server & Foundation</body></html>"),
        ("fsf.org", "Free Software Foundation — working together for free software", "<html><head><title>Free Software Foundation</title></head><body>GNU and Free Software</body></html>"),
        ("redcross.org", "American Red Cross | Help Those Affected by Disasters", "<html><head><title>American Red Cross</title><script src=\"https://js.stripe.com/v3/\"></script></head><body>Donate to Red Cross</body></html>"),
        ("khanacademy.org", "Khan Academy | Free Online Courses, Lessons & Practice", "<html><head><title>Khan Academy</title><script>window.__NEXT_DATA__ = {};</script></head><body>Learn anything</body></html>"),
        ("ted.org", "TED: Ideas Worth Spreading", "<html><head><title>TED Talks</title><script src=\"https://connect.facebook.net/en_US/fbevents.js\"></script></head><body>TED Conferences</body></html>"),
    ]

    now_iso = datetime.now(timezone.utc).isoformat()
    raw_records = []
    intel_records = []

    # Add famous organizations
    for host, title, html in FAMOUS_ORGS:
        url = f"https://{host}/"
        raw_records.append({"url": url, "html": html})
        intel_records.append({"url": url, "html": html, "status_code": 200, "headers": {"server": "nginx"}})

    # Generate procedural realistic domains up to count
    i = 0
    while len(raw_records) < count:
        cat = CATEGORIES[i % len(CATEGORIES)]
        top = TOPICS[(i * 3 + 1) % len(TOPICS)]
        num = i + 101
        host = f"{top}-{cat}{num}.org"
        url = f"https://{host}/"

        mod = i % 5
        if mod == 0:
            # WordPress site with theme and plugin
            theme = ["astra", "twentytwentyfour", "generatepress", "oceanwp"][i % 4]
            wp_ver = ["6.4.3", "6.3.2", "6.2.1"][i % 3]
            html = f"""<!DOCTYPE html>
<html>
<head>
  <title>{top.title()} {cat.title()} Initiative - Welcome</title>
  <meta name="generator" content="WordPress {wp_ver}">
  <link rel="stylesheet" href="/wp-content/themes/{theme}/style.css">
  <link rel="stylesheet" href="/wp-content/plugins/woocommerce/assets/css/woocommerce.css">
  <script src="/wp-includes/js/jquery/jquery.min.js"></script>
  <script>gtag('config', 'G-{i}WP99');</script>
</head>
<body class="home blog">
  <h1>Welcome to {top.title()} {cat.title()}</h1>
  <p>Dedicated to global progress and collaboration.</p>
</body>
</html>"""
            raw_records.append({"url": url, "html": html})
            intel_records.append({"url": url, "html": html, "status_code": 200, "headers": {"server": "Apache"}})
        elif mod == 1:
            # E-commerce / Stripe site
            html = f"""<!DOCTYPE html>
<html>
<head>
  <title>{top.title()} Store & Goods</title>
  <script src="https://js.stripe.com/v3/"></script>
  <script src="https://cdn.shopify.com/s/files/store.js"></script>
  <script>(adsbygoogle = window.adsbygoogle || []).push({{}});</script>
</head>
<body>
  <h1>Support our {cat}</h1>
</body>
</html>"""
            raw_records.append({"url": url, "html": html})
            intel_records.append({"url": url, "html": html, "status_code": 200, "headers": {"server": "cloudflare"}})
        elif mod == 2:
            # Modern React / Tailwind site
            html = f"""<!DOCTYPE html>
<html>
<head>
  <title>{top.title()} {cat.title()} Portal</title>
  <script>window.__NEXT_DATA__ = {{}}; gtag('config', 'UA-{num}-1');</script>
  <link rel="stylesheet" href="/_next/static/css/styles.css">
</head>
<body class="tailwindcss bg-slate-900 text-white">
  <h1>Modern {cat} Platform</h1>
</body>
</html>"""
            raw_records.append({"url": url, "html": html})
            intel_records.append({"url": url, "html": html, "status_code": 200, "headers": {"server": "Vercel"}})
        elif mod == 3:
            # Simple static community site
            html = f"""<!DOCTYPE html>
<html>
<head>
  <title>{top.title()} Community Network</title>
  <script src="https://challenges.cloudflare.com/turnstile/v0/api.js"></script>
</head>
<body>
  <h1>Join the community</h1>
</body>
</html>"""
            raw_records.append({"url": url, "html": html})
            intel_records.append({"url": url, "html": html, "status_code": 200, "headers": {"server": "cloudflare"}})
        else:
            # Raw DNS delegation with no HTML evidence recorded
            raw_records.append({"url": url, "html": ""})
        i += 1

    # Ingest into catalog
    class DummyArgs:
        source = "dev-seed-dataset"
        format = "jsonl"
    ingest(db, DummyArgs(), stream_records=raw_records)

    # Index active HTML into intel database
    index_pages_batch(db, intel_records)
    print(f"[+] Successfully seeded {len(raw_records)} domains and {len(intel_records)} HTML intelligence pages!")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--database", type=Path, default=DEFAULT_DATABASE)
    db_parent = argparse.ArgumentParser(add_help=False)
    db_parent.add_argument("--database", type=Path, default=argparse.SUPPRESS)
    commands = parser.add_subparsers(dest="command", required=True)
    load = commands.add_parser("import", parents=[db_parent])
    load.add_argument("input", type=Path)
    load.add_argument("--format", choices=("txt", "csv", "jsonl", "observations"), required=True)
    load.add_argument("--source", required=True)
    search = commands.add_parser("search", parents=[db_parent])
    search.add_argument("--host", default="")
    search.add_argument("--wordpress", action="store_true")
    search.add_argument("--limit", type=int, default=100)
    intel_search = commands.add_parser("intel-search", parents=[db_parent])
    intel_search.add_argument("query")
    intel_search.add_argument("--limit", type=int, default=25)
    intel_scan = commands.add_parser("intel-scan", parents=[db_parent])
    intel_scan.add_argument("url")
    export = commands.add_parser("export", parents=[db_parent])
    export.add_argument("--wordpress", action="store_true", help="Export only verified WordPress sites")
    export.add_argument("--host", default="", help="Filter by host substring")
    export.add_argument("--signature", default="", help="Filter by technology signature (e.g. Shopify, WooCommerce)")
    export.add_argument("--field", choices=["url", "host"], default="url", help="Output full URL or hostname only")
    export.add_argument("--output", "-o", type=Path, default=None, help="Save to .txt file instead of stdout")
    inspect = commands.add_parser("observations", parents=[db_parent])
    inspect.add_argument("url")
    seed_cmd = commands.add_parser("seed", parents=[db_parent])
    seed_cmd.add_argument("--count", type=int, default=250, help="Number of demo domains to generate")
    commands.add_parser("stats", parents=[db_parent])
    args = parser.parse_args()
    db = connect(args.database)
    try:
        if args.command == "import":
            ingest(db, args)
        elif args.command == "seed":
            seed_catalog(db, count=args.count)
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
            field_col = "s.url" if args.field == "url" else "s.host"
            conditions = []
            params = []
            if args.signature:
                sql = f"SELECT {field_col} FROM page_contents p JOIN sites s ON s.url = p.url WHERE p.signatures_json LIKE ?"
                params.append(f'%"{args.signature}"%')
                if args.host:
                    sql += " AND instr(s.host, ?) > 0"
                    params.append(args.host.lower())
                if args.wordpress:
                    sql += " AND EXISTS(SELECT 1 FROM observations o WHERE o.url=s.url AND o.evidence!='[]')"
                sql += f" ORDER BY {field_col}"
            else:
                sql = f"SELECT {field_col} FROM sites s"
                if args.host:
                    conditions.append("instr(s.host, ?) > 0")
                    params.append(args.host.lower())
                if args.wordpress:
                    conditions.append("EXISTS(SELECT 1 FROM observations o WHERE o.url=s.url AND o.evidence!='[]')")
                if conditions:
                    sql += " WHERE " + " AND ".join(conditions)
                sql += f" ORDER BY {field_col}"

            out_stream = open(args.output, "w", encoding="utf-8") if args.output else sys.stdout
            try:
                for row in db.execute(sql, params):
                    out_stream.write((row[0] or "") + "\n")
            finally:
                if args.output:
                    out_stream.close()
                    print(f"[+] Exported to {args.output}")
        elif args.command == "intel-search":
            from intel import search_intel
            res = search_intel(db, args.query, limit=args.limit)
            print(f"Found {res['total']} results for '{args.query}':")
            for item in res["items"]:
                print(f"- {item['url']} ({item['title']}) [{', '.join(item['signatures'])}]")
                for snip in item["snippets"]:
                    print(f"  Line {snip['line_number']}: {snip['matched_line'].strip()}")
        elif args.command == "intel-scan":
            from intel import init_intel_tables, fetch_and_index_url
            init_intel_tables(db)
            meta = fetch_and_index_url(db, args.url)
            print(f"Indexed {meta['url']} - Title: {meta['title']} - Tech: {', '.join(meta['signatures'])}")
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
