"""Bounded reverse-IP restock waves on managed workers; no catalog crawl work."""
import argparse
import base64
import csv
import io
import ipaddress
import json
import logging
import random
import socket
import sqlite3
import time
import uuid
from contextlib import closing
from pathlib import Path
from urllib.error import HTTPError
from urllib.request import Request, urlopen

import wordpress_bulk as bulk
import wordpress_sources as source_policy

LOG = logging.getLogger('wordpress-generator')
SCRIPT = '/opt/cnc/tools/wordpress-restock/wordpress_reverse.py'
MAX_HOSTS = 50000
MAX_RESPONSE = 32 << 20
MAX_CACHE = 10_000_000


def public_ip(value):
    try:
        ip = ipaddress.IPv4Address(value)
        return str(ip) if ip.is_global and not ip.is_multicast else None
    except (ipaddress.AddressValueError, TypeError):
        return None


def domains(provider, ip):
    url = (f'https://ip.thc.org/api/v1/download?ip_address={ip}&limit={MAX_HOSTS}' if provider == 'thc'
           else f'https://api.dnsrift.net/reverse-ip-lookup?ip={ip}')
    try:
        with urlopen(Request(url, headers={'User-Agent': 'CNC WordPress Generator'}), timeout=20) as response:
            data = response.read(MAX_RESPONSE + 1)
    except HTTPError as error:
        if provider != 'thc' or error.code != 404:
            raise
        error.close()
        return []
    if len(data) > MAX_RESPONSE:
        raise ValueError('oversized reverse IP response')
    if provider == 'dnsrift':
        value = json.loads(data)
        if not isinstance(value, dict) or value.get('error') or value.get('success') is False or value.get('ip', ip) != ip:
            raise ValueError('invalid reverse IP response')
        values = value.get('domain_list')
        if not isinstance(values, list) or not all(isinstance(v, str) for v in values):
            raise ValueError('invalid reverse IP domains')
        return values[:MAX_HOSTS]
    if not data:
        return []
    rows = csv.DictReader(io.StringIO(data.decode('utf-8-sig')))
    if not {'apexDomain', 'subdomain'}.issubset(rows.fieldnames or ()):
        raise ValueError('invalid reverse IP CSV')
    found = []
    for row in rows:
        apex = (row['apexDomain'] or '').strip().lower().rstrip('.')
        host = (row['subdomain'] or apex).strip().lower().rstrip('.')
        if apex and (host == apex or host.endswith('.' + apex)):
            found.append(host)
        if len(found) >= MAX_HOSTS:
            break
    return found


def wave(encoded, config=Path('/opt/cnc/config.json')):
    if len(encoded) > 60000:
        raise ValueError('oversized reverse wave')
    payload = json.loads(base64.b64decode(encoded, validate=True))
    assignments = payload.get('assignments') if isinstance(payload, dict) else None
    provider = payload.get('provider') if isinstance(payload, dict) else None
    if provider not in ('thc', 'dnsrift') or not isinstance(assignments, dict) or not assignments or not all(
        isinstance(worker, str) and public_ip(ip) == ip for worker, ip in assignments.items()
    ) or len(set(assignments.values())) != len(assignments):
        raise ValueError('invalid reverse wave')
    ident = json.loads(config.read_text())['worker_id']
    if ident not in assignments:
        return 0
    ip = assignments[ident]
    for name in (provider, 'dnsrift' if provider == 'thc' else 'thc'):
        try:
            urls = dict(candidate for host in domains(name, ip) if (candidate := bulk.homepage(host)))
            print(json.dumps({'ip': ip, 'provider': name, 'urls': list(urls.values())}, separators=(',', ':')), flush=True)
            return 0
        except (OSError, ValueError, EOFError, csv.Error) as error:
            code = error.code if isinstance(error, HTTPError) else None
            if isinstance(error, HTTPError):
                error.close()
            print(f'Reverse provider {name} failed: {type(error).__name__} HTTP={code}', flush=True, file=__import__('sys').stderr)
    return 1


