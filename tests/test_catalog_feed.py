import argparse
from contextlib import closing
from pathlib import Path
import sqlite3
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).parents[1] / 'discovery'))
import catalog_feed
import wordpress_generator as generator
import wordpress_sources as sources

class CatalogFeedTests(unittest.TestCase):
    def test_catalog_fills_unique_bounded_pool_while_scanning_off(self):
        with tempfile.TemporaryDirectory() as tmp:
            catalog=Path(tmp)/'catalog.db';root=Path(tmp)/'discovery';root.mkdir()
            (root/'cutover.json').write_text('{}');(root/'paused').touch()
            state=root/'generator-state.db';generator.initialize(state)
            with closing(sqlite3.connect(catalog)) as db,db:
                db.execute('CREATE TABLE sites(url TEXT PRIMARY KEY,host TEXT)')
                db.executemany('INSERT INTO sites VALUES (?,?)',[(u,h) for u,h in [('http://a.example/','a.example'),('https://a.example/','a.example'),('https://b.example/','b.example'),('https://c.example/','c.example')]])
            with closing(sqlite3.connect(state)) as db,db:db.execute('UPDATE pool_policy SET target=2,refilling=1')
            for _ in range(5):catalog_feed.stage(root,state,catalog)
            with closing(sqlite3.connect(state)) as db:
                self.assertEqual(db.execute('SELECT available,target,refilling FROM pool_policy').fetchone(),(2,2,0))
                self.assertEqual(db.execute('SELECT count(DISTINCT host) FROM ready_candidates').fetchone(),(2,))
                self.assertEqual(db.execute('SELECT DISTINCT source FROM ready_candidates').fetchall(),[('catalog',)])
            self.assertIn('catalog',sources.allowed(sources.read(root),root))
            generator.fill_shards(argparse.Namespace(root=root,catalog=catalog,batch_limit=1,pooled=True),state,1)
            with closing(sqlite3.connect(state)) as db:self.assertEqual(db.execute('SELECT count(*) FROM shard_leases').fetchone(),(0,))
            self.assertTrue((root/'paused').exists())
