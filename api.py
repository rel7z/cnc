#!/usr/bin/env python3
"""Serve the Internet Map catalog API and its independent browser UI."""
import argparse
from datetime import datetime, timezone
import hmac
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import signal
import sqlite3
import subprocess
import sys
import time
from urllib.parse import parse_qs, urlsplit

from main import DEFAULT_DATABASE, normalize
from intel import init_intel_tables, search_intel, fetch_and_index_url, get_signatures_summary

_stats_cache = {}


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


def get_scanner_state(state_path: Path) -> dict:
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


def get_scanner_config(config_path: Path) -> dict:
    if config_path.exists():
        try:
            return json.loads(config_path.read_text(encoding="utf-8"))
        except Exception:
            pass
    return {
        "concurrency": 600,
        "profile": "high",
        "cpu_quota": "180%",
        "memory_max_mb": 1200,
        "memory_high_mb": 900,
        "cgroups_managed": True,
    }


def save_scanner_config(config_path: Path, config: dict):
    config_path.parent.mkdir(parents=True, exist_ok=True)
    tmp = config_path.with_suffix(".tmp")
    tmp.write_text(json.dumps(config, indent=2), encoding="utf-8")
    tmp.replace(config_path)


def get_process_memory_mb(pid: int) -> float:
    if not pid or pid <= 0:
        return 0.0
    statm = Path(f"/proc/{pid}/statm")
    if statm.exists():
        try:
            return round(int(statm.read_text().split()[1]) * 4096 / (1024 * 1024), 1)
        except Exception:
            pass
    return 0.0


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

        def check_auth(self):
            supplied = self.headers.get("Authorization", "")
            return hmac.compare_digest(supplied.encode(), ("Bearer " + key).encode())

        def do_GET(self):
            parsed = urlsplit(self.path)
            if parsed.path == "/":
                payload = (Path(__file__).parent / "ui/index.html").read_bytes()
                self.send_response(200)
                self.send_header("Content-Type", "text/html; charset=utf-8")
                self.send_header("Cache-Control", "no-store")
                self.send_header(
                    "Content-Security-Policy",
                    "default-src 'self'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'",
                )
                self.end_headers()
                self.wfile.write(payload)
                return

            if not self.check_auth():
                self.reply(401, {"error": "Enter a valid API key."})
                return

            query = parse_qs(parsed.query)
            state_path = database.parent / "scanner-state.json"

            if parsed.path in ("/api/v1/scanner/status", "/api/v1/scanner"):
                self.reply(200, get_scanner_state(state_path))
                return

            if parsed.path in ("/api/v1/scanner/resources", "/api/v1/scanner/resource"):
                config_path = database.parent / "scanner-config.json"
                st = get_scanner_state(state_path)
                pid = st.get("pid", 0)
                mem_mb = get_process_memory_mb(pid) if st.get("running") else 0.0
                cfg = get_scanner_config(config_path)
                result = {
                    "cgroups": {
                        "cpu_quota": cfg.get("cpu_quota", "180%"),
                        "cpu_quota_cores": 1.8,
                        "memory_max_mb": cfg.get("memory_max_mb", 1200),
                        "memory_high_mb": cfg.get("memory_high_mb", 900),
                        "tasks_max": 1500,
                        "cpu_weight": 50,
                        "nice": 5,
                        "active": True,
                    },
                    "telemetry": {
                        "running": st.get("running", False),
                        "pid": pid,
                        "memory_mb": mem_mb,
                        "speed": st.get("speed", 0.0),
                    },
                    "config": cfg,
                }
                self.reply(200, result)
                return

            if parsed.path == "/api/v1/intel/status":
                self.reply(200, get_scanner_state(state_path))
                return

            try:
                with sqlite3.connect(database.resolve().as_uri() + "?mode=ro", uri=True, timeout=5) as db:
                    db.row_factory = sqlite3.Row

                    if parsed.path == "/api/v1/stats":
                        now = time.monotonic()
                        cached = _stats_cache.get(str(database))
                        if cached and (now - cached["timestamp"] < 2.0):
                            result = cached["data"]
                        else:
                            urls_count = db.execute("SELECT count(*) FROM sites").fetchone()[0]
                            imports_count = db.execute("SELECT count(*) FROM imports").fetchone()[0]
                            wp_count = db.execute(
                                "SELECT count(DISTINCT url) FROM observations WHERE evidence!='[]'"
                            ).fetchone()[0]
                            try:
                                scanned_count = db.execute("SELECT count(*) FROM scan_progress").fetchone()[0]
                            except sqlite3.OperationalError:
                                scanned_count = 0

                            result = {
                                "urls": urls_count,
                                "imports": imports_count,
                                "wordpress": wp_count,
                            }
                            if query.get("extended", ["0"])[0] == "1" or query.get("scanner", ["0"])[0] == "1":
                                result["scanned"] = scanned_count
                                result["scanner"] = get_scanner_state(state_path)
                            _stats_cache[str(database)] = {"timestamp": now, "data": result}

                    elif parsed.path == "/api/v1/sites":
                        page = int(query.get("page", ["1"])[0])
                        if not 1 <= page <= 10000000:
                            raise ValueError("Invalid page")
                        limit = int(query.get("limit", ["25"])[0])
                        if not 1 <= limit <= 100:
                            limit = 25
                        host = query.get("host", [""])[0].lower()
                        is_wp = query.get("wordpress", ["0"])[0] == "1"
                        with_evidence = query.get("with_evidence", ["0"])[0] == "1"

                        fetch_count = limit + 1
                        offset = (page - 1) * limit

                        if is_wp:
                            # Fast indexed query on observations_wp_url
                            conditions = ["o.evidence != '[]'"]
                            params = []
                            if host:
                                conditions.append("instr(s.host, ?) > 0")
                                params.append(host)
                            where = " WHERE " + " AND ".join(conditions)
                            params.extend([fetch_count, offset])

                            rows = db.execute(
                                "SELECT s.url, s.host, o.evidence FROM observations o JOIN sites s ON s.url = o.url"
                                + where
                                + " GROUP BY s.url ORDER BY s.url LIMIT ? OFFSET ?",
                                params,
                            ).fetchall()

                            items = []
                            for r in rows[:limit]:
                                item = {"url": r["url"], "host": r["host"]}
                                if with_evidence:
                                    try:
                                        item["evidence"] = json.loads(r["evidence"])
                                    except Exception:
                                        item["evidence"] = [r["evidence"]]
                                items.append(item)
                            result = {"items": items, "has_more": len(rows) > limit, "page": page}

                        else:
                            conditions = []
                            params = []
                            if host:
                                conditions.append("instr(s.host, ?) > 0")
                                params.append(host)
                            where = (" WHERE " + " AND ".join(conditions)) if conditions else ""
                            params.extend([fetch_count, offset])

                            if with_evidence:
                                sql = (
                                    """
                                    SELECT s.url, s.host,
                                           (SELECT evidence FROM observations o WHERE o.url = s.url AND o.evidence != '[]' LIMIT 1) AS wp_evidence
                                    FROM sites s
                                    """
                                    + where
                                    + " ORDER BY s.url LIMIT ? OFFSET ?"
                                )
                                rows = db.execute(sql, params).fetchall()
                                items = []
                                for r in rows[:limit]:
                                    item = {"url": r["url"], "host": r["host"]}
                                    ev = r["wp_evidence"]
                                    if ev:
                                        try:
                                            item["evidence"] = json.loads(ev)
                                        except Exception:
                                            item["evidence"] = [ev]
                                    items.append(item)
                            else:
                                sql = "SELECT s.url, s.host FROM sites s" + where + " ORDER BY s.url LIMIT ? OFFSET ?"
                                rows = db.execute(sql, params).fetchall()
                                items = [{"url": r["url"], "host": r["host"]} for r in rows[:limit]]

                            result = {"items": items, "has_more": len(rows) > limit, "page": page}

                    elif parsed.path == "/api/v1/observations":
                        _, url = normalize(query.get("url", [""])[0])
                        rows = db.execute(
                            "SELECT i.source, i.imported_at, o.evidence FROM observations o JOIN imports i ON i.id = o.import_id WHERE o.url = ? ORDER BY i.id DESC LIMIT 100",
                            (url,),
                        ).fetchall()
                        result = {
                            "items": [
                                {
                                    "source": r["source"],
                                    "imported_at": r["imported_at"],
                                    "evidence": json.loads(r["evidence"]),
                                }
                                for r in rows
                            ]
                        }

                    elif parsed.path == "/api/v1/intel/search":
                        search_q = query.get("q", [""])[0]
                        sig_filter = query.get("signature", [""])[0]
                        page = int(query.get("page", ["1"])[0])
                        limit = int(query.get("limit", ["25"])[0])
                        if not 1 <= limit <= 100:
                            limit = 25
                        result = search_intel(db, search_q, signature=sig_filter, page=page, limit=limit)

                    elif parsed.path == "/api/v1/intel/signatures":
                        from intel import get_signatures_summary
                        result = {"signatures": get_signatures_summary(db)}

                    elif parsed.path in ("/api/v1/scanner/recent", "/api/v1/scanner-recent"):
                        try:
                            rows = db.execute("""
                                SELECT url, host, status_code, title, signatures_json, content_length, scanned_at
                                FROM page_contents
                                ORDER BY scanned_at DESC
                                LIMIT 20
                            """).fetchall()
                            recent_items = [{
                                "url": r["url"],
                                "host": r["host"],
                                "status_code": r["status_code"],
                                "title": r["title"],
                                "signatures": json.loads(r["signatures_json"]) if r["signatures_json"] else [],
                                "content_length": r["content_length"],
                                "scanned_at": r["scanned_at"],
                            } for r in rows]
                        except sqlite3.OperationalError:
                            recent_items = []
                        result = {"recent": recent_items}

                    elif parsed.path == "/api/v1/export":
                        is_wp = query.get("wordpress", ["0"])[0] == "1"
                        host_filter = query.get("host", [""])[0].lower()
                        sql = "SELECT s.url FROM sites s"
                        conditions = []
                        params = []
                        if host_filter:
                            conditions.append("instr(s.host, ?) > 0")
                            params.append(host_filter)
                        if is_wp:
                            conditions.append("EXISTS(SELECT 1 FROM observations o WHERE o.url = s.url AND o.evidence != '[]')")
                        if conditions:
                            sql += " WHERE " + " AND ".join(conditions)
                        sql += " ORDER BY s.url LIMIT 50000"
                        rows = db.execute(sql, params).fetchall()
                        result = {"total": len(rows), "urls": [r[0] for r in rows]}

                    elif parsed.path == "/api/v1/intel/source":
                        _, target_url = normalize(query.get("url", [""])[0])
                        row = db.execute(
                            "SELECT url, host, status_code, title, html, signatures_json, scanned_at FROM page_contents WHERE url = ?",
                            (target_url,),
                        ).fetchone()
                        if not row:
                            self.reply(404, {"error": "Page source not found in intelligence database"})
                            return
                        result = {
                            "url": row["url"],
                            "host": row["host"],
                            "status_code": row["status_code"],
                            "title": row["title"],
                            "signatures": json.loads(row["signatures_json"]) if row["signatures_json"] else [],
                            "scanned_at": row["scanned_at"],
                            "html": row["html"],
                            "line_count": len(row["html"].splitlines()),
                        }

                    elif parsed.path == "/api/v1/intel/stats":
                        total_pages = 0
                        total_bytes = 0
                        try:
                            p_row = db.execute(
                                "SELECT count(*), coalesce(sum(content_length), 0) FROM page_contents"
                            ).fetchone()
                            if p_row:
                                total_pages = p_row[0]
                                total_bytes = p_row[1]
                        except sqlite3.OperationalError:
                            pass
                        result = {"total_pages": total_pages, "total_bytes": total_bytes}

                    else:
                        self.reply(404, {"error": "Endpoint not found"})
                        return

                self.reply(200, result)
            except (ValueError, UnicodeError):
                self.reply(400, {"error": "Invalid query"})
            except sqlite3.Error:
                self.reply(503, {"error": "Catalog unavailable. Import a dataset first."})

        def do_POST(self):
            parsed = urlsplit(self.path)
            if not self.check_auth():
                self.reply(401, {"error": "Enter a valid API key."})
                return

            content_len = int(self.headers.get("Content-Length", 0))
            body = self.rfile.read(content_len) if content_len > 0 else b"{}"
            try:
                data = json.loads(body.decode("utf-8")) if body else {}
            except Exception:
                data = {}

            state_path = database.parent / "scanner-state.json"

            if parsed.path in ("/api/v1/scanner/resources", "/api/v1/scanner/resource"):
                config_path = database.parent / "scanner-config.json"
                cfg = get_scanner_config(config_path)

                req_concurrency = int(data.get("concurrency", cfg.get("concurrency", 600)))
                req_concurrency = max(25, min(1000, req_concurrency))
                req_profile = str(data.get("profile", ""))
                if not req_profile:
                    if req_concurrency <= 150:
                        req_profile = "eco"
                    elif req_concurrency <= 400:
                        req_profile = "balanced"
                    elif req_concurrency <= 750:
                        req_profile = "high"
                    else:
                        req_profile = "turbo"

                cfg["concurrency"] = req_concurrency
                cfg["profile"] = req_profile
                save_scanner_config(config_path, cfg)

                try:
                    subprocess.run(["systemctl", "restart", "internet-map-crawler"], timeout=5)
                except Exception:
                    pass

                _stats_cache.clear()
                self.reply(200, {"success": True, "config": cfg, "message": "Resource profile updated & applied"})
                return

            if parsed.path in ("/api/v1/scanner/start", "/api/v1/scanner", "/api/v1/scanner/status") and (
                parsed.path == "/api/v1/scanner/start" or data.get("action") == "start"
            ):
                curr = get_scanner_state(state_path)
                if curr.get("running"):
                    self.reply(200, {"success": True, "already_running": True, "state": curr})
                    return

                concurrency = int(data.get("concurrency", 0))
                if concurrency > 0:
                    config_path = database.parent / "scanner-config.json"
                    cfg = get_scanner_config(config_path)
                    cfg["concurrency"] = concurrency
                    save_scanner_config(config_path, cfg)

                started_via_systemd = False
                try:
                    res = subprocess.run(["systemctl", "start", "internet-map-crawler"], timeout=5, capture_output=True)
                    if res.returncode == 0:
                        started_via_systemd = True
                except Exception:
                    pass

                if not started_via_systemd:
                    limit = int(data.get("limit", 0))
                    scanner_script = Path(__file__).resolve().parent / "scanner.py"
                    cmd = [
                        sys.executable,
                        str(scanner_script),
                        "--database",
                        str(database),
                    ]
                    if concurrency > 0:
                        cmd.extend(["--concurrency", str(concurrency)])
                    if limit > 0:
                        cmd.extend(["--limit", str(limit)])

                    proc = subprocess.Popen(
                        cmd,
                        stdout=subprocess.DEVNULL,
                        stderr=subprocess.DEVNULL,
                        start_new_session=True,
                    )
                    curr["pid"] = proc.pid

                time.sleep(0.5)
                new_state = get_scanner_state(state_path)
                new_state["running"] = True
                update_scanner_state(state_path, new_state)
                _stats_cache.clear()
                self.reply(200, {"success": True, "message": "Scanner started", "state": new_state})
                return

            if parsed.path in ("/api/v1/scanner/stop", "/api/v1/scanner", "/api/v1/scanner/status") and (
                parsed.path == "/api/v1/scanner/stop" or data.get("action") == "stop"
            ):
                # 1. Stop systemd service cleanly so it does NOT restart automatically
                try:
                    subprocess.run(["systemctl", "stop", "internet-map-crawler"], timeout=5, capture_output=True)
                except Exception:
                    pass

                # 2. Terminate any direct process if running
                curr = get_scanner_state(state_path)
                pid = curr.get("pid")
                if pid:
                    try:
                        os.kill(pid, signal.SIGTERM)
                    except OSError:
                        pass

                curr["running"] = False
                curr["speed"] = 0.0
                update_scanner_state(state_path, curr)
                _stats_cache.clear()
                self.reply(200, {"success": True, "message": "Scanner stopped successfully"})
                return

            if parsed.path == "/api/v1/intel/scan":
                target_url = data.get("url") or parse_qs(parsed.query).get("url", [""])[0]
                if not target_url:
                    self.reply(400, {"error": "Missing url"})
                    return
                try:
                    with sqlite3.connect(database, timeout=15) as db_rw:
                        init_intel_tables(db_rw)
                        res = fetch_and_index_url(db_rw, target_url)
                    self.reply(200, {"success": True, "item": res})
                except Exception as e:
                    self.reply(500, {"error": str(e)})
                return

            self.reply(404, {"error": "Endpoint not found"})

    return Handler


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--database", type=Path, default=DEFAULT_DATABASE)
    parser.add_argument("--port", type=int, default=3500)
    args = parser.parse_args()
    key = os.environ.get("INTERNET_MAP_API_KEY", "")
    if len(key) < 32:
        parser.error("Set INTERNET_MAP_API_KEY to a secret of at least 32 characters")

    # Initialize tables
    db_init = sqlite3.connect(args.database)
    init_intel_tables(db_init)
    db_init.close()

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