def schema(state):
    with closing(sqlite3.connect(state, timeout=30)) as db, db:
        db.execute('''CREATE TABLE IF NOT EXISTS reverse_worker_jobs(
            batch TEXT PRIMARY KEY,job_id TEXT,assignments TEXT NOT NULL,provider TEXT NOT NULL,
            phase TEXT NOT NULL DEFAULT 'prepared',created INTEGER NOT NULL,hosts INTEGER NOT NULL DEFAULT 0)''')


def assignments(db, workers, now):
    """Resolve the CT/catalog input slice, then reverse only its origin IPs."""
    ips=[];seen_seeds=[];origins=[]
    seeds=db.execute('SELECT host,source FROM reverse_input_seeds WHERE due<=? ORDER BY due,host LIMIT ?', (now,max(32,len(workers)*8))).fetchall()
    for host,source in seeds:
        if len(ips)>=len(workers):break
        try:
            resolved=[row[4][0] for row in socket.getaddrinfo(host+'.',None,socket.AF_INET,socket.SOCK_STREAM)]
        except OSError:
            resolved=[]
        for value in resolved:
            ip=public_ip(value)
            if ip and ip not in ips and not db.execute('SELECT 1 FROM reverse_lookups WHERE ip=?',(ip,)).fetchone():
                ips.append(ip);origins.append((ip,host,source));break
        seen_seeds.append((now+86400,host))
    # Network resolution happens before acquiring SQLite's write lock.
    db.executemany('UPDATE reverse_input_seeds SET due=? WHERE host=?',seen_seeds)
    db.executemany('INSERT OR IGNORE INTO reverse_ip_origins VALUES (?,?,?)',origins)
    return {worker['id']:ip for worker,ip in zip(workers,ips)}


def import_results(state, batch, assigned, tasks, catalog=None):
    records = {}
    for task in tasks:
        worker = task.get('assigned_to')
        if worker not in assigned:
            continue
        for line in ((task.get('result') or {}).get('stdout') or '').splitlines():
            value = json.loads(line)
            if not isinstance(value, dict) or set(value) != {'ip', 'provider', 'urls'} or value['ip'] != assigned[worker] or value['provider'] not in ('thc', 'dnsrift'):
                raise ValueError('reverse receipt outside assigned IP')
            urls = value['urls']
            if not isinstance(urls, list) or len(urls) > MAX_HOSTS or not all(isinstance(u,str) and bulk.homepage(u) for u in urls) or worker in records:
                raise ValueError('invalid or duplicate reverse receipt')
            records[worker] = value
    if catalog is not None and records:
        with closing(sqlite3.connect(catalog,timeout=30)) as cat,cat:
            cat.executemany("""INSERT INTO candidates(host,url,sources) VALUES (?,?,2)
                ON CONFLICT(host) DO UPDATE SET sources=candidates.sources|2
                WHERE (candidates.sources&2)=0""",
                ((h,u) for value in records.values() for url in value['urls'] if (candidate:=bulk.homepage(url)) for h,u in [candidate]))
    with closing(sqlite3.connect(state, timeout=30)) as db, db:
        row = db.execute('SELECT phase FROM reverse_worker_jobs WHERE batch=?', (batch,)).fetchone()
        if not row or row[0] == 'done':
            return 0
        before = db.total_changes
        room=max(0,MAX_CACHE-db.execute('SELECT count(*) FROM reverse_candidates').fetchone()[0])
        for value in records.values():
            start_changes=db.total_changes
            db.executemany('''INSERT OR IGNORE INTO reverse_candidates SELECT ?,?
                WHERE NOT EXISTS(SELECT 1 FROM generator_checked WHERE host=?)
                  AND NOT EXISTS(SELECT 1 FROM merged_hosts WHERE host=?)
                  AND NOT EXISTS(SELECT 1 FROM ready_candidates WHERE host=?)
                  AND NOT EXISTS(SELECT 1 FROM shard_hosts WHERE host=?)''',
                ((h,u,h,h,h,h) for url in value['urls'][:room] if (candidate := bulk.homepage(url)) for h,u in [candidate]))
            room-=db.total_changes-start_changes
        added = db.total_changes - before
        now = int(time.time())
        db.executemany('INSERT OR REPLACE INTO reverse_lookups VALUES (?,?)', ((r['ip'],now) for r in records.values()))
        db.execute("UPDATE reverse_worker_jobs SET phase='done',hosts=? WHERE batch=?", (added,batch))
    return added


