import io
import contextlib
from pathlib import Path
import tempfile
import unittest

import main


class CLITests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.db_path = Path(self.temp.name) / "test_cli.db"

    def test_seed_and_stats(self):
        db = main.connect(self.db_path)
        try:
            with contextlib.redirect_stdout(io.StringIO()):
                main.seed_catalog(db, count=25)
            urls = db.execute("SELECT count(*) FROM sites").fetchone()[0]
            self.assertEqual(urls, 25)
            obs = db.execute("SELECT count(*) FROM observations").fetchone()[0]
            self.assertEqual(obs, 25)
            pages = db.execute("SELECT count(*) FROM page_contents").fetchone()[0]
            self.assertTrue(pages > 0)
        finally:
            db.close()

    def test_search_and_export(self):
        db = main.connect(self.db_path)
        try:
            with contextlib.redirect_stdout(io.StringIO()):
                main.seed_catalog(db, count=25)

            # Search intel
            from intel import search_intel
            res = search_intel(db, "WordPress")
            self.assertTrue(res["total"] > 0)

            # Search with signature
            res_sig = search_intel(db, signature="Google Analytics")
            self.assertTrue(res_sig["total"] > 0)
        finally:
            db.close()


if __name__ == "__main__":
    unittest.main()
