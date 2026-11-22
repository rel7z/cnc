"""Collect archived WordPress candidates on CNC workers; verify them separately."""
import argparse
import base64
import gzip
import io
import json
import logging
import re
import random
import sqlite3
import socket
import sys
import time
import uuid
from contextlib import closing
from pathlib import Path
from tempfile import TemporaryDirectory
from urllib.request import urlopen
from urllib.error import HTTPError, URLError

import wordpress_bulk as bulk
import wordpress_sources as source_policy

LOG = logging.getLogger('wordpress-generator')
PREFIX = re.compile(r'crawl-data/CC-MAIN-\d{4}-\d{2}/segments/[0-9.]+/wat/[A-Z0-9-]+\.warc\.wat\.gz')
WORKER_SCRIPT = '/opt/cnc/tools/wordpress-restock/wordpress_restock.py'
PER_WORKER = 2


def worker(source):
    return collect(source.read_text().splitlines())


def wave(encoded,config=Path('/opt/cnc/config.json')):
    if len(encoded)>100000:
        raise ValueError('oversized restock assignment')
    assignments=json.loads(base64.b64decode(encoded,validate=True))
    if not isinstance(assignments,dict) or not all(
        isinstance(ident,str) and isinstance(paths,list) and 0<len(paths)<=PER_WORKER
        and all(isinstance(path,str) and PREFIX.fullmatch(path) for path in paths)
        for ident,paths in assignments.items()
    ):
        raise ValueError('invalid restock assignments')
    ident=json.loads(config.read_text())['worker_id']
    return collect(assignments[ident]) if ident in assignments else 0


def wave_workers(raw):
    assignments=json.loads(raw)
    return len(assignments) if isinstance(assignments,dict) else (len(assignments)+PER_WORKER-1)//PER_WORKER


def fetch_prefix(path):
    for attempt in range(3):
        try:
            return bulk.wat_prefix_candidates(path,all_hosts=True)
        except URLError as error:
            # Short retries only for the resolver's explicitly temporary error.
            # Provider rate limits retain the per-source cooldown instead.
            if isinstance(error,HTTPError) or not isinstance(error.reason,socket.gaierror) or error.reason.errno!=socket.EAI_AGAIN or attempt==2:
                raise
            time.sleep(1+attempt*2)


def collect(paths):
    if not paths or len(paths) > PER_WORKER or not all(PREFIX.fullmatch(path) for path in paths):
        raise ValueError('invalid restock task paths')
    failed = False
    for path in paths:
        try:
            urls = fetch_prefix(path)
            print(json.dumps({'path': path, 'urls': urls}, separators=(',', ':')), flush=True)
        except (OSError, ValueError, EOFError) as error:
            print(f'Restock prefix failed: {path}: {type(error).__name__}: {error}', file=sys.stderr, flush=True)
            failed = True
    return int(failed)


def listing(catalog=None):
    try:
        available=source_policy.recent_crawls(source_policy.collection_records(Path(catalog).parent if catalog else None,fetch=urlopen))
        if not available:raise ValueError('no crawl in the last two years')
        crawl=random.choice(available)['id']
    except (OSError,ValueError,IndexError,KeyError,TypeError):
        # The release index can fail while immutable archive downloads work.
        # Resume a release already proven by the controller's import receipts.
        if catalog is None:
            raise
        if (Path(catalog).parent/'commoncrawl-mode.json').exists():raise ValueError('Crawl dates could not be verified')
        with closing(sqlite3.connect(catalog, timeout=30)) as db:
            row=db.execute('SELECT max(substr(path,12,15)) FROM commoncrawl_wat_prefixes').fetchone()
        if not row or not row[0]:
            raise ValueError('no previous restock release receipt')
        crawl=row[0]
        from datetime import datetime,timezone,timedelta
        first=datetime.strptime(crawl+'-1','CC-MAIN-%G-%V-%u').replace(tzinfo=timezone.utc)
        now=datetime.now(timezone.utc)
        if first<now.replace(year=now.year-2):raise ValueError('previous crawl is older than two years')
    if not isinstance(crawl, str) or not re.fullmatch(r'CC-MAIN-\d{4}-\d{2}', crawl):
        raise ValueError('invalid restock release')
    with urlopen(bulk.COMMONCRAWL_DATA + f'crawl-data/{crawl}/wat.paths.gz', timeout=30) as response:
        data = response.read((4 << 20) + 1)
    if len(data) > 4 << 20:
        raise ValueError('oversized restock listing')
    with gzip.GzipFile(fileobj=io.BytesIO(data)) as stream:
        raw = stream.read((32 << 20) + 1)
    if len(raw) > 32 << 20:
        raise ValueError('oversized expanded restock listing')
    paths = raw.decode('ascii').splitlines()
    if not all(PREFIX.fullmatch(path) for path in paths):
        raise ValueError('invalid restock listing paths')
    # Randomize file selection within the latest release. Persistent receipts
    # still exclude every completed prefix before a wave is prepared.
    paths=sorted(set(paths))
    random.shuffle(paths)
    return paths


