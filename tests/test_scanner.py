import os
from pathlib import Path
import tempfile
import unittest
import sqlite3
import json

from scanner import (
    is_pid_alive,
    get_scanner_state,
    update_scanner_state,
    fetch_next_urls_batch,
)
from main import connect


class ScannerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.state_file = Path(self.temp.name) / "state.json"
        self.db_path = Path(self.temp.name) / "test.db"
        self.db = connect(self.db_path)
        from intel import init_intel_tables
        from scanner import init_scanner_tables
        init_intel_tables(self.db)
        init_scanner_tables(self.db)

    def tearDown(self):
        self.db.close()

    def test_is_pid_alive(self):
        # Current process should be alive
        self.assertTrue(is_pid_alive(os.getpid()))
        # Invalid / dead PID should be false
        self.assertFalse(is_pid_alive(99999999))
        self.assertFalse(is_pid_alive(0))
        self.assertFalse(is_pid_alive(-1))

    def test_state_management(self):
        # Initial missing state
        state = get_scanner_state(self.state_file)
        self.assertFalse(state["running"])
        self.assertEqual(state["scanned"], 0)

        # Write active state with dead PID
        fake_state = {
            "running": True,
            "pid": 99999999,
            "scanned": 100,
            "html_indexed": 50,
            "wordpress_found": 10,
            "speed": 12.5,
            "last_url": "https://test.org/",
        }
        update_scanner_state(self.state_file, fake_state)

        # Reading state should detect dead PID and auto-update running to False
        recovered = get_scanner_state(self.state_file)
        self.assertFalse(recovered["running"])
        self.assertEqual(recovered["scanned"], 100)
        self.assertEqual(recovered["speed"], 0.0)

    def test_fetch_next_urls_batch(self):
        # Insert sample sites
        self.db.executemany("INSERT INTO sites VALUES (?, ?)", [
            ("https://blog-tech.org/", "blog-tech.org"),
            ("https://community-hub.org/", "community-hub.org"),
            ("https://random-site123.org/", "random-site123.org"),
        ])
        self.db.commit()

        # Batch should return priority sites first
        urls, pfx_idx = fetch_next_urls_batch(self.db, batch_size=2)
        self.assertEqual(len(urls), 1)
        self.assertEqual(urls[0], "https://blog-tech.org/")


if __name__ == "__main__":
    unittest.main()
