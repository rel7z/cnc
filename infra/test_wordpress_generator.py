import argparse
import base64
import io
import json
import sqlite3
import struct
import tempfile
import unittest
from contextlib import closing
from pathlib import Path
from unittest.mock import patch
from urllib.error import HTTPError

import wordpress_bulk as bulk
import wordpress_ct as ct
import wordpress_generator as generator


class WordPressGeneratorTest(unittest.TestCase):
    def test_ct_wire_formats(self):
        stamp = struct.pack(">QH", 0, 0)
        cert = b"\x00\x00\x03DER"
        leaf = base64.b64encode(b"\x00\x00" + stamp + cert).decode()
        dynamic = json.dumps({"entries": [{"leaf_input": leaf}]}).encode()
        with patch.object(ct, "names", lambda der: [der.decode()]):
            with patch.object(ct, "fetch", return_value=dynamic):
                self.assertEqual(list(ct.dynamic_entries("https://example.org/", 0, 1)), [["DER"]])
            with patch.object(ct, "fetch", return_value=stamp + cert + bytes(4)) as fetch:
                self.assertEqual(ct.tiled_entries("https://example.org/", 256, 257), [["DER"]])
                fetch.assert_called_once_with("https://example.org/tile/data/001.p/1")

    def test_feed_cursor_survives_restart(self):
        with tempfile.TemporaryDirectory() as tmp:
            state = Path(tmp) / "state.db"
            generator.initialize(state)
            log = ("ct-log", "https://example.org/", False)
            with patch.object(ct, "head", return_value=4), patch.object(ct, "dynamic_entries") as entries:
                generator.poll_log(state, log)
                entries.assert_not_called()
            with patch.object(ct, "head", return_value=5), patch.object(
                ct, "dynamic_entries", return_value=iter([["site.example", "www.site.example", "*.wild.example"]]),
            ):
                generator.poll_log(state, log)
            with closing(sqlite3.connect(state)) as db:
                self.assertEqual(db.execute("SELECT position FROM cursors").fetchone(), (5,))
                self.assertEqual(db.execute("SELECT host FROM fresh").fetchall(), [("site.example",)])

    def test_lost_submit_then_reconcile(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            args = argparse.Namespace(root=root, catalog=root / "catalog.db", data=root, merged=root,
                                      batch_limit=1000, threads=64)
            state = root / "state.db"
            generator.initialize(state)
            seed = root / "seed.txt"
            seed.write_text("backlog.example\n")
            with closing(sqlite3.connect(args.catalog)) as db, db:
                bulk.import_urls(db, seed, "webgraph", "txt")
            with closing(sqlite3.connect(state)) as db, db:
                db.execute("INSERT INTO fresh VALUES ('site.example','https://site.example/',1)")

            class API:
                job = None
                submissions = 0

                def call(self, path, payload=None):
                    if path == "/api/jobs":
                        return []
                    if path == "/api/workers":
                        return [{"wordpress_bulk": True, "enum_checkpoint": True, "enum_stream_receipts": True,
                                 "status": "online", "current_load": 0}]
                    if payload:
                        self.submissions += 1
                        ident = "enum_fresh_" + payload["fresh_batch_id"]
                        self.job = {"id": ident, "name": "Tool: wordpress-bulk", "status": "completed",
                                    "total_tasks": 1, "output_file": f"wordpress-fresh-{ident}.txt"}
                        raise OSError("lost launch response")
                    if not self.job:
                        raise HTTPError(path, 404, "not found", {}, io.BytesIO())
                    if path.endswith("/tasks"):
                        return [{"id": "task", "job_id": self.job["id"], "status": "completed",
                                 "result": {"exit_code": 0, "stderr": "[*] homepage outcomes verified=1\n[*] targets=1 errors=0"}}]
                    return self.job

            api = API()
            (root / "paused").touch()
            generator.dispatch(api, args, state)
            self.assertEqual(api.submissions, 0)
            (root / "paused").unlink()
            with self.assertRaises(OSError):
                generator.dispatch(api, args, state)
            snapshot = root / "enum-jobs" / api.job["id"] / "input.txt"
            snapshot.parent.mkdir(parents=True)
            snapshot.write_text("https://site.example/\n")
            (root / api.job["output_file"]).write_text("https://site.example/\n")
            (root / "paused").touch()
            self.assertTrue(generator.dispatch(api, args, state))
            self.assertEqual(api.submissions, 1)
            self.assertEqual((root / "wordpress.txt").read_text(), "https://site.example/\n")
            self.assertFalse((root / api.job["output_file"]).exists())
            self.assertTrue((root / "archive" / api.job["output_file"]).is_file())
            with closing(sqlite3.connect(args.catalog)) as db:
                self.assertEqual(db.execute("SELECT host FROM attempted").fetchall(), [("site.example",)])
