#!/usr/bin/env python3
"""Serve the Internet Map catalog API and its independent browser UI."""
import argparse
import hmac
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import sqlite3
from urllib.parse import parse_qs, urlsplit

from main import normalize


def handler(database, key):
    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass

        def reply(self, status, data):
            payload = json.dumps(data).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Cache-Control", "no-store")
            self.send_header("X-Content-Type-Options", "nosniff")
            self.end_headers()
            self.wfile.write(payload)

        def do_GET(self):
            parsed = urlsplit(self.path)
            if parsed.path == "/":
                payload = (Path(__file__).parent / "ui/index.html").read_bytes()
                self.send_response(200)
                self.send_header("Content-Type", "text/html; charset=utf-8")
                self.send_header("Cache-Control", "no-store")
                self.send_header("Content-Security-Policy", "default-src 'self'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'")
                self.end_headers()
                self.wfile.write(payload)
                return
            supplied = self.headers.get("Authorization", "")
            if not hmac.compare_digest(supplied.encode(), ("Bearer " + key).encode()):
                self.reply(401, {"error": "Enter a valid API key."})
                return
            query = parse_qs(parsed.query)
            try:
                with sqlite3.connect(database.resolve().as_uri() + "?mode=ro", uri=True, timeout=5) as db:
                    db.row_factory = sqlite3.Row
                    if parsed.path == "/api/v1/stats":
                        result = {
                            "urls": db.execute("SELECT count(*) FROM sites").fetchone()[0],
                            "imports": db.execute("SELECT count(*) FROM imports").fetchone()[0],
                            "wordpress": db.execute("SELECT count(DISTINCT url) FROM observations WHERE evidence!='[]'").fetchone()[0],
                        }
                    elif parsed.path == "/api/v1/sites":
                        page = int(query.get("page", ["1"])[0])
                        if not 1 <= page <= 10000000:
                            raise ValueError("Invalid page")
                        host = query.get("host", [""])[0].lower()
                        condition = "instr(s.host,?)>0"
                        if query.get("wordpress", ["0"])[0] == "1":
                            condition += " AND EXISTS(SELECT 1 FROM observations o WHERE o.url=s.url AND o.evidence!='[]')"
                        rows = db.execute("SELECT s.url,s.host FROM sites s WHERE " + condition + " ORDER BY s.url LIMIT 26 OFFSET ?", (host, (page-1)*25)).fetchall()
                        result = {"items": [dict(r) for r in rows[:25]], "has_more": len(rows)>25, "page": page}
                    elif parsed.path == "/api/v1/observations":
                        _, url = normalize(query.get("url", [""])[0])
                        rows = db.execute("SELECT i.source,i.imported_at,o.evidence FROM observations o JOIN imports i ON i.id=o.import_id WHERE o.url=? ORDER BY i.id DESC LIMIT 100", (url,))
                        result = {"items": [{"source": r["source"], "imported_at": r["imported_at"], "evidence": json.loads(r["evidence"])} for r in rows]}
                    else:
                        self.reply(404, {"error": "Endpoint not found"})
                        return
                self.reply(200, result)
            except (ValueError, UnicodeError):
                self.reply(400, {"error": "Invalid query"})
            except sqlite3.Error:
                self.reply(503, {"error": "Catalog unavailable. Import a dataset first."})
    return Handler


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--database", type=Path, default=Path("internet-map/data/catalog.db"))
    parser.add_argument("--port", type=int, default=3500)
    args = parser.parse_args()
    key = os.environ.get("INTERNET_MAP_API_KEY", "")
    if len(key) < 32:
        parser.error("Set INTERNET_MAP_API_KEY to a secret of at least 32 characters")
    server = ThreadingHTTPServer(("127.0.0.1", args.port), handler(args.database, key))
    print(f"Internet Map available at http://127.0.0.1:{args.port}", flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()


if __name__ == "__main__":
    main()
