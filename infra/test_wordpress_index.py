import argparse
import json
import sqlite3
import tempfile
import unittest
from contextlib import closing
from datetime import datetime,timezone
from pathlib import Path
from unittest.mock import patch
import wordpress_generator as generator
import wordpress_sources as sources
import wordpress_index as index

PART='cc-index/table/cc-main/warc/crawl=CC-MAIN-2026-43/subset=warc/part-test.parquet'
class IndexTest(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory();self.addCleanup(self.temp.cleanup)
        self.root=Path(self.temp.name);self.state=self.root/'state.db';self.catalog=self.root/'catalog.db'
        generator.initialize(self.state);index.schema(self.state)
        sources.update(self.root,sources.COMMONCRAWL_ONLY);sources.set_mode(self.root,'index')
        with closing(sqlite3.connect(self.catalog)) as db,db:db.execute('CREATE TABLE candidates(host TEXT PRIMARY KEY,url TEXT,sources INTEGER)')
        with closing(sqlite3.connect(self.state)) as db,db:db.execute('INSERT INTO cc_index_parts(path,crawl,updated) VALUES (?,\'CC-MAIN-2026-43\',0)',(PART,))
    def test_scanning_off_collects_unique_hosts_until_target_without_shards(self):
        (self.root/'paused').touch()
        with closing(sqlite3.connect(self.state)) as db,db:
            db.execute('UPDATE pool_policy SET target=3,available=0,refilling=1')
        self.assertTrue(index.active(self.root))
        consumed,added=index.stage_hosts(self.root,self.state,self.catalog,PART,['a.example','a.example','b.example'])
        self.assertEqual((consumed,added),(3,2))
        self.assertEqual(index.stage_hosts(self.root,self.state,self.catalog,PART,['c.example','d.example']),(1,1))
        self.assertEqual(index.stage_hosts(self.root,self.state,self.catalog,PART,['e.example']),(0,0))
        with closing(sqlite3.connect(self.state)) as db:
            self.assertEqual(db.execute('SELECT available,refilling FROM pool_policy').fetchone(),(3,0))
            self.assertEqual(db.execute('SELECT count(*) FROM shard_leases').fetchone(),(0,))
        self.assertTrue((self.root/'paused').exists())

    def test_crawl_range_excludes_older_future_and_invalid_records(self):
        now=datetime(2026,11,22,tzinfo=timezone.utc)
        rows=[{'id':str(n),'from':date} for n,date in enumerate(('2024-11-21T23:59:59','2024-11-22T00:00:00','2025-01-01T00:00:00','2026-11-23T00:00:00','bad'))]
        self.assertEqual([r['id'] for r in sources.recent_crawls(rows,now)],['1','2'])
    def test_official_inventory_cache_avoids_metadata_endpoint_outage(self):
        records=[{'id':'CC-MAIN-2026-43','from':'2026-10-05T10:56:23'}]
        (self.root/'crawl-collections.json').write_text(json.dumps(records))
        with patch.object(index,'urlopen',side_effect=AssertionError('Cache should serve the inventory')) as fetch:
            self.assertEqual(sources.collection_records(self.root,fetch=fetch),records)
        self.assertEqual(sources.recent_crawls(records,datetime(2026,11,22,tzinfo=timezone.utc)),records)

    def test_mode_changes_preserve_source_flags_and_output_session(self):
        flags=sources.read(self.root);(self.root/'output-session.json').write_text('same session')
        sources.set_mode(self.root,'wat');self.assertEqual(sources.mode(self.root),'wat')
        self.assertEqual(flags,sources.read(self.root));self.assertEqual((self.root/'output-session.json').read_text(),'same session')
        with self.assertRaises(ValueError):sources.set_mode(self.root,'html')
    def test_index_hosts_go_directly_to_workers_and_replays_stay_unique(self):
        with closing(sqlite3.connect(self.state)) as db,db:db.execute("INSERT INTO generator_checked VALUES ('used.example')")
        consumed,queued=index.stage_hosts(self.root,self.state,self.catalog,PART,['new.example','www.new.example','used.example'])
        self.assertEqual((consumed,queued),(3,1))
        self.assertEqual(index.stage_hosts(self.root,self.state,self.catalog,PART,['new.example']),(1,0))
        with closing(sqlite3.connect(self.state)) as db:
            self.assertEqual(db.execute('SELECT host,source FROM ready_candidates').fetchall(),[('new.example','commoncrawl-index')])
            self.assertEqual(db.execute('SELECT position,queued FROM cc_index_parts').fetchone(),(4,1))
    def test_index_mode_never_scans_legacy_catalog_with_unknown_crawl_age(self):
        args=argparse.Namespace(root=self.root,catalog=self.catalog,batch_limit=10000,pooled=True)
        with closing(sqlite3.connect(self.catalog)) as db,db:db.execute("INSERT INTO candidates VALUES ('old.example','https://old.example/',32)")
        with patch.object(generator,'stock_candidates',side_effect=AssertionError('Legacy crawl age is unknown')):
            self.assertEqual(generator.refill_pool(args,self.state,fast=True),0)
    def test_resume_checkpoints_only_committed_hosts_and_respects_mode_off(self):
        reader=index.Reader(self.root,self.state,self.catalog)
        with patch.object(index,'hostname_batches',return_value=iter([['fresh.example'],['second.example']])):reader.process(PART,0)
        with closing(sqlite3.connect(self.state)) as db:
            self.assertEqual(db.execute('SELECT position,done,claimed FROM cc_index_parts').fetchone(),(2,1,0))
        sources.set_mode(self.root,'wat')
        self.assertEqual(index.stage_hosts(self.root,self.state,self.catalog,PART,['never.example']),(0,0))

if __name__=='__main__':unittest.main()
