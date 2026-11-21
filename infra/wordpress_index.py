"""Read only hostname columns from public Common Crawl URL Index Parquet files."""
import argparse
import concurrent.futures
import fcntl
import gzip
import importlib
import json
import logging
import random
import re
import sqlite3
import sys
import threading
import time
from contextlib import closing
from pathlib import Path
from urllib.request import urlopen

import wordpress_bulk as bulk
import wordpress_sources as sources

LOG=logging.getLogger('wordpress-generator')
PART=re.compile(r'cc-index/table/cc-main/warc/crawl=(CC-MAIN-\d{4}-\d{2})/subset=warc/[A-Za-z0-9_.-]+\.parquet')
BATCH=25000


def active(root):
    # OFF pauses scanners, not the public dataset reader. Capacity is checked separately.
    return sources.read(root)['commoncrawl'] and sources.mode(root)=='index'


def schema(state):
    with closing(sqlite3.connect(state,timeout=30)) as db,db:
        db.execute('''CREATE TABLE IF NOT EXISTS cc_index_parts(
            path TEXT PRIMARY KEY,crawl TEXT NOT NULL,position INTEGER NOT NULL DEFAULT 0,
            done INTEGER NOT NULL DEFAULT 0,claimed INTEGER NOT NULL DEFAULT 0,
            queued INTEGER NOT NULL DEFAULT 0,updated INTEGER NOT NULL,last_error TEXT)''')


def hostname_batches(root,path,position):
    sys.path.insert(0,str(root/'index-lib'))
    duckdb=importlib.import_module('duckdb')
    with duckdb.connect(config={'extension_directory':str(root/'index-extensions'),'temp_directory':str(root/'index-scratch'),'memory_limit':'256MB','threads':'2'}) as connection:
        connection.execute('LOAD httpfs')
        result=connection.execute('''SELECT DISTINCT url_host_name FROM read_parquet(?)
            WHERE url_host_name IS NOT NULL ORDER BY url_host_name OFFSET ?''',
            ['https://data.commoncrawl.org/'+path,position])
        while rows:=result.fetchmany(BATCH):yield [row[0] for row in rows]


def stage_hosts(root,state,catalog,path,hosts):
    """No website requests: normalize and deduplicate against existing work once."""
    with closing(sqlite3.connect(state,timeout=30)) as db,db:
        db.execute('BEGIN IMMEDIATE')
        room,refilling=db.execute('SELECT target-available,refilling FROM pool_policy WHERE slot=1').fetchone()
        if room<=0 or not refilling or not active(root):return 0,0
        selected=hosts[:room]
        candidates={h:u for value in selected if isinstance(value,str) and (item:=bulk.homepage(value)) for h,u in [item]}
        db.execute('CREATE TEMP TABLE index_incoming(host TEXT PRIMARY KEY,url TEXT NOT NULL) WITHOUT ROWID')
        db.executemany('INSERT OR IGNORE INTO index_incoming VALUES (?,?)',sorted(candidates.items()))
        db.execute('''DELETE FROM index_incoming AS c WHERE
            EXISTS(SELECT 1 FROM generator_checked g WHERE g.host=c.host)
            OR EXISTS(SELECT 1 FROM merged_hosts m WHERE m.host=c.host)
            OR EXISTS(SELECT 1 FROM ready_candidates r WHERE r.host=c.host)
            OR EXISTS(SELECT 1 FROM shard_hosts h WHERE h.host=c.host)
            OR EXISTS(SELECT 1 FROM deferred d WHERE d.host=c.host AND d.until>unixepoch())''')
        incoming=db.execute('SELECT host,url FROM index_incoming ORDER BY host').fetchall()
        added=db.execute("INSERT OR IGNORE INTO ready_candidates SELECT host,url,'commoncrawl-index' FROM index_incoming").rowcount
        if sources.read(root)['reverse_ip']:
            db.execute("INSERT OR IGNORE INTO reverse_input_seeds SELECT host,'commoncrawl',unixepoch() FROM index_incoming")
        db.execute('UPDATE pool_policy SET started=max(started,available>=10000),refilling=(available<target) WHERE slot=1')
        db.execute('UPDATE cc_index_parts SET position=position+?,queued=queued+?,updated=?,last_error=NULL WHERE path=?',(len(selected),added,int(time.time()),path))
    # Ready tasks are already available while canonical provenance is persisted.
    if incoming and catalog is not None:
        with closing(sqlite3.connect(catalog,timeout=30)) as cat,cat:
            cat.executemany('''INSERT INTO candidates(host,url,sources) VALUES (?,?,32)
                ON CONFLICT(host) DO UPDATE SET sources=candidates.sources|32 WHERE (candidates.sources&32)=0''',incoming)
            if cat.execute("SELECT 1 FROM sqlite_schema WHERE name='fresh_catalog_queue'").fetchone():
                cat.executemany('DELETE FROM fresh_catalog_queue WHERE host=?',((h,) for h,_ in incoming))
    return len(selected),added


