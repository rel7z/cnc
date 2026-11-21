import argparse
import base64
import gzip
import io
import json
import sqlite3
import struct
import tempfile
import threading
import unittest
from concurrent.futures import Future, ThreadPoolExecutor
from contextlib import closing
from pathlib import Path
from unittest.mock import patch
from urllib.error import HTTPError

import wordpress_bulk as bulk
import wordpress_ct as ct
import wordpress_generator as generator


class WordPressGeneratorTest(unittest.TestCase):
    def test_pool_refills_only_consumed_hosts_to_target_and_survives_restart(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);state=root/"state.db";catalog=root/"catalog.db"
            args=argparse.Namespace(root=root,catalog=catalog,batch_limit=8,pooled=True)
            with patch.multiple(generator,POOL_MIN=20,POOL_MAX=20,POOL_LOW=20,POOL_PAGE=7):
                generator.initialize(state)
                with closing(sqlite3.connect(catalog)) as db,db:
                    db.execute("CREATE TABLE candidates(host TEXT PRIMARY KEY,url TEXT,sources INTEGER)")
                    db.execute("CREATE TABLE attempted(host TEXT PRIMARY KEY)")
                with closing(sqlite3.connect(state)) as db,db:
                    db.executemany("INSERT INTO fresh VALUES (?,?,1)",[(f"ct{i:03}.example",f"https://ct{i:03}.example/") for i in range(100)])
                with closing(sqlite3.connect(state)) as db,db:
                    db.executemany("INSERT INTO reverse_candidates VALUES (?,?)",[(f"rev{i:03}.example",f"https://rev{i:03}.example/") for i in range(100)])
                generator.refill_pool(args,state)
                generator.fill_shards(args,state,1)
                self.assertEqual(list(root.glob("*.txt")),[])
                for _ in range(8):generator.refill_pool(args,state)
                generator.fill_shards(args,state,1)
                with closing(sqlite3.connect(state)) as db:
                    self.assertEqual(generator.pool_snapshot(db)[0],12)
                generator.initialize(state)
                self.assertEqual(generator.refill_pool(args,state),7)
                self.assertEqual(generator.refill_pool(args,state),1)
                self.assertEqual(generator.refill_pool(args,state),0)
                with closing(sqlite3.connect(state)) as db:
                    self.assertEqual(generator.pool_snapshot(db),(20,1,0,20))
                    self.assertEqual(db.execute("SELECT count(*) FROM shard_hosts").fetchone(),(8,))
                    self.assertEqual(db.execute("SELECT count(*) FROM ready_candidates r JOIN shard_hosts s USING(host)").fetchone(),(0,))

    def test_pool_never_exceeds_cap_or_replays_checked_and_cross_source_hosts(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp)
            state,catalog=root/"state.db",root/"catalog.db"
            args=argparse.Namespace(root=root,catalog=catalog)
            generator.initialize(state)
            with closing(sqlite3.connect(catalog)) as db, db:
                db.execute("CREATE TABLE candidates(host TEXT PRIMARY KEY,url TEXT,sources INTEGER)")
                db.execute("CREATE TABLE attempted(host TEXT PRIMARY KEY)")
                db.execute("INSERT INTO attempted VALUES ('attempted.example')")
                db.execute("INSERT INTO candidates VALUES ('attempted.example','https://attempted.example/',8)")
            with closing(sqlite3.connect(state)) as db, db:
                db.execute("UPDATE pool_policy SET target=10")
                rows=[(f"host{i}.example",f"https://host{i}.example/") for i in range(25)]
                db.executemany("INSERT INTO fresh VALUES (?,?,1)",rows)
                db.executemany("INSERT INTO reverse_candidates VALUES (?,?)",rows)
                db.execute("INSERT INTO generator_checked VALUES ('host0.example')")
                db.execute("INSERT INTO merged_hosts VALUES ('host1.example','https://host1.example/')")
                db.execute("INSERT INTO shard_hosts VALUES ('host2.example','active','ct')")
                db.execute("INSERT INTO deferred VALUES ('host3.example',unixepoch()+999)")
            with patch.multiple(generator,POOL_MIN=5,POOL_MAX=10,POOL_LOW=3,POOL_PAGE=50):
                self.assertEqual(generator.refill_pool(args,state),10)
                self.assertEqual(generator.refill_pool(args,state),0)
            with closing(sqlite3.connect(state)) as db:
                self.assertEqual(generator.pool_snapshot(db),(10,1,0,10))
                hosts={h for h, in db.execute("SELECT host FROM ready_candidates")}
                self.assertFalse(hosts & {f"host{i}.example" for i in range(4)})
                self.assertNotIn("attempted.example",hosts)

    def test_pool_pause_and_exact_low_water_boundary(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp); state=root/"state.db"
            args=argparse.Namespace(root=root,catalog=root/"catalog.db")
            generator.initialize(state)
            with closing(sqlite3.connect(state)) as db, db:
                db.execute("UPDATE pool_policy SET available=1000000,started=1,refilling=0")
            with patch.object(generator,"mixed_candidates",return_value=[]) as query:
                self.assertEqual(generator.refill_pool(args,state),0)
                query.assert_not_called()
                (root/"paused").touch()
                with closing(sqlite3.connect(state)) as db, db:
                    db.execute("UPDATE pool_policy SET available=999999")
                self.assertEqual(generator.refill_pool(args,state),0)
                query.assert_called_once()
                with closing(sqlite3.connect(state)) as db:
                    self.assertEqual(generator.pool_snapshot(db)[2],1)

    def test_pool_hysteresis_restarts_at_one_million_and_refills_to_five(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);state=root/"state.db"
            args=argparse.Namespace(root=root,catalog=root/"catalog.db")
            generator.initialize(state)
            with closing(sqlite3.connect(state)) as db,db:
                db.execute("UPDATE pool_policy SET available=1000000,started=1,refilling=0")
            with patch.object(generator,"mixed_candidates",return_value=[]) as query:
                generator.initialize(state)
                self.assertEqual(generator.refill_pool(args,state),0)
                query.assert_not_called()
                with closing(sqlite3.connect(state)) as db,db:
                    db.execute("UPDATE pool_policy SET available=999999")
                generator.refill_pool(args,state)
                query.assert_called_once()
                with closing(sqlite3.connect(state)) as db:
                    self.assertEqual(generator.pool_snapshot(db),(999999,1,1,5000000))
                # Crossing the low watermark does not end an active refill.
                with closing(sqlite3.connect(state)) as db,db:
                    db.execute("UPDATE pool_policy SET available=3000000")
                generator.initialize(state)
                with closing(sqlite3.connect(state)) as db:
                    self.assertEqual(generator.pool_snapshot(db),(3000000,1,1,5000000))
                generator.refill_pool(args,state)
                self.assertEqual(query.call_count,2)
                with closing(sqlite3.connect(state)) as db,db:
                    db.execute("UPDATE pool_policy SET available=5000000")
                generator.refill_pool(args,state)
                self.assertEqual(query.call_count,2)
                with closing(sqlite3.connect(state)) as db:
                    self.assertEqual(generator.pool_snapshot(db),(5000000,1,0,5000000))
                # Consumption above the low watermark stays idle after restart.
                with closing(sqlite3.connect(state)) as db,db:
                    db.execute("UPDATE pool_policy SET available=3000000")
                generator.initialize(state)
                generator.refill_pool(args,state)
                self.assertEqual(query.call_count,2)
                with closing(sqlite3.connect(state)) as db:
                    self.assertEqual(generator.pool_snapshot(db),(3000000,1,0,5000000))

    def test_fast_stock_advances_past_consumed_windows_and_preserves_dedup(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);state=root/'state.db';catalog=root/'catalog.db'
            args=argparse.Namespace(root=root,catalog=catalog)
            generator.initialize(state)
            with closing(sqlite3.connect(catalog)) as db,db:
                db.execute('CREATE TABLE candidates(host TEXT PRIMARY KEY,url TEXT,sources INTEGER) WITHOUT ROWID')
                db.execute('CREATE TABLE attempted(host TEXT PRIMARY KEY) WITHOUT ROWID')
                db.executemany('INSERT INTO candidates VALUES (?,?,32)',
                    ((f'host{i:02}.example',f'https://host{i:02}.example/') for i in range(20)))
                db.execute("INSERT INTO attempted VALUES ('host04.example')")
                db.execute("UPDATE candidates SET sources=8 WHERE host='host05.example'")
                db.execute("UPDATE candidates SET sources=1 WHERE host='host06.example'")
            with closing(sqlite3.connect(state)) as db,db:
                db.execute("INSERT INTO generator_checked VALUES ('host00.example')")
                db.execute("INSERT INTO merged_hosts VALUES ('host01.example','https://host01.example/')")
                db.execute("INSERT INTO shard_hosts VALUES ('host02.example','active','ct')")
                db.execute("INSERT INTO deferred VALUES ('host03.example',unixepoch()+999)")
            with patch.object(generator,'POOL_FAST_PAGE',5):
                self.assertEqual(generator.refill_pool(args,state,fast=True),0)
                self.assertTrue(args.pool_scan_progress)
                generator.initialize(state)
                self.assertEqual(generator.refill_pool(args,state,fast=True),4)
                with closing(sqlite3.connect(state)) as db:
                    self.assertEqual(db.execute("SELECT source FROM ready_candidates WHERE host='host06.example'").fetchone(),('archive',))
                for _ in range(5):generator.refill_pool(args,state,fast=True)
            with closing(sqlite3.connect(state)) as db:
                self.assertEqual(db.execute('SELECT count(*) FROM ready_candidates').fetchone()[0],14)
                self.assertEqual(db.execute('SELECT count(*) FROM ready_candidates WHERE host IN (SELECT host FROM shard_hosts)').fetchone()[0],0)

    def test_pool_feed_starts_with_immediate_stock_and_notifies_dispatcher(self):
        args=argparse.Namespace(shard_ready_event=threading.Event())
        with patch.object(generator,'refill_pool',return_value=10000) as refill,patch.object(generator.time,'sleep',side_effect=RuntimeError('end iteration')):
            with self.assertRaisesRegex(RuntimeError,'end iteration'):
                generator.pool_feed(args,Path('unused-state.db'))
        self.assertTrue(args.shard_ready_event.is_set())
        self.assertTrue(refill.call_args.kwargs['fast'])

    def test_fast_stock_cursor_does_not_advance_after_failed_admission(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);state=root/'state.db';catalog=root/'catalog.db'
            args=argparse.Namespace(root=root,catalog=catalog)
            generator.initialize(state)
            with closing(sqlite3.connect(catalog)) as db,db:
                db.execute('CREATE TABLE candidates(host TEXT PRIMARY KEY,url TEXT,sources INTEGER) WITHOUT ROWID')
                db.execute("INSERT INTO candidates VALUES ('fresh.example','https://fresh.example/',32)")
            class Interrupted(sqlite3.Connection):
                def executemany(self,sql,rows):
                    if 'INSERT OR IGNORE INTO ready_candidates' in sql:
                        raise OSError('interrupted admission')
                    return super().executemany(sql,rows)
            def connect(path):return sqlite3.connect(path,timeout=10,factory=Interrupted)
            with patch.object(generator,'connect',side_effect=connect):
                with self.assertRaisesRegex(OSError,'interrupted admission'):
                    generator.refill_pool(args,state,fast=True)
            with closing(sqlite3.connect(state)) as db:
                self.assertEqual(db.execute("SELECT host FROM source_cursors WHERE source='stock'").fetchall(),[])
                self.assertEqual(db.execute('SELECT count(*) FROM ready_candidates').fetchone()[0],0)
            self.assertEqual(generator.refill_pool(args,state,fast=True),1)

    def test_fresh_pool_can_dispatch_first_full_shard_before_five_million(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);state=root/'state.db';catalog=root/'catalog.db'
            args=argparse.Namespace(root=root,catalog=catalog,batch_limit=10000,pooled=True)
            generator.initialize(state)
            with closing(sqlite3.connect(catalog)) as db,db:
                db.execute('CREATE TABLE candidates(host TEXT PRIMARY KEY,url TEXT,sources INTEGER) WITHOUT ROWID')
                db.executemany('INSERT INTO candidates VALUES (?,?,32)',
                    ((f'host{i:05}.example',f'https://host{i:05}.example/') for i in range(12000)))
            self.assertEqual(generator.refill_pool(args,state,fast=True),12000)
            with closing(sqlite3.connect(state)) as db:
                self.assertEqual(generator.pool_snapshot(db),(12000,1,1,5000000))
            generator.fill_shards(args,state,1)
            with closing(sqlite3.connect(state)) as db:
                self.assertEqual(generator.pool_snapshot(db)[0],2000)
                self.assertEqual(db.execute('SELECT count(*) FROM shard_hosts').fetchone()[0],10000)
                self.assertEqual(db.execute('SELECT count(*) FROM ready_candidates r JOIN shard_hosts s USING(host)').fetchone()[0],0)

    def test_random_reverse_skips_nonpublic_multicast_and_previous_ips(self):
        import ipaddress
        with tempfile.TemporaryDirectory() as tmp:
            state=Path(tmp)/"state.db"
            generator.initialize(state)
            with closing(sqlite3.connect(state)) as db, db:
                db.execute("INSERT INTO reverse_lookups VALUES ('8.8.8.8',1)")
                with patch.object(generator.random,"getrandbits",side_effect=[
                    int(ipaddress.IPv4Address(ip)) for ip in ("127.0.0.1","10.1.2.3","224.0.0.1","8.8.8.8","9.9.9.9")
                ]):
                    self.assertEqual(generator.random_reverse_ip(db),"9.9.9.9")
                with patch.object(generator.socket,"getaddrinfo") as dns, patch.object(
                    generator,"reverse_domains",return_value=["fresh.example"]
                ) as reverse:
                    self.assertFalse(generator.reverse_seed(db,"9.9.9.9",100))
                    dns.assert_not_called()
                    reverse.assert_called_once_with("thc","9.9.9.9")

    def test_production_pool_emits_10k_per_worker_for_dynamic_fleet(self):
        self.assertEqual(generator.SHARD_TARGETS,10_000)
        self.assertEqual(generator.SHARDS_PER_WORKER,2)
        self.assertEqual((generator.POOL_MIN,generator.POOL_MAX,generator.POOL_LOW),
                         (5_000_000,5_000_000,1_000_000))
        for workers in (8,9):
            with self.subTest(workers=workers), tempfile.TemporaryDirectory() as tmp:
                root=Path(tmp); state=root/"state.db"
                args=argparse.Namespace(root=root,catalog=root/"unused.db",batch_limit=10_000,pooled=True)
                generator.initialize(state)
                with closing(sqlite3.connect(state)) as db, db:
                    db.execute("UPDATE pool_policy SET started=1")
                    db.executemany("INSERT INTO ready_candidates VALUES (?,?,?)", (
                        (f"host{i:07}.example",f"https://host{i:07}.example/","ct") for i in range(workers*10_000+1)))
                with patch.object(generator,"mixed_candidates") as query:
                    generator.fill_shards(args,state,workers)
                    query.assert_not_called()
                with closing(sqlite3.connect(state)) as db:
                    self.assertEqual(db.execute("SELECT count(*) FROM shard_hosts").fetchone(),(workers*10_000,))
                    self.assertEqual(generator.pool_snapshot(db)[0],1)
                    self.assertEqual(db.execute("SELECT count(*) FROM ready_candidates").fetchone(),(1,))
                    sizes=db.execute("SELECT count(*) FROM shard_hosts GROUP BY batch").fetchall()
                    self.assertEqual(sizes,[(10_000,)]*workers)
                all_hosts=set()
                for batch in root.glob("*.txt"):
                    with batch.open() as stream:
                        lines=stream.read().splitlines()
                    self.assertEqual(len(lines),10_000)
                    self.assertFalse(all_hosts.intersection(lines))
                    all_hosts.update(lines)
                self.assertEqual(len(all_hosts),workers*10_000)

    def test_restaging_shrinks_only_unlaunched_batches_and_keeps_used_hosts_excluded(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);state=root/"state.db"
            generator.initialize(state)
            args=argparse.Namespace(root=root,batch_limit=2)
            (root/"pending.txt").write_text("https://one.example/\nhttps://two.example/\nhttps://used.example/\n")
            with closing(sqlite3.connect(state)) as db,db:
                db.execute("INSERT INTO shard_leases VALUES ('pending',0)")
                db.execute("INSERT INTO shard_leases VALUES ('active',1)")
                db.executemany("INSERT INTO shard_hosts VALUES (?,'pending','ct')",[(h,) for h in ('one.example','two.example','used.example')])
                db.execute("INSERT INTO shard_hosts VALUES ('running.example','active','ct')")
                db.execute("INSERT INTO generator_checked VALUES ('used.example')")
            generator.restage_oversized_shards(args,state)
            generator.restage_oversized_shards(args,state)
            with closing(sqlite3.connect(state)) as db:
                self.assertEqual(set(db.execute("SELECT host FROM ready_candidates")),{('one.example',),('two.example',)})
                self.assertEqual(db.execute("SELECT host FROM shard_hosts").fetchall(),[('running.example',)])
                self.assertEqual(generator.pool_snapshot(db)[0],2)
                self.assertEqual(db.execute("SELECT batch,launched FROM shard_leases").fetchall(),[('active',1)])
            self.assertTrue((root/"archive/resharded/pending.txt").is_file())

    def test_dispatch_replaces_finished_shards_while_result_import_is_blocked(self):
        with tempfile.TemporaryDirectory() as tmp, ThreadPoolExecutor(max_workers=1) as executor:
            root=Path(tmp);state=root/"state.db"
            args=argparse.Namespace(root=root,catalog=root/"unused.db",batch_limit=2,pooled=True,
                                    reconcile_executor=executor,once=False)
            generator.initialize(state)
            with closing(sqlite3.connect(state)) as db,db:
                db.execute("UPDATE pool_policy SET started=1")
                db.execute("INSERT INTO shard_leases VALUES ('old',1)")
                db.execute("INSERT INTO shard_hosts VALUES ('used.example','old','ct')")
                db.executemany("INSERT INTO ready_candidates VALUES (?,?, 'ct')",[(f"new{i}.example",f"https://new{i}.example/") for i in range(4)])
            worker=dict(wordpress_bulk=True,enum_checkpoint=True,enum_stream_receipts=True,
                        status="online",current_load=0,max_tasks=8,fresh_wordpress_slots=2)
            api=argparse.Namespace(call=lambda path: [worker]*2 if path=='/api/workers' else
                                   {'status':'completed_with_errors'} if path.endswith('/enum_fresh_old') else [])
            entered=threading.Event();release=threading.Event();submitted=[]
            def reconcile(_api,_args,_state,batch):
                if batch=='old':
                    entered.set();release.wait(5);return True
                submitted.append(batch)
            try:
                with patch.object(generator,'reconcile_batch',side_effect=reconcile):
                    generator.dispatch_shards(api,args,state)
                    self.assertTrue(entered.wait(1))
                    self.assertEqual(len(submitted),2)
                    self.assertFalse(args.reconcile_future.done())
                    with closing(sqlite3.connect(state)) as db:
                        self.assertEqual(db.execute("SELECT launched FROM shard_leases WHERE batch='old'").fetchone(),(2,))
                        self.assertEqual(db.execute("SELECT count(*) FROM shard_leases WHERE launched=1").fetchone(),(2,))
                        self.assertEqual(db.execute("SELECT host FROM shard_hosts WHERE batch='old'").fetchone(),('used.example',))
                        self.assertEqual(db.execute("SELECT count(*) FROM ready_candidates r JOIN shard_hosts s USING(host)").fetchone(),(0,))
            finally:release.set()

    def test_failed_old_result_cannot_starve_healthy_result_after_cooldown_expires(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);state=root/'state.db';generator.initialize(state)
            with closing(sqlite3.connect(state)) as db,db:
                db.executemany('INSERT INTO shard_leases VALUES (?,2)',[('bad',),('healthy',)])
            visited=[]
            def reconcile(_api,_args,_state,batch):
                visited.append(batch)
                if batch=='bad':raise ValueError('invalid membership')
                return True
            class Immediate:
                def submit(self,function,*args):
                    future=Future()
                    try:future.set_result(function(*args))
                    except Exception as error:future.set_exception(error)
                    return future
            args=argparse.Namespace(reconcile_executor=Immediate())
            api=argparse.Namespace(call=lambda path: [])
            # A slow failure can already have expired by the next selection.
            with patch.object(generator,'reconcile_batch',side_effect=reconcile),patch.object(generator.time,'monotonic',side_effect=[0,0,20]):
                generator.reconcile_shards_async(api,args,state)
                generator.reconcile_shards_async(api,args,state)
            self.assertEqual(visited,['bad','healthy'])
            self.assertTrue(args.reconcile_future.result())

    def test_public_graph_commits_chunks_without_marking_interrupted_shards_complete(self):
        shard='projects/hyperlinkgraph/test/host/vertices/part-00000.gz'
        listing='projects/hyperlinkgraph/test/host/test-host-vertices.paths.gz'
        release='https://commoncrawl.org/blog/host--and-domain-level-web-graphs-test'
        responses={bulk.COMMONCRAWL_BLOG:f'<a href="{release}">graph</a>'.encode(),
                   release:('https://data.commoncrawl.org/'+listing).encode(),
                   bulk.COMMONCRAWL_DATA+listing:gzip.compress((shard+'\n').encode()),
                   bulk.COMMONCRAWL_DATA+shard:gzip.compress(b'dummy')}
        def fetch(url,**_):return io.BytesIO(responses[url.full_url if hasattr(url,'full_url') else url])
        with tempfile.TemporaryDirectory() as tmp:
            catalog=Path(tmp)/'catalog.db'
            with closing(sqlite3.connect(catalog)) as importer,closing(sqlite3.connect(catalog,timeout=0)) as scanner:
                scanner.execute('CREATE TABLE attempted(host TEXT PRIMARY KEY)');scanner.commit()
                def interrupted(*_):
                    yield 'one.example';yield 'two.example'
                    with scanner:
                        scanner.execute("INSERT INTO attempted VALUES ('unrelated.example')")
                        self.assertEqual(scanner.execute('SELECT count(*) FROM candidates').fetchone(),(2,))
                        self.assertEqual(scanner.execute('SELECT count(*) FROM commoncrawl_public_shards').fetchone(),(0,))
                    raise OSError('interrupted download import')
                with patch.object(bulk,'urlopen',side_effect=fetch),patch.object(bulk,'PUBLIC_GRAPH_IMPORT_BATCH',2),patch.object(bulk.time,'sleep'):
                    with patch.object(bulk,'input_urls',side_effect=interrupted):
                        with self.assertRaisesRegex(OSError,'interrupted'):
                            bulk.pull_commoncrawl(importer)
                    self.assertEqual(scanner.execute('SELECT count(*) FROM commoncrawl_public_shards').fetchone(),(0,))
                    with patch.object(bulk,'input_urls',return_value=iter(('one.example','two.example','three.example'))):
                        result=bulk.pull_commoncrawl(importer)
                    self.assertEqual(result['status'],'imported')
                    self.assertEqual(scanner.execute('SELECT count(*) FROM candidates').fetchone(),(3,))
                    self.assertEqual(scanner.execute('SELECT count(*) FROM commoncrawl_public_shards').fetchone(),(1,))

    def test_graph_fallback_collects_while_scanning_paused_until_pool_full(self):
        for available, refilling, paused, expected in (
            (0,1,False,1), (5_000_000,1,False,0), (0,0,False,0), (0,1,True,1)
        ):
            with self.subTest(available=available,refilling=refilling,paused=paused), tempfile.TemporaryDirectory() as tmp:
                root=Path(tmp); state=root/"state.db"
                args=argparse.Namespace(root=root,catalog=root/"catalog.db")
                generator.initialize(state)
                with closing(sqlite3.connect(state)) as db, db:
                    db.execute("UPDATE pool_policy SET available=?,refilling=?",(available,refilling))
                if paused:
                    (root/"paused").touch()
                with patch.object(generator.bulk,"pull_commoncrawl",return_value={"status":"imported"}) as pull, \
                     patch.object(generator.time,"sleep",side_effect=RuntimeError("end iteration")):
                    with self.assertRaisesRegex(RuntimeError,"end iteration"):
                        generator.graph_feed(args,state)
                    self.assertEqual(pull.call_count,expected)

    def test_sparse_source_window_skips_history_lookups_for_other_sources(self):
        with tempfile.TemporaryDirectory() as tmp:
            state=Path(tmp)/"state.db"
            generator.initialize(state)
            with closing(sqlite3.connect(state)) as db, db:
                db.execute("CREATE TABLE source_fixture(host TEXT PRIMARY KEY,url TEXT,sources INTEGER)")
                db.executemany("INSERT INTO source_fixture VALUES (?,?,1)",(
                    (f"other{i:04}.example",f"https://other{i:04}.example/") for i in range(1000)))
                db.execute("INSERT INTO source_fixture VALUES ('wanted.example','https://wanted.example/',32)")
                looked_up=[]
                db.create_function("history_probe",1,lambda host: looked_up.append(host) or 1)
                rows=generator.candidate_page(db,"sparse","source_fixture","(c.sources & 32)!=0",
                                              "history_probe(c.host)","",100,False)
                self.assertEqual(rows,[("wanted.example","https://wanted.example/")])
                self.assertEqual(looked_up,["wanted.example"])

    def test_reverse_cache_evicts_sharded_hosts_when_shard_is_created(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            state = root / "state.db"
            args = argparse.Namespace(root=root, catalog=root / "catalog.db", batch_limit=1)
            generator.initialize(state)
            with closing(sqlite3.connect(state)) as db, db:
                db.executemany("INSERT INTO reverse_candidates VALUES (?,?)", [
                    ("used.example", "https://used.example/"), ("unused.example", "https://unused.example/"),
                ])
            with patch.object(generator, "mixed_candidates", return_value=[
                ("used.example", "https://used.example/", "reverse-seed")
            ]):
                generator.fill_shards(args, state, 1)
            with closing(sqlite3.connect(state)) as db:
                batch, = db.execute("SELECT batch FROM shard_leases").fetchone()
                self.assertEqual(db.execute("SELECT host FROM reverse_candidates").fetchall(), [("unused.example",)])
                self.assertEqual(db.execute("SELECT host FROM shard_hosts").fetchall(), [("used.example",)])
                self.assertTrue((root / (batch + ".txt")).is_file())

    def test_reverse_cache_persists_across_two_hours_and_restart(self):
        with tempfile.TemporaryDirectory() as tmp:
            state = Path(tmp) / "state.db"
            generator.initialize(state)
            with closing(sqlite3.connect(state)) as db, db:
                db.execute("INSERT INTO reverse_candidates VALUES ('site.example','https://site.example/')")
                db.execute("INSERT INTO ready_candidates VALUES ('site.example','https://site.example/','reverse-seed')")
                db.execute("INSERT INTO source_cursors VALUES ('reverse-seed','site.example')")
                db.execute("INSERT INTO source_cursors VALUES ('archive','archive.example')")
                generator.expire_reverse_cache(db, 100)
            generator.initialize(state)
            with closing(sqlite3.connect(state)) as db:
                generator.expire_reverse_cache(db, 7299)
                self.assertEqual(db.execute("SELECT count(*) FROM reverse_candidates").fetchone(), (1,))
                generator.expire_reverse_cache(db, 7300)
                self.assertEqual(db.execute("SELECT count(*) FROM reverse_candidates").fetchone(), (1,))
                self.assertEqual(db.execute("SELECT count(*) FROM ready_candidates").fetchone(), (1,))
                self.assertEqual(set(db.execute("SELECT source FROM source_cursors").fetchall()), {("archive",), ("reverse-seed",)})
                self.assertIsNone(db.execute("SELECT expires_at FROM reverse_cache_timer").fetchone())

    def test_fresh_schedule_backfills_and_tracks_cooldowns(self):
        with tempfile.TemporaryDirectory() as tmp:
            state = Path(tmp) / "state.db"
            with closing(sqlite3.connect(state)) as db, db:
                db.execute("CREATE TABLE fresh(host TEXT PRIMARY KEY,url TEXT NOT NULL,seen INTEGER NOT NULL)")
                db.execute("CREATE TABLE deferred(host TEXT PRIMARY KEY,until INTEGER NOT NULL)")
                db.execute("INSERT INTO fresh VALUES ('old.example','https://old.example/',1)")
                db.execute("INSERT INTO deferred VALUES ('old.example',100)")
            generator.initialize(state)
            with closing(sqlite3.connect(state)) as db, db:
                self.assertEqual(db.execute("SELECT * FROM fresh_schedule").fetchall(), [("old.example", 100)])
                db.execute("INSERT OR REPLACE INTO deferred VALUES ('old.example',200)")
                db.execute("INSERT INTO fresh VALUES ('new.example','https://new.example/',2)")
                self.assertEqual(db.execute("SELECT * FROM fresh_schedule ORDER BY host").fetchall(),
                                 [("new.example", 0), ("old.example", 200)])
                db.execute("DELETE FROM deferred WHERE host='old.example'")
                db.execute("DELETE FROM fresh WHERE host='new.example'")
            generator.initialize(state)
            with closing(sqlite3.connect(state)) as db:
                self.assertEqual(db.execute("SELECT * FROM fresh_schedule").fetchall(), [("old.example", 0)])

    def test_primary_sources_displace_crawl_catalogs_and_do_not_replay_leases(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            state, catalog = root/'state.db', root/'catalog.db'
            generator.initialize(state)
            with closing(sqlite3.connect(catalog)) as db, db:
                db.execute('CREATE TABLE candidates(host TEXT PRIMARY KEY,url TEXT,sources INTEGER)')
                db.executemany('INSERT INTO candidates VALUES (?,?,?)', (
                    (f'crawl{i}.example', f'https://crawl{i}.example/', bulk.SOURCES['webgraph'] | bulk.SOURCES['commoncrawl']) for i in range(200)))
            with closing(sqlite3.connect(state)) as db, db:
                db.executemany('INSERT INTO fresh VALUES (?,?,1)', (
                    (f'ct{i}.example', f'https://ct{i}.example/') for i in range(300)))
                db.executemany('INSERT INTO reverse_candidates VALUES (?,?)', (
                    (f'reverse{i}.example', f'https://reverse{i}.example/') for i in range(300)))
                first = generator.mixed_candidates(db, catalog, 100)
                self.assertEqual(sum(source=='ct' for _,_,source in first),10)
                self.assertEqual(sum(source=='reverse-seed' for _,_,source in first),80)
                db.executemany('INSERT INTO shard_hosts VALUES (?, ?, ?)', ((host,'batch1',source) for host,_,source in first))
            with closing(sqlite3.connect(state)) as db, db:
                second = generator.mixed_candidates(db, catalog, 100)
                self.assertEqual(sum(source=='ct' for _,_,source in second),10)
                self.assertEqual(sum(source=='reverse-seed' for _,_,source in second),80)
                self.assertFalse({h for h,_,_ in first} & {h for h,_,_ in second})

    def test_reverse_candidate_page_moves_past_attempted_hosts(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            state, catalog = root / "state.db", root / "catalog.db"
            generator.initialize(state)
            hosts = [f"host-{index:04d}.example" for index in range(1025)]
            with closing(sqlite3.connect(catalog)) as db, db:
                db.execute("CREATE TABLE candidates(host TEXT PRIMARY KEY,url TEXT,sources INTEGER)")
                db.execute("CREATE TABLE attempted(host TEXT PRIMARY KEY)")
                db.executemany("INSERT INTO attempted VALUES (?)", ((host,) for host in hosts[:-1]))
            with closing(sqlite3.connect(state)) as db, db:
                db.executemany("INSERT INTO reverse_candidates VALUES (?,?)",
                               ((host, f"https://{host}/") for host in hosts))
            with closing(sqlite3.connect(state)) as db, patch.object(generator, "MAX_CANDIDATE_PAGE_ROWS", 1024):
                self.assertEqual(generator.mixed_candidates(db, catalog, 512), [])
                self.assertEqual(db.execute("SELECT host FROM source_cursors WHERE source='reverse-seed'")
                                 .fetchone(), (hosts[-2],))
            with closing(sqlite3.connect(state)) as db:
                self.assertEqual(generator.mixed_candidates(db, catalog, 1),
                                 [(hosts[-1], f"https://{hosts[-1]}/", "reverse-seed")])
            with closing(sqlite3.connect(catalog)) as db, db:
                db.execute("DELETE FROM attempted WHERE host=?", (hosts[0],))
                db.execute("INSERT INTO attempted VALUES (?)", (hosts[-1],))
            with closing(sqlite3.connect(state)) as db:
                self.assertEqual(generator.mixed_candidates(db, catalog, 1), [])
            with closing(sqlite3.connect(state)) as db:
                self.assertEqual(generator.mixed_candidates(db, catalog, 1),
                                 [(hosts[0], f"https://{hosts[0]}/", "reverse-seed")])

    def test_verified_seed_queue_has_no_200_host_truncation(self):
        with tempfile.TemporaryDirectory() as tmp:
            state = Path(tmp)/'state.db'
            generator.initialize(state)
            with closing(sqlite3.connect(state)) as db, db:
                hosts = [f'site{i}.example' for i in range(1500)]
                generator.queue_verified_seeds(db, hosts, 1)
                generator.queue_verified_seeds(db, hosts, 2)
                self.assertEqual(db.execute('SELECT count(*) FROM reverse_seeds').fetchone(), (1500,))

    def test_ct_sampling_randomizes_before_limit_not_just_after_prefix(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            state, catalog = root/'state.db',root/'catalog.db'
            generator.initialize(state)
            with closing(sqlite3.connect(catalog)) as db, db:
                db.execute('CREATE TABLE candidates(host TEXT PRIMARY KEY,url TEXT,sources INTEGER)')
            with closing(sqlite3.connect(state)) as db, db:
                db.executemany('INSERT INTO fresh VALUES (?,?,1)', ((f'site{i:03}.example',f'https://site{i:03}.example/') for i in range(100)))
                # A deterministic random ordering makes the tail win. Merely
                # shuffling the first LIMIT rows would fail this assertion.
                values = iter(range(100,0,-1))
                db.create_function('random', 0, lambda: next(values))
                rows = generator.mixed_candidates(db,catalog,10)
                self.assertEqual({host for host,_,_ in rows}, {f'site{i:03}.example' for i in range(90,100)})

    def test_commoncrawl_is_available_only_as_last_resort(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            state, catalog = root/'state.db',root/'catalog.db'
            generator.initialize(state)
            with closing(sqlite3.connect(catalog)) as db, db:
                db.execute('CREATE TABLE candidates(host TEXT PRIMARY KEY,url TEXT,sources INTEGER)')
                db.execute('INSERT INTO candidates VALUES (?,?,?)', ('fallback.example','https://fallback.example/',bulk.SOURCES['commoncrawl']))
            with closing(sqlite3.connect(state)) as db:
                self.assertEqual(generator.mixed_candidates(db,catalog,10), [('fallback.example','https://fallback.example/','commoncrawl')])

    def test_reverse_providers_rotate_for_uncached_ips_and_fall_back(self):
        with tempfile.TemporaryDirectory() as tmp:
            state = Path(tmp) / "state.db"
            generator.initialize(state)
            csv_response = b"apexDomain,subdomain\nfirst.example,first.example\n"
            json_response = b'{"domain_list":["second.example"]}'
            dns = [[(None, None, None, None, (ip, 0))]
                   for ip in ("1.1.1.1", "1.1.1.1", "8.8.8.8", "9.9.9.9")]
            with closing(sqlite3.connect(state)) as db, patch.object(
                generator.socket, "getaddrinfo", side_effect=dns,
            ), patch.object(generator, "urlopen", side_effect=[
                io.BytesIO(csv_response), io.BytesIO(json_response),
                OSError("provider unavailable"), io.BytesIO(json_response),
            ]) as fetch:
                for i in range(4):
                    self.assertEqual(generator.reverse_seed(db, "seed.example", 1000000 + i), i == 1)
                self.assertEqual([call.args[0].host for call in fetch.call_args_list], [
                    "ip.thc.org", "api.dnsrift.net", "ip.thc.org", "api.dnsrift.net",
                ])
                self.assertEqual(db.execute("SELECT count(*) FROM reverse_lookups").fetchone(), (3,))

    def test_dork_txt_seeds_go_through_reverse_ip_before_scan(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            state, catalog, source = (root / name for name in ("state.db", "catalog.db", "dork-seeds.txt"))
            with closing(sqlite3.connect(state)) as db, db:
                db.execute("CREATE TABLE dork_candidates(host TEXT PRIMARY KEY,url TEXT NOT NULL)")
                db.execute("INSERT INTO dork_candidates VALUES ('seed.example','https://seed.example/')")
            generator.initialize(state)
            source.write_text("https://www.seed.example/post\nseed.example\n")
            signature = generator.collect_dork_seeds(state, source, None)
            self.assertEqual(generator.collect_dork_seeds(state, source, signature), signature)
            with closing(sqlite3.connect(catalog)) as db, db:
                db.execute("CREATE TABLE candidates(host TEXT PRIMARY KEY,url TEXT,sources INTEGER)")
            with closing(sqlite3.connect(state)) as db:
                self.assertEqual(db.execute("SELECT host FROM reverse_seeds").fetchall(), [("seed.example",)])
                self.assertEqual(generator.mixed_candidates(db, catalog, 10), [])
            dns = [(None, None, None, None, ("1.1.1.1", 0))]
            with closing(sqlite3.connect(state)) as db, patch.object(
                generator.socket, "getaddrinfo", return_value=dns,
            ), patch.object(generator, "urlopen", return_value=io.BytesIO(
                b"apexDomain,subdomain\nseed.example,seed.example\nexpanded.example,expanded.example\n",
            )) as fetch:
                self.assertFalse(generator.reverse_seed(db, "seed.example", 1000000))
                self.assertTrue(generator.reverse_seed(db, "seed.example", 1000001))
                fetch.assert_called_once()
                self.assertEqual(generator.mixed_candidates(db, catalog, 10),
                                 [("expanded.example", "https://expanded.example/", "reverse-seed")])
                with db:
                    db.execute("DELETE FROM reverse_seeds")
            generator.initialize(state)
            with closing(sqlite3.connect(state)) as db:
                self.assertEqual(db.execute("SELECT count(*) FROM reverse_seeds").fetchone(), (0,))

    def test_concurrency_trial_balances_profiles_and_stops_at_quota(self):
        with tempfile.TemporaryDirectory() as tmp:
            state = Path(tmp) / "state.db"
            generator.initialize(state)
            args = argparse.Namespace(threads=192, trial_shards=2)
            for batch in ("a", "a", "b", "c", "d", "e"):
                with closing(sqlite3.connect(state)) as db, db:
                    generator.assign_concurrency_trial(db, batch, args)
            with closing(sqlite3.connect(state)) as db:
                self.assertEqual(db.execute("""
                    SELECT threads,automatic,count(*) FROM concurrency_trials
                    GROUP BY threads,automatic ORDER BY threads
                """).fetchall(), [(96, 1, 2), (128, 0, 2)])

    def test_random_txt_inbox_normalizes_deduplicates_and_feeds_shards(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            state, catalog = root / "state.db", root / "catalog.db"
            source = root / "random.txt"
            source.write_text("https://www.one.example/path\none.example\ntwo.example\n127.0.0.1\n")
            generator.initialize(state)
            with closing(sqlite3.connect(catalog)) as db:
                first = bulk.sync_candidate_files(db, [source], "random-txt")
                repeat = bulk.sync_candidate_files(db, [source], "random-txt")
                self.assertEqual((first[0]["status"], repeat[0]["status"]), ("imported", "unchanged"))
            generator.initialize_catalog(catalog)
            with closing(sqlite3.connect(state)) as db:
                rows = generator.mixed_candidates(db, catalog, 10)
            self.assertEqual({(host, method) for host, _, method in rows},
                             {("one.example", "random-txt"), ("two.example", "random-txt")})

    def test_wat_download_starts_next_prefix_before_slow_prefix_finishes(self):
        fourth_started = threading.Event()

        def download(path):
            if path == "slow":
                self.assertTrue(fourth_started.wait(2))
            if path == "fourth":
                fourth_started.set()
            return path

        with patch.object(bulk, "ProcessPoolExecutor", side_effect=lambda max_workers, mp_context: ThreadPoolExecutor(max_workers)), patch.object(
            bulk, "wat_prefix_candidates", side_effect=download,
        ):
            results = {request.result() for request, _ in bulk.wat_downloads(("slow", "fast", "other", "fourth"), None)}
        self.assertEqual(results, {"slow", "fast", "other", "fourth"})

    def test_reverse_ip_accepts_public_ipv4_only_and_reuses_successful_lookup(self):
        with tempfile.TemporaryDirectory() as tmp:
            state = Path(tmp) / "state.db"
            generator.initialize(state)
            lines = ["apexDomain,subdomain", "seed.example,seed.example", "10.0.0.2,10.0.0.2"]
            lines.extend(f"site{i}.example,site{i}.example" for i in range(110))
            lines.extend(["site0.example,www.site0.example", "site0.example,blog.site0.example"])
            dns = [(None, None, None, None, (ip, 0)) for ip in ("127.0.0.1", "1.1.1.1", "1.1.1.1")]
            with closing(sqlite3.connect(state)) as db, patch.object(
                generator.socket, "getaddrinfo", return_value=dns,
            ), patch.object(generator, "urlopen", return_value=io.BytesIO("\n".join(lines).encode())) as fetch:
                self.assertFalse(generator.reverse_seed(db, "seed.example", 1000000))
                self.assertTrue(generator.reverse_seed(db, "second.example", 1000001))
                self.assertEqual(db.execute("SELECT count(*) FROM reverse_candidates").fetchone(), (111,))
                self.assertEqual(db.execute("SELECT ip FROM reverse_lookups").fetchall(), [("1.1.1.1",)])
                fetch.assert_called_once()
                self.assertIn("/download?ip_address=1.1.1.1&limit=50000", fetch.call_args.args[0].full_url)

    def test_retries_yield_to_new_hosts_but_can_fill_an_empty_queue(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            state, catalog = root / "state.db", root / "catalog.db"
            generator.initialize(state)
            with closing(sqlite3.connect(catalog)) as db, db:
                db.execute("CREATE TABLE candidates(host TEXT PRIMARY KEY,url TEXT,sources INTEGER)")
                db.executemany("INSERT INTO candidates VALUES (?,?,?)", (
                    (f"a{i}.example", f"https://a{i}.example/", bulk.SOURCES["archive"])
                    for i in range(100)
                ))
            with closing(sqlite3.connect(state)) as db, db:
                db.executemany("INSERT INTO fresh VALUES (?,?,1)", (
                    (f"c{i}.example", f"https://c{i}.example/") for i in range(100)
                ))
                db.executemany("INSERT INTO retries(host,url,due) VALUES (?,?,0)", (
                    (f"r{i}.example", f"https://r{i}.example/") for i in range(100)
                ))
            with closing(sqlite3.connect(state)) as db:
                mixed = generator.mixed_candidates(db, catalog, 100)
            self.assertEqual(len(mixed), 100)
            self.assertEqual(sum(source == "retry" for _, _, source in mixed), 0)
            with closing(sqlite3.connect(state)) as db, db:
                db.execute("DELETE FROM fresh")
            with closing(sqlite3.connect(catalog)) as db, db:
                db.execute("DELETE FROM candidates")
            with closing(sqlite3.connect(state)) as db:
                self.assertEqual(len(generator.mixed_candidates(db, catalog, 100)), 100)

    def test_dork_rotation_persists_quota_and_avoids_repeat(self):
        with tempfile.TemporaryDirectory() as tmp:
            state = Path(tmp) / "state.db"
            generator.initialize(state)
            now = 86400 * 100
            with patch.object(generator.random, "choice", side_effect=lambda keys: keys[0]):
                with closing(sqlite3.connect(state)) as db:
                    first = generator.next_dork(db, 2, now)
                with closing(sqlite3.connect(state)) as db:
                    self.assertIsNone(generator.next_dork(db, 2, now + 1))
                    second = generator.next_dork(db, 2, now + 43200)
                    self.assertIsNone(generator.next_dork(db, 2, now + 86399))
                    third = generator.next_dork(db, 2, now + 86400)
                    self.assertEqual(db.execute("SELECT count(*) FROM dork_queries").fetchone(), (3,))
            self.assertEqual(len({first, second, third}), 3)

    def test_partial_refill_spreads_across_current_worker_count(self):
        worker = dict(wordpress_bulk=True, enum_checkpoint=True, enum_stream_receipts=True,
                      status="online", current_load=0, max_tasks=8, fresh_wordpress_slots=2)
        candidates = [(f"host-{i}.example", f"https://host-{i}.example/", "ct") for i in range(24)]
        for count in (0, 3, 8, 11):
            with self.subTest(workers=count), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                state = root / "state.db"
                args = argparse.Namespace(root=root, catalog=root / "catalog.db", batch_limit=10)
                generator.initialize(state)
                api = argparse.Namespace(call=lambda path: [worker] * count if path == "/api/workers" else [])
                with patch.object(generator, "mixed_candidates", side_effect=[candidates, []]) as select, \
                     patch.object(generator, "reconcile_batch") as reconcile:
                    generator.dispatch_shards(api, args, state)
                    self.assertEqual(reconcile.call_count, count * 2)
                    if count:
                        self.assertEqual(select.call_args_list[0].args[-1], count * 2 * args.batch_limit)
                    else:
                        select.assert_not_called()
                with closing(sqlite3.connect(state)) as db:
                    sizes = [size for size, in db.execute(
                        "SELECT count(*) FROM shard_hosts GROUP BY batch")]
                    self.assertEqual(len(sizes), count * 2)
                    if sizes:
                        self.assertEqual(sum(sizes), len(candidates))
                        self.assertLessEqual(max(sizes) - min(sizes), 1)

    def test_shards_wait_for_ten_thousand_hosts(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            state = root / "state.db"
            args = argparse.Namespace(root=root, catalog=root / "catalog.db", batch_limit=10_000)
            generator.initialize(state)
            candidates = [(f"host-{i}.example", f"https://host-{i}.example/", "ct") for i in range(19_999)]
            with patch.object(generator, "mixed_candidates", side_effect=[candidates[:9_999], candidates[9_999:]]):
                generator.fill_shards(args, state, 16)
                self.assertEqual(list(root.glob("*.txt")), [])
                generator.initialize(state)
                generator.fill_shards(args, state, 16)
            with closing(sqlite3.connect(state)) as db:
                sizes = db.execute("SELECT count(*) FROM shard_hosts GROUP BY batch ORDER BY 1").fetchall()
                self.assertEqual(sizes, [(10_000,)])
                self.assertEqual(db.execute("SELECT count(*) FROM ready_candidates").fetchone(), (9_999,))

    def test_shard_leases_mix_sources_and_survive_dispatch_restart(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            state, catalog = root / "state.db", root / "catalog.db"
            args = argparse.Namespace(root=root, catalog=catalog, batch_limit=10, once=False)
            generator.initialize(state)
            with closing(sqlite3.connect(catalog)) as db, db:
                db.execute("CREATE TABLE candidates(host TEXT PRIMARY KEY,url TEXT,sources INTEGER)")
                db.executemany("INSERT INTO candidates VALUES (?,?,?)", (
                    (f"a{i}.example", f"https://a{i}.example/", bulk.SOURCES["archive"])
                    for i in range(100)
                ))
            with closing(sqlite3.connect(state)) as db, db:
                db.executemany("INSERT INTO fresh VALUES (?,?,1)", (
                    (f"c{i}.example", f"https://c{i}.example/") for i in range(100)
                ))
            worker = dict(wordpress_bulk=True, enum_checkpoint=True, enum_stream_receipts=True,
                          status="online", current_load=0)
            jobs = [{"id": "ordinary", "status": "queued"}]

            def call(path):
                return [worker] * 8 if path == "/api/workers" else jobs

            api = argparse.Namespace(call=call)
            with patch.object(generator, "reconcile_batch") as reconcile:
                reconcile.return_value = None
                generator.dispatch_shards(api, args, state)
                reconcile.assert_not_called()
                jobs.clear()
                generator.dispatch_shards(api, args, state)
                first = [entry.args[-1] for entry in reconcile.call_args_list]
                self.assertEqual(len(set(first)), 8)
                reconcile.reset_mock()
                generator.dispatch_shards(api, args, state)
                self.assertEqual({entry.args[-1] for entry in reconcile.call_args_list}, set(first))
            with closing(sqlite3.connect(state)) as db:
                self.assertEqual(db.execute("SELECT count(*) FROM shard_leases WHERE launched=0").fetchone(), (8,))
                self.assertEqual(db.execute("SELECT count(*) FROM shard_hosts").fetchone(), (160,))
                for batch in first:
                    counts = dict(db.execute("SELECT source,count(*) FROM shard_hosts WHERE batch=? GROUP BY source", (batch,)))
                    self.assertEqual(sum(counts.values()), 10)
                    # Both CT and archive remain available when reverse IP is empty in a non-pooled canary.
                    self.assertTrue(set(counts) <= {"ct", "archive"})
                self.assertGreater(db.execute("SELECT count(*) FROM shard_hosts WHERE source='ct'").fetchone()[0],0)

    def test_wat_wordpress_rest_link_without_generator_or_assets(self):
        def record(host, headers):
            return json.dumps({"Envelope": {
                "WARC-Header-Metadata": {
                    "WARC-Type": "response", "WARC-Target-URI": f"https://{host}/",
                },
                "Payload-Metadata": {"HTTP-Response-Metadata": {"Headers": headers}},
            }})

        lines = [
            record("first.example", {"Link": '<https://first.example/wp-json/>; rel="https://api.w.org/"'}),
            record("second.example", {"Link:1": '</feed>; rel=alternate, </?rest_route=/>; rel="https://api.w.org/"'}),
            record("mention.example", {"Link": '</feed>; rel=alternate; title="https://api.w.org/"'}),
            record("other.example", {"Location": '</wp-json/>; rel="https://api.w.org/"'}),
        ]
        self.assertEqual(list(bulk.input_urls(lines, "cc-wat", "url")),
                         ["https://first.example/", "https://second.example/"])

    def test_wat_prefix_keeps_complete_records_before_a_partial_gzip_header(self):
        record = {"Envelope": {
            "WARC-Header-Metadata": {"WARC-Type": "response", "WARC-Target-URI": "https://site.example/"},
            "Payload-Metadata": {"HTTP-Response-Metadata": {"HTML-Metadata": {
                "Head": {"Metas": [{"name": "generator", "content": "WordPress 6.9"}]},
            }}},
        }}
        data = gzip.compress((json.dumps(record) + "\n").encode()) + b"\x1f"
        response = io.BytesIO(data)
        response.status = 206
        response.headers = {"Content-Range": f"bytes 0-{len(data) - 1}/{len(data) + 100}"}
        with patch.object(bulk, "WAT_PREFIX_BYTES", len(data)), patch.object(bulk, "urlopen", return_value=response):
            self.assertEqual(bulk.wat_prefix_candidates("fixture"), ("https://site.example/",))

    @patch.object(bulk, "ProcessPoolExecutor", new=lambda max_workers, mp_context: ThreadPoolExecutor(max_workers))
    def test_wat_refresh_resumes_previous_release_when_index_is_unavailable(self):
        base = "crawl-data/CC-MAIN-2026-43/segments/100.1/wat/CC-MAIN-"
        done, pending = (base + str(i) + ".warc.wat.gz" for i in range(2))
        listing = io.BytesIO(gzip.compress(f"{done}\n{pending}\n".encode()))
        with closing(sqlite3.connect(":memory:")) as db, patch.object(
            bulk, "urlopen", side_effect=[OSError("index offline"), listing],
        ), patch.object(bulk, "wat_prefix_candidates", return_value=["https://site.example"]) as fetch:
            db.execute("CREATE TABLE commoncrawl_wat_prefixes(path TEXT PRIMARY KEY)")
            db.execute("INSERT INTO commoncrawl_wat_prefixes VALUES (?)", (done,))
            db.commit()
            result = bulk.pull_commoncrawl_wordpress(db, 1)
            self.assertEqual(result["crawl_source"], "previous_receipt")
            self.assertEqual(result["prefixes"], 1)
            self.assertEqual(result["new_unattempted_hosts"], 1)
            self.assertEqual(db.execute("SELECT host FROM archive_ready").fetchall(), [("site.example",)])
            bulk.insert_attempted(db, ["site.example"])
            self.assertEqual(db.execute("SELECT host FROM archive_ready").fetchall(), [])
            fetch.assert_called_once_with(pending)

    def test_wat_assets_must_point_to_a_wordpress_path_on_the_same_host(self):
        page = "https://site.example/article"
        self.assertTrue(bulk.wat_wordpress_assets(page, {"Scripts": [{"url": "/wp-includes/js/core.js"}]}))
        self.assertTrue(bulk.wat_wordpress_assets(page, {"Link": [{"url": "//www.site.example/wp-content/themes/a.css"}]}))
        self.assertFalse(bulk.wat_wordpress_assets(page, {"Scripts": [{"url": "https://other.example/wp-includes/a.js"}]}))
        self.assertFalse(bulk.wat_wordpress_assets(page, {"Link": [{"url": "/asset?path=/wp-includes/a.js"}]}))

    @patch.object(bulk, "ProcessPoolExecutor", new=lambda max_workers, mp_context: ThreadPoolExecutor(max_workers))
    def test_wat_refresh_keeps_going_after_one_failed_prefix(self):
        crawl = "CC-MAIN-2026-43"
        shards = [f"crawl-data/{crawl}/segments/100.1/wat/CC-MAIN-{i}.warc.wat.gz" for i in range(3)]
        def download(shard):
            if shard == shards[0]:
                raise OSError("retry later")
            return ["https://site.example"]
        with closing(sqlite3.connect(":memory:")) as db, patch.object(
            bulk, "urlopen", side_effect=[io.BytesIO(json.dumps([{"id": crawl}]).encode()),
                                         io.BytesIO(gzip.compress("\n".join(shards).encode()))],
        ), patch.object(bulk, "wat_prefix_candidates", side_effect=download) as fetch:
            result = bulk.pull_commoncrawl_wordpress(db, 2)
            self.assertEqual(result["prefixes"], 1)
            self.assertEqual(result["failures"][0]["shard"], shards[0])
            self.assertEqual(fetch.call_count, 2)
            self.assertEqual(db.execute("SELECT path FROM commoncrawl_wat_prefixes").fetchall(), [(shards[1],)])

    def test_fresh_feed_has_reserved_capacity_with_archive_backlog(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            state, catalog, seed = (root / name for name in ("state.db", "catalog.db", "seed.txt"))
            generator.initialize(state)
            seed.write_text("archive-a.example\narchive-b.example\narchive-c.example\narchive-d.example\n")
            with closing(sqlite3.connect(catalog)) as db, db:
                bulk.import_urls(db, seed, "archive", "txt")
            with closing(sqlite3.connect(state)) as db, db:
                db.executemany("INSERT INTO fresh VALUES (?,?,?)", [
                    ("old.example", "https://old.example/", 1),
                    ("new.example", "https://new.example/", 2),
                ])
                batch = generator.candidates(db, catalog, 4)
            self.assertEqual(len(batch), 4)
            self.assertEqual(batch[0], ("https://new.example/",))
            self.assertTrue(all(url.startswith("https://archive-") for url, in batch[1:]))

    def test_certificate_promotes_a_generic_catalog_host(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            state, catalog, seed = (root / name for name in ("state.db", "catalog.db", "seed.txt"))
            generator.initialize(state)
            with closing(sqlite3.connect(catalog)) as db, db:
                seed.write_text("generic.example\n")
                bulk.import_urls(db, seed, "webgraph", "txt")
                seed.write_text("archive.example\n")
                bulk.import_urls(db, seed, "archive", "txt")
            log = ("ct-log", "https://example.org/", False)
            with patch.object(ct, "head", return_value=1):
                generator.poll_log(state, log, catalog)
            with patch.object(ct, "head", return_value=2), patch.object(
                ct, "dynamic_entries", return_value=iter([["generic.example", "archive.example"]]),
            ):
                generator.poll_log(state, log, catalog)
            with closing(sqlite3.connect(state)) as db:
                self.assertEqual(generator.candidates(db, catalog, 4),
                                 [("https://generic.example/",), ("https://archive.example/",)])

    def test_wat_refresh_skips_only_while_archive_backlog_is_available(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            state, catalog, seed = (root / name for name in ("generator-state.db", "catalog.db", "seed.txt"))
            generator.initialize(state)
            seed.write_text("ready.example\n")
            with closing(sqlite3.connect(catalog)) as db, db:
                bulk.import_urls(db, seed, "archive", "txt")
            with closing(sqlite3.connect(catalog)) as db, patch.object(bulk, "urlopen") as fetch:
                self.assertEqual(bulk.pull_commoncrawl_wordpress(db, 1, root / "paused", backlog_limit=1),
                                 {"status": "backlog_ready", "pending_at_least": 1})
                fetch.assert_not_called()
                with closing(sqlite3.connect(state)) as queue, queue:
                    queue.execute("INSERT INTO deferred VALUES ('ready.example',unixepoch()+3600)")
                fetch.side_effect = OSError("test fetch")
                with self.assertRaisesRegex(OSError, "test fetch"):
                    bulk.pull_commoncrawl_wordpress(db, 1, root / "paused", backlog_limit=1)

    def test_wat_candidates_require_a_wordpress_generator_on_a_response(self):
        def record(kind, name, content):
            return json.dumps({"Envelope": {
                "WARC-Header-Metadata": {"WARC-Type": kind, "WARC-Target-URI": "https://site.example/article"},
                "Payload-Metadata": {"HTTP-Response-Metadata": {"HTML-Metadata": {
                    "Head": {"Metas": [{"name": name, "content": content}]},
                }}},
            }})
        lines = [record("response", "generator", "WordPress 6.9"),
                 record("request", "generator", "WordPress 6.9"),
                 record("response", "description", "WordPress 6.9"),
                 record("response", "generator", "WordPressish")]
        self.assertEqual(list(bulk.input_urls(lines, "cc-wat", "url")), ["https://site.example/article"])

    def test_catalog_reader_sees_committed_candidates_during_import(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            state, catalog, seed = (root / name for name in ("state.db", "catalog.db", "seed.txt"))
            generator.initialize(state)
            seed.write_text("ready.example\n")
            with closing(sqlite3.connect(catalog)) as db, db:
                bulk.import_urls(db, seed, "ranked", "txt")
            generator.initialize_catalog(catalog)
            seed.write_text("pending.example\n")
            with closing(sqlite3.connect(catalog)) as writer, writer:
                bulk.import_urls(writer, seed, "ranked", "txt")
                with closing(sqlite3.connect(state)) as reader:
                    self.assertEqual(generator.candidates(reader, catalog, 10), [("https://ready.example/",)])
            with closing(sqlite3.connect(state)) as reader:
                self.assertEqual(len(generator.candidates(reader, catalog, 10)), 2)

    def test_partial_results_require_membership_in_controller_input(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            state, catalog, batch, snapshot, output = (root / name for name in
                                                        ("state.db", "catalog.db", "batch", "input", "output"))
            generator.initialize(state)
            args = argparse.Namespace(catalog=catalog, merged=root)
            batch.write_text("site.example\nrejected.example\n")
            snapshot.write_text("site.example\n")
            output.write_text("https://site.example/\nhttps://rejected.example/\n")
            with self.assertRaises(ValueError):
                generator.recover_partial_results(args, state, batch, snapshot, output)
            self.assertFalse((root / "wordpress.txt").exists())
            output.write_text("https://site.example/\n")
            self.assertEqual(generator.recover_partial_results(args, state, batch, snapshot, output), {"site.example"})
            self.assertEqual((root / "wordpress.txt").read_text(), "https://site.example\n")

    def test_snapshot_import_releases_catalog_writer_between_batches(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            catalog, source = root / "catalog.db", root / "snapshot.txt"
            source.touch()
            with closing(sqlite3.connect(catalog)) as importer, closing(sqlite3.connect(catalog, timeout=0)) as scanner:
                def rows(*_):
                    yield "first.example"
                    yield "second.example"
                    with scanner:
                        scanner.execute("BEGIN IMMEDIATE")
                        self.assertEqual(scanner.execute("SELECT count(*) FROM candidates").fetchone(), (2,))
                    yield "third.example"

                with patch.object(bulk, "input_urls", side_effect=rows):
                    bulk.import_urls(importer, source, "webgraph", "txt", commit_batch_size=2)
                self.assertEqual(scanner.execute("SELECT count(*) FROM candidates").fetchone(), (3,))

    def test_missing_historical_detections_are_selected_once_for_live_recheck(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            state, catalog, seed = (root / name for name in ("state.db", "catalog.db", "seed.txt"))
            generator.initialize(state)
            seed.write_text("already-exported.example\nalready-rechecked.example\nmissing.example\n")
            with closing(sqlite3.connect(catalog)) as db, db:
                bulk.import_urls(db, seed, "verified", "txt")
                bulk.record_attempted(db, [seed])
            with closing(sqlite3.connect(state)) as db, db:
                db.execute("INSERT INTO merged_hosts VALUES ('already-exported.example','https://already-exported.example/')")
                db.execute("INSERT INTO generator_checked VALUES ('already-rechecked.example')")
                self.assertEqual(generator.candidates(db, catalog, 10), [("https://missing.example/",)])

    def test_archive_then_ranked_candidates_outrank_generic_hosts(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            state, catalog, seed = (root / name for name in ("state.db", "catalog.db", "seed.txt"))
            generator.initialize(state)
            with closing(sqlite3.connect(catalog)) as db, db:
                for source, host in (("webgraph", "generic.example"), ("ranked", "popular.example"),
                                     ("archive", "known-wordpress.example")):
                    seed.write_text(host + "\n")
                    bulk.import_urls(db, seed, source, "txt")
            generator.initialize_catalog(catalog)
            with closing(sqlite3.connect(state)) as db:
                self.assertEqual(generator.candidates(db, catalog, 10), [("https://known-wordpress.example/",)])
            with closing(sqlite3.connect(catalog)) as db, db:
                bulk.insert_attempted(db, ["known-wordpress.example"])
            with closing(sqlite3.connect(state)) as db:
                self.assertEqual(generator.candidates(db, catalog, 10), [("https://popular.example/",)])

    def test_merged_exports_only_canonical_https_hostnames(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            state, source = root / "state.db", root / "batch.txt"
            generator.initialize(state)
            source.write_text("http://www.site.example:80/path?a=b#c\nhttps://site.example/other\n")
            generator.publish_merged(state, root, [source])
            self.assertEqual(len((root / "wordpress.txt").read_text().splitlines()), 1)
            self.assertRegex((root / "wordpress.txt").read_text(), r"^https://(?:www\.)?site\.example\n$")

    def test_merged_rebuilds_after_interrupted_publish_and_imports_external_additions(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            state, source = root / "state.db", root / "batch.txt"
            target = root / "wordpress.txt"
            generator.initialize(state)
            source.write_text("https://first.example\n")
            generator.publish_merged(state, root, [source])
            before = target.stat()
            self.assertEqual(generator.publish_merged(state, root, [source]), 0)
            self.assertEqual(target.stat().st_ino, before.st_ino)
            source.write_text("https://second.example\n")
            with patch.object(Path, "replace", side_effect=OSError("interrupted before rename")):
                with self.assertRaises(OSError):
                    generator.publish_merged(state, root, [source])
            generator.publish_merged(state, root, [source])
            with target.open("a") as stream:
                stream.write("https://external.example\n")
            generator.publish_merged(state, root, [])
            self.assertEqual(set(target.read_text().splitlines()), {
                "https://first.example", "https://second.example", "https://external.example",
            })

    def test_completed_job_keeps_results_when_other_shard_reports_all_errors(self):
        with tempfile.TemporaryDirectory() as tmp:
            state = Path(tmp) / "state.json"
            job = {"id": "job", "name": "Tool: wordpress-bulk", "status": "completed_with_errors", "total_tasks": 2}
            tasks = [
                {"id": "1", "job_id": "job", "status": "completed",
                 "result": {"exit_code": 0, "stderr": "[*] homepage outcomes verified=1\n[*] targets=1 errors=0"}},
                {"id": "2", "job_id": "job", "status": "failed",
                 "result": {"exit_code": 1, "stderr": "[*] homepage outcomes dns=2\n[*] targets=2 errors=2"}},
            ]
            state.write_text(json.dumps({"job": job, "tasks": tasks}))
            self.assertEqual(bulk.completed_job_counts(state), (3, 1))
            tasks[1]["result"]["stderr"] = "process interrupted"
            state.write_text(json.dumps({"job": job, "tasks": tasks}))
            with self.assertRaises(ValueError):
                bulk.completed_job_counts(state)

    def test_all_error_shard_only_defers_if_input_was_not_fully_checked(self):
        for checked in (1, 2):
            with self.subTest(checked=checked), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                state = root / "state.db"
                args = argparse.Namespace(root=root, data=root, merged=root, catalog=root / "catalog.db")
                generator.initialize(state)
                ident = "enum_fresh_batch"
                directory = root / "enum-jobs" / ident
                directory.mkdir(parents=True)
                inputs = "https://a.example/\nhttps://b.example/\n"
                (root / "batch.txt").write_text(inputs)
                (directory / "input.txt").write_text(inputs)
                (directory / "wordpress-retry.txt").write_text("https://b.example/\n")
                job = dict(id=ident, name="Tool: wordpress-bulk", status="failed", total_tasks=1,
                           output_file=f"wordpress-fresh-{ident}.txt")
                task = dict(id="task", job_id=ident, status="failed",
                            result=dict(exit_code=1, stderr=f"[*] homepage outcomes timeout={checked}\n"
                                                             f"[*] targets={checked} errors={checked}"))
                api = argparse.Namespace(call=lambda path: [task] if path.endswith("/tasks") else job)
                self.assertEqual(bool(generator.reconcile_batch(api, args, state, "batch")), checked == 2)
                with closing(sqlite3.connect(state)) as db:
                    self.assertEqual(db.execute("SELECT count(*) FROM deferred").fetchone(),
                                     (2 if checked == 1 else 0,))
                    self.assertEqual(db.execute("SELECT host FROM retries").fetchall(),
                                     [] if checked == 1 else [("b.example",)])

    def test_standalone_runner_results_are_candidates_and_resume_at_complete_rows(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            state = root / "state.db"
            catalog = root / "catalog.db"
            results = root / "wordpress_discoveries_1m.jsonl"
            seed = root / "seed.txt"
            seed.write_text("backlog.example\n")
            with closing(sqlite3.connect(catalog)) as db, db:
                bulk.import_urls(db, seed, "webgraph", "txt")
            generator.initialize(state)
            results.write_text(
                '{"url":"https://wp.example:443/wp-login.php"}\n'
                '{"url":"https://wp.example/wp-admin/"}\n'
                '{"url":"http://192.168.1.1/"}\n'
                '{"url":"https://next.example/"}'
            )
            generator.collect_discovery_results(state, results)
            with closing(sqlite3.connect(state)) as db:
                self.assertEqual(generator.candidates(db, catalog, 100), [("https://wp.example/",)])
            self.assertFalse((root / "wordpress.txt").exists())
            with results.open("a") as stream:
                stream.write("\n")
            generator.collect_discovery_results(state, results)
            with closing(sqlite3.connect(state)) as db:
                self.assertEqual(generator.candidates(db, catalog, 100),
                                 [("https://next.example/",), ("https://wp.example/",)])

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

    def test_ct_rate_limit_cools_only_the_affected_origin(self):
        poller = generator.CTPoller()
        first = ("first", "https://limited.example/one/", False)
        sibling = ("sibling", "https://limited.example/two/", True)
        other = ("other", "https://available.example/", False)
        response = HTTPError(first[1], 429, "limited", {"Retry-After": "120"}, io.BytesIO())
        with patch.object(generator, "poll_log", side_effect=[response, None, None]) as poll, patch.object(
            generator.time, "monotonic", side_effect=[10, 10, 129, 129, 130],
        ):
            poller.poll(None, first, None)
            poller.poll(None, sibling, None)
            poller.poll(None, other, None)
            poller.poll(None, sibling, None)
        self.assertEqual([call.args[1] for call in poll.call_args_list], [first, other, sibling])

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
            self.assertFalse((root / "wordpress.txt").exists())
            self.assertEqual(next(root.glob("wp-*.txt")).read_text(), "https://site.example\n")
            self.assertFalse((root / api.job["output_file"]).exists())
            self.assertTrue((root / "archive" / api.job["output_file"]).is_file())
            with closing(sqlite3.connect(args.catalog)) as db:
                self.assertEqual(db.execute("SELECT host FROM attempted").fetchall(), [("site.example",)])