class Coordinator:
    def __init__(self):
        self.cooldown=0

    def step(self, api, args, state):
        with closing(sqlite3.connect(state,timeout=30)) as db:
            row = db.execute("SELECT batch,job_id,assignments,provider,created FROM reverse_worker_jobs WHERE phase!='done' ORDER BY created LIMIT 1").fetchone()
            refilling = db.execute('SELECT refilling FROM pool_policy WHERE slot=1').fetchone()[0]
        paused = (args.root/'paused').exists()
        enabled=source_policy.read(args.root)['reverse_ip']
        if row:
            batch,job_id,raw,provider,created = row
            assigned = json.loads(raw)
            if not job_id:
                found = next((job for job in (api.call('/api/jobs') or []) if job.get('name') == 'WordPress reverse:'+batch),None)
                if found:
                    job_id = found['id']
                elif paused or not refilling or not enabled:
                    return
                else:
                    encoded=base64.b64encode(json.dumps({'provider':provider,'assignments':assigned},separators=(',',':')).encode()).decode()
                    launch=api.call('/api/jobs',{'name':'WordPress reverse:'+batch,'mode':'broadcast','timeout_seconds':60,'command':f'/usr/bin/python3 {SCRIPT} --wave {encoded}'})
                    job_id=launch['job_id']
                with closing(sqlite3.connect(state,timeout=30)) as db,db:
                    db.execute("UPDATE reverse_worker_jobs SET job_id=?,phase='running' WHERE batch=?",(job_id,batch))
            job=api.call('/api/jobs/'+job_id)
            if job['status'] in ('pending','queued','running'):
                if paused:api.call('/api/jobs/'+job_id+'/cancel',{})
                return
            tasks=api.call('/api/jobs/'+job_id+'/tasks')
            try:
                added=import_results(state,batch,assigned,tasks,args.catalog)
            except ValueError as error:
                LOG.warning('Worker reverse receipt rejected: %s',error)
                with closing(sqlite3.connect(state,timeout=30)) as db,db:
                    db.execute("UPDATE reverse_worker_jobs SET phase='done' WHERE batch=?",(batch,))
                added=0
            if any('HTTP=429' in ((task.get('result') or {}).get('stderr') or '') for task in tasks):
                self.cooldown=time.monotonic()+60
            elif not any(((task.get('result') or {}).get('stdout') or '').strip() for task in tasks):
                self.cooldown=time.monotonic()+10
            LOG.info('Worker reverse imported %s: %d unique candidates from %d workers',job_id,added,len(assigned))
            return
        if paused or not refilling or not enabled or time.monotonic()<self.cooldown:
            return
        now=int(time.time())
        with closing(sqlite3.connect(state,timeout=30)) as db:
            if db.execute('SELECT count(*) FROM reverse_candidates').fetchone()[0]>=MAX_CACHE:return
            last=db.execute('SELECT max(created) FROM reverse_worker_jobs').fetchone()[0]
            # At most one request per worker per six seconds; failures do not
            # trigger immediate provider bursts.
            if last and now-last<6:return
        workers=sorted([w for w in (api.call('/api/workers') or []) if w.get('wordpress_bulk') and w.get('status') in ('online','busy')],key=lambda w:w['id'])
        if not workers:return
        with closing(sqlite3.connect(state,timeout=30)) as db,db:
            assigned=assignments(db,workers,now)
            if not assigned:return
            batch=uuid.uuid4().hex
            provider='thc' if db.execute('SELECT count(*) FROM reverse_worker_jobs').fetchone()[0]%2==0 else 'dnsrift'
            db.execute('INSERT INTO reverse_worker_jobs(batch,assignments,provider,created) VALUES (?,?,?,?)',(batch,json.dumps(assigned),provider,now))


def run(api,args,state):
    schema(state)
    coordinator=Coordinator()
    while True:
        try:
            coordinator.step(api,args,state)
            delay=1
        except (OSError,ValueError,KeyError,TypeError,EOFError,sqlite3.Error) as error:
            LOG.warning('Worker reverse deferred (%s)',type(error).__name__)
            delay=10
        time.sleep(delay)


if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--wave',required=True)
    raise SystemExit(wave(parser.parse_args().wave))