def schema(state):
    with closing(sqlite3.connect(state, timeout=30)) as db, db:
        db.execute('''CREATE TABLE IF NOT EXISTS worker_restock_jobs (
            batch TEXT PRIMARY KEY, job_id TEXT, paths TEXT NOT NULL,
            phase TEXT NOT NULL DEFAULT 'prepared', created INTEGER NOT NULL,
            hosts INTEGER NOT NULL DEFAULT 0)''')


def import_results(catalog, batch, paths, tasks, state=None):
    records = {}
    for task in tasks:
        # Results include bounded complete JSON lines, never live stream fragments.
        result = task.get('result') or {}
        for line in (result.get('stdout') or '').splitlines():
            record = json.loads(line)
            if not isinstance(record, dict) or set(record) != {'path', 'urls'}:
                raise ValueError('invalid worker restock record')
            path, urls = record['path'], record['urls']
            if path not in paths or not isinstance(urls, list) or not all(isinstance(url, str) and bulk.homepage(url) for url in urls):
                raise ValueError('worker restock record outside assigned paths')
            if path in records:
                raise ValueError('duplicate worker restock receipt')
            records[path] = urls
    added = 0
    incoming={}
    with closing(sqlite3.connect(catalog, timeout=30)) as db:
        db.execute('PRAGMA busy_timeout=120000')
        db.execute('CREATE TABLE IF NOT EXISTS commoncrawl_wat_prefixes(path TEXT PRIMARY KEY) WITHOUT ROWID')
        db.execute('CREATE TABLE IF NOT EXISTS archive_ready(host TEXT PRIMARY KEY,url TEXT NOT NULL) WITHOUT ROWID')
        db.execute('CREATE TABLE IF NOT EXISTS attempted(host TEXT PRIMARY KEY) WITHOUT ROWID')
        db.execute('''CREATE TABLE IF NOT EXISTS worker_restock_receipts(
            path TEXT PRIMARY KEY,batch TEXT NOT NULL,hosts INTEGER NOT NULL) WITHOUT ROWID''')
        db.commit()
        for path, urls in records.items():
            with TemporaryDirectory(prefix='cnc-restock-import-') as temporary:
                source = Path(temporary)/'candidates.txt'
                source.write_text(''.join(url+'\n' for url in urls), encoding='ascii')
                with db:
                    receipt = db.execute('INSERT OR IGNORE INTO commoncrawl_wat_prefixes VALUES (?)', (path,))
                    if not receipt.rowcount:
                        continue
                    # A WAT signal is a historical candidate, never a verified hit.
                    bulk.import_urls(db, source, 'commoncrawl', 'txt', summarize=False)
                    candidates = dict(bulk.homepage(url) for url in urls)
                    incoming.update(candidates)
                    queued = db.executemany('''INSERT OR IGNORE INTO archive_ready
                        SELECT ?,? WHERE NOT EXISTS (SELECT 1 FROM attempted WHERE host=?)''',
                        ((host,url,host) for host,url in candidates.items()))
                    db.execute('INSERT INTO worker_restock_receipts VALUES (?,?,?)', (path,batch,len(candidates)))
                    added += queued.rowcount
    if state is not None and incoming and source_policy.read(Path(state).parent)["commoncrawl"]:
        # Send fresh hostname candidates straight to worker verification. This
        # only excludes duplicates/already used hosts; it makes no HTTP probes.
        with closing(sqlite3.connect(state,timeout=30)) as ready,ready:
            room=max(0,ready.execute('SELECT target-available FROM pool_policy WHERE slot=1').fetchone()[0])
            ready.executemany("""INSERT OR IGNORE INTO ready_candidates SELECT ?,?,?
                WHERE NOT EXISTS(SELECT 1 FROM generator_checked WHERE host=?)
                  AND NOT EXISTS(SELECT 1 FROM merged_hosts WHERE host=?)
                  AND NOT EXISTS(SELECT 1 FROM shard_hosts WHERE host=?)""",
                ((h,u,'commoncrawl-wat' if (Path(state).parent/'commoncrawl-mode.json').exists() else 'commoncrawl',h,h,h) for h,u in sorted(incoming.items())[:room]))
            ready.execute('UPDATE pool_policy SET started=max(started,available>=10000),refilling=(available<target) WHERE slot=1')
    return added, len(records)


