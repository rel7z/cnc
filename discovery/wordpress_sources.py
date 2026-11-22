"""Persistent, hot-reloaded source switches for the private generator."""
import argparse
import fcntl
import json
import os
import tempfile
import time
import urllib.request
from pathlib import Path
from datetime import datetime,timezone

# Preserve legacy deployments until the owner explicitly chooses a source set.
LEGACY={'commoncrawl':True,'reverse_ip':True,'ct':True,'other_catalogs':True}
COMMONCRAWL_ONLY={name:name=='commoncrawl' for name in LEGACY}
GROUPS={'commoncrawl':('commoncrawl','commoncrawl-index','commoncrawl-wat'),'reverse_ip':('reverse-ip','reverse-seed'),'ct':('ct',),
        'other_catalogs':('archive','ranked','webgraph','random-txt','historical','discovery','retry')}


def read(root):
    try:
        value=json.loads((Path(root)/'sources.json').read_text())
        if not isinstance(value,dict) or set(value)!=set(LEGACY) or not all(type(v) is bool for v in value.values()):
            raise ValueError('invalid source configuration')
        return value
    except FileNotFoundError:
        return dict(LEGACY)


def allowed(policy,root=None):
    return tuple(source for name,group in GROUPS.items() if policy.get(name) for source in group
                 if source!='commoncrawl' or root is None or not (Path(root)/'commoncrawl-mode.json').exists())


def update(root,patch):
    if not isinstance(patch,dict) or not patch or not set(patch)<=set(LEGACY) or not all(type(v) is bool for v in patch.values()):
        raise ValueError('Expected source names and boolean switches')
    root=Path(root);root.mkdir(parents=True,exist_ok=True,mode=0o700)
    with (root/'sources.lock').open('a') as lock:
        fcntl.flock(lock,fcntl.LOCK_EX)
        value={**read(root),**patch}
        fd,name=tempfile.mkstemp(prefix='.sources-',dir=root)
        try:
            with os.fdopen(fd,'w') as output:json.dump(value,output)
            os.replace(name,root/'sources.json')
        finally:
            if os.path.exists(name):os.unlink(name)
    return value

def mode(root):
    try:
        value=json.loads((Path(root)/'commoncrawl-mode.json').read_text())['mode']
        if value not in ('index','wat'):raise ValueError('invalid Common Crawl mode')
        return value
    except FileNotFoundError:return 'wat'


def set_mode(root,value):
    if value not in ('index','wat'):raise ValueError('Choose index or wat')
    root=Path(root);root.mkdir(parents=True,exist_ok=True,mode=0o700)
    with (root/'sources.lock').open('a') as lock:
        fcntl.flock(lock,fcntl.LOCK_EX)
        fd,name=tempfile.mkstemp(prefix='.source-mode-',dir=root)
        try:
            with os.fdopen(fd,'w') as output:json.dump({'mode':value},output)
            os.replace(name,root/'commoncrawl-mode.json')
        finally:
            if os.path.exists(name):os.unlink(name)
    return value


def collection_records(root=None,fetch=None):
    fetch=fetch or urllib.request.urlopen
    cache=Path(root)/'crawl-collections.json' if root is not None else None
    saved=None
    if cache is not None:
        try:
            saved=json.loads(cache.read_text())
            if not isinstance(saved,list):saved=None
            if saved is not None and time.time()-cache.stat().st_mtime<3600:return saved
        except (FileNotFoundError,ValueError):pass
    try:
        with fetch('https://index.commoncrawl.org/collinfo.json',timeout=30) as response:data=response.read((2<<20)+1)
        if len(data)>2<<20:raise ValueError('crawl inventory is too large')
        records=json.loads(data)
        if not isinstance(records,list):raise ValueError('invalid crawl inventory')
    except (OSError,ValueError):
        if saved is not None:return saved
        raise
    if cache is not None:
        fd,name=tempfile.mkstemp(prefix='.crawl-inventory-',dir=cache.parent)
        try:
            with os.fdopen(fd,'w') as output:json.dump(records,output)
            os.replace(name,cache)
        finally:
            if os.path.exists(name):os.unlink(name)
    return records


def recent_crawls(records,now=None):
    now=now or datetime.now(timezone.utc)
    try:cutoff=now.replace(year=now.year-2)
    except ValueError:cutoff=now.replace(year=now.year-2,day=28)
    result=[]
    for record in records:
        try:
            start=datetime.fromisoformat(record['from'].replace('Z','+00:00'))
            if start.tzinfo is None:start=start.replace(tzinfo=timezone.utc)
            if cutoff<=start<=now:result.append(record)
        except (ValueError,KeyError,TypeError):continue
    return result


if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root',type=Path,required=True)
    group=parser.add_mutually_exclusive_group(required=True)
    group.add_argument('--set-json')
    group.add_argument('--set-mode',choices=('index','wat'))
    args=parser.parse_args()
    print(json.dumps({'mode':set_mode(args.root,args.set_mode)}) if args.set_mode else json.dumps(update(args.root,json.loads(args.set_json))))