class Reader:
    def __init__(self,root,state,catalog,event=None):
        self.root,self.state,self.catalog,self.event=root,state,catalog,event
        self.lock=threading.Lock();self.collections=[];self.loaded=0;self.manifests={}

    def claim(self):
        with self.lock:
            if time.monotonic()-self.loaded>3600 or not self.collections:
                records=sources.collection_records(self.root,fetch=urlopen)
                self.collections=sources.recent_crawls(records);self.loaded=time.monotonic()
            choices=sources.recent_crawls(self.collections);random.shuffle(choices)
            for crawl in choices:
                ident=crawl['id']
                if not re.fullmatch(r'CC-MAIN-\d{4}-\d{2}',ident):continue
                if ident not in self.manifests:
                    with urlopen(bulk.COMMONCRAWL_DATA+f'crawl-data/{ident}/cc-index-table.paths.gz',timeout=30) as response:
                        data=response.read((2<<20)+1)
                    if len(data)>2<<20:raise ValueError('index manifest too large')
                    with gzip.GzipFile(fileobj=__import__('io').BytesIO(data)) as stream:raw=stream.read((4<<20)+1)
                    if len(raw)>4<<20:raise ValueError('expanded index manifest too large')
                    paths=[p for p in raw.decode('ascii').splitlines() if (match:=PART.fullmatch(p)) and match[1]==ident]
                    random.shuffle(paths);self.manifests[ident]=paths
                with closing(sqlite3.connect(self.state,timeout=30)) as db,db:
                    db.execute('BEGIN IMMEDIATE')
                    for path in self.manifests[ident]:
                        row=db.execute('SELECT position,done,claimed FROM cc_index_parts WHERE path=?',(path,)).fetchone()
                        if row and (row[1] or row[2]):continue
                        db.execute('INSERT OR IGNORE INTO cc_index_parts(path,crawl,updated) VALUES (?,?,?)',(path,ident,int(time.time())))
                        db.execute('UPDATE cc_index_parts SET claimed=1,updated=? WHERE path=?',(int(time.time()),path))
                        return path,row[0] if row else 0
            return None

    def status(self):
        with self.lock,closing(sqlite3.connect(self.state,timeout=30)) as db:
            done,busy,read,queued=db.execute('SELECT coalesce(sum(done),0),coalesce(sum(claimed),0),coalesce(sum(position),0),coalesce(sum(queued),0) FROM cc_index_parts').fetchone()
            crawls=[row[0] for row in db.execute('SELECT DISTINCT crawl FROM cc_index_parts WHERE claimed=1')]
            error=db.execute('SELECT last_error FROM cc_index_parts WHERE last_error IS NOT NULL ORDER BY updated DESC LIMIT 1').fetchone()
            value={'mode':'index','max_age_years':2,'parts_done':done,'active_parts':busy,'hostnames_read':read,'unique_queued':queued,'active_crawls':crawls,'last_error':error[0] if error else None,'updated_at':int(time.time())}
            temp=self.root/'.index-status.tmp';temp.write_text(json.dumps(value));temp.replace(self.root/'index-status.json')

    def process(self,path,position):
        exhausted=True
        try:
            for hosts in hostname_batches(self.root,path,position):
                consumed,added=stage_hosts(self.root,self.state,self.catalog,path,hosts)
                if added and self.event:self.event.set()
                self.status()
                if consumed<len(hosts):exhausted=False;break
        except Exception as error:
            exhausted=False
            with closing(sqlite3.connect(self.state,timeout=30)) as db,db:
                db.execute('UPDATE cc_index_parts SET last_error=?,updated=? WHERE path=?',(type(error).__name__+': '+str(error)[:160],int(time.time()),path))
            LOG.warning('Common Crawl index part deferred (%s)',type(error).__name__)
            raise
        finally:
            with closing(sqlite3.connect(self.state,timeout=30)) as db,db:
                db.execute('UPDATE cc_index_parts SET claimed=0,done=max(done,?),updated=? WHERE path=?',(int(exhausted),int(time.time()),path))
            self.status()

    def loop(self):
        while True:
            try:
                with closing(sqlite3.connect(self.state,timeout=30)) as db:refilling=db.execute('SELECT refilling FROM pool_policy WHERE slot=1').fetchone()[0]
                if not active(self.root) or not refilling:time.sleep(1);continue
                part=self.claim()
                if part:self.process(*part)
                else:time.sleep(30)
            except Exception as error:
                LOG.warning('Common Crawl index reader deferred (%s): %s',type(error).__name__,str(error));time.sleep(10)


def run(args,state):
    schema(state)
    lock=(args.root/'index-reader.lock').open('a')
    try:fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
    except BlockingIOError:lock.close();return
    with lock:
        with closing(sqlite3.connect(state,timeout=30)) as db,db:db.execute('UPDATE cc_index_parts SET claimed=0 WHERE done=0')
        reader=Reader(args.root,state,args.catalog,getattr(args,'shard_ready_event',None))
        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
            for future in [pool.submit(reader.loop) for _ in range(2)]:future.result()


if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--root',type=Path,required=True);parser.add_argument('--once',action='store_true')
    args=parser.parse_args();args.catalog=args.root/'candidates.db';state=args.root/'generator-state.db';schema(state)
    reader=Reader(args.root,state,args.catalog)
    if args.once:
        with (args.root/'index-reader.lock').open('a') as lock:
            fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
            path,position=reader.claim();reader.process(path,position);print((args.root/'index-status.json').read_text())
    else:run(args,state)
