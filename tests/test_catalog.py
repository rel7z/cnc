import argparse
import contextlib
import io
import json
from pathlib import Path
import tempfile
import unittest

import main
import api
import cnc_import
import org_zone


class CatalogTests(unittest.TestCase):
    def test_shared_database_default(self):
        expected = Path(main.__file__).resolve().parent / 'data/catalog.db'
        for module in (main, api, cnc_import, org_zone):
            self.assertEqual(module.DEFAULT_DATABASE, expected)

    def test_relative_wordpress_resources(self):
        for path in ('wp-includes/js/a.js', '/wp-includes/js/a.js',
                     '../wp-includes/js/a.js', 'https://example.org/wp-includes/js/a.js'):
            self.assertEqual(main.wordpress_evidence(f'<script src="{path}"></script>'),
                             ['wp-includes resource reference'])
        self.assertEqual(main.wordpress_evidence('<script src="fake-wp-includes/a.js"></script>'), [])

    def test_invalid_observation_rolls_back_whole_import(self):
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder)
            source = path / 'records.jsonl'
            source.write_text('\n'.join(map(json.dumps, [
                {'url': 'example.org', 'wordpress_evidence': []},
                {'url': 'other.org', 'wordpress_evidence': ['invalid label']},
            ])))
            db = main.connect(path / 'catalog.db')
            try:
                with self.assertRaises(ValueError):
                    main.ingest(db, argparse.Namespace(input=source, format='observations', source='test'))
                for table in ('imports', 'sites', 'observations'):
                    self.assertEqual(db.execute(f'SELECT count(*) FROM {table}').fetchone()[0], 0)
            finally:
                db.close()

    def test_duplicate_evidence_merges_in_either_order(self):
        records = [
            {'url': 'example.org/a', 'html': ''},
            {'url': 'example.org/b', 'html': '<meta name="generator" content="WordPress">'},
            {'url': 'example.org/c', 'html': '<script src="wp-content/a.js"></script>'},
        ]
        for rows in (records, list(reversed(records))):
            with tempfile.TemporaryDirectory() as folder:
                path = Path(folder)
                source = path / 'records.jsonl'
                source.write_text('\n'.join(map(json.dumps, rows)))
                db = main.connect(path / 'catalog.db')
                try:
                    with contextlib.redirect_stdout(io.StringIO()):
                        main.ingest(db, argparse.Namespace(input=source, format='jsonl', source='test'))
                    evidence, = db.execute('SELECT evidence FROM observations').fetchone()
                    self.assertEqual(json.loads(evidence), ['WordPress generator tag', 'wp-content resource reference'])
                    self.assertEqual(db.execute('SELECT accepted FROM imports').fetchone()[0], 1)
                finally:
                    db.close()

    def test_zone_quote_aware_comments_and_deduplication(self):
        zone_data = """
$ORIGIN org.
$TTL 86400
org. IN TXT ( "v=spf1; include:example.com" ) ; multiline comment
example.org. IN NS ns1.example.org.
             IN NS ns2.example.org.
test.org.    IN NS ns1.test.org.
"""
        results = list(org_zone.delegations(zone_data.strip().splitlines()))
        self.assertEqual(results, ["example.org", "test.org"])


if __name__ == '__main__':
    unittest.main()