class Coordinator:
    def __init__(self,allow_new=True):
        self.allow_new=allow_new
        self.paths = None
        self.listed = 0
        self.cooldowns = {}

    def step(self, api, args, state):
        with closing(sqlite3.connect(state, timeout=30)) as db:
            row = db.execute("SELECT batch,job_id,paths FROM worker_restock_jobs WHERE phase!='done' ORDER BY created LIMIT 1").fetchone()
            refilling = db.execute('SELECT refilling FROM pool_policy WHERE slot=1').fetchone()[0]
        paused = (args.root/'paused').exists()
        enabled=source_policy.read(args.root)['commoncrawl'] and source_policy.mode(args.root)=='wat'
        if row:
            batch, job_id, raw = row
            assignments = json.loads(raw)
            paths = [path for group in assignments.values() for path in group] if isinstance(assignments,dict) else assignments
            if not job_id:
                # Recover a lost launch response before submitting the same wave again.
                found = next((job for job in (api.call('/api/jobs') or []) if job.get('name') == 'WordPress restock:'+batch), None)
                if found:
                    job_id = found['id']
                elif paused or not refilling or not enabled:
                    return
                else:
                    source = args.root/'restock-jobs'/(batch+'.txt')
                    payload={'name':'WordPress restock:'+batch,'timeout_seconds':180}
                    if isinstance(assignments,dict):
                        encoded=base64.b64encode(json.dumps(assignments,separators=(',',':')).encode()).decode()
                        payload.update(mode='broadcast',command=f'/usr/bin/python3 {WORKER_SCRIPT} --wave {encoded}')
                    else:
                        # Resume an already-prepared spread job from an earlier release.
                        payload.update(mode='spread',input_file=str(source),workers=wave_workers(raw),
                                       command=f'/usr/bin/python3 {WORKER_SCRIPT} --worker {{input}}')
                    launch = api.call('/api/jobs',payload)
                    job_id = launch['job_id']
                with closing(sqlite3.connect(state, timeout=30)) as db, db:
                    db.execute("UPDATE worker_restock_jobs SET job_id=?,phase='running' WHERE batch=?", (job_id,batch))
                LOG.info('Worker restock submitted %s: %d prefixes', job_id,len(paths))
            job = api.call('/api/jobs/'+job_id)
            if job['status'] in ('pending','queued','running'):
                if paused:
                    api.call('/api/jobs/'+job_id+'/cancel', {})
                return
            tasks = api.call('/api/jobs/'+job_id+'/tasks')
            try:
                added, finished = import_results(args.catalog,batch,paths,tasks,state)
                if getattr(args,"shard_ready_event",None):args.shard_ready_event.set()
            except ValueError as error:
                LOG.warning('Worker restock rejected %s: %s',job_id,error)
                added, finished = 0, 0
            if finished < len(paths):
                # Failed source requests do not monopolize every later wave.
                for path in paths:
                    self.cooldowns[path]=time.monotonic()+300
            with closing(sqlite3.connect(state, timeout=30)) as db, db:
                db.execute("UPDATE worker_restock_jobs SET phase='done',hosts=? WHERE batch=?",(added,batch))
            LOG.info('Worker restock imported %s: %d candidates from %d/%d prefixes',job_id,added,finished,len(paths))
            return
        if paused or not refilling or not self.allow_new or not enabled:
            return
        workers = sorted([w for w in (api.call('/api/workers') or []) if w.get('wordpress_bulk') and w.get('status') in ('online','busy')],key=lambda w:w['id'])
        if not workers:
            return
        if self.paths is None or time.monotonic()-self.listed>3600:
            self.paths=listing(args.catalog);self.listed=time.monotonic()
        with closing(sqlite3.connect(args.catalog, timeout=30)) as db:
            db.execute('CREATE TABLE IF NOT EXISTS commoncrawl_wat_prefixes(path TEXT PRIMARY KEY) WITHOUT ROWID')
            finished={path for path, in db.execute('SELECT path FROM commoncrawl_wat_prefixes')}
        paths=[path for path in self.paths if path not in finished and self.cooldowns.get(path,0)<=time.monotonic()][:len(workers)*PER_WORKER]
        # Each worker receives at most two prefixes in a bounded wave.
        if not paths:
            return
        assignments={w['id']:paths[index*PER_WORKER:(index+1)*PER_WORKER] for index,w in enumerate(workers) if index*PER_WORKER<len(paths)}
        batch=uuid.uuid4().hex
        directory=args.root/'restock-jobs';directory.mkdir(mode=0o700,exist_ok=True)
        source=directory/(batch+'.txt');source.write_text(''.join(path+'\n' for path in paths));source.chmod(0o600)
        with closing(sqlite3.connect(state, timeout=30)) as db, db:
            db.execute('INSERT INTO worker_restock_jobs(batch,paths,created) VALUES (?,?,?)',(batch,json.dumps(assignments),int(time.time())))


def run(api,args,state,allow_new=True):
    schema(state)
    coordinator=Coordinator(allow_new=allow_new)
    while True:
        try:
            coordinator.step(api,args,state)
            delay=2
        except (OSError,ValueError,KeyError,IndexError,EOFError,sqlite3.Error) as error:
            LOG.warning('Worker restock deferred: %s',error)
            delay=30
        time.sleep(delay)


if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    group=parser.add_mutually_exclusive_group(required=True)
    group.add_argument('--worker',type=Path)
    group.add_argument('--wave')
    args=parser.parse_args()
    sys.exit(worker(args.worker) if args.worker else wave(args.wave))
