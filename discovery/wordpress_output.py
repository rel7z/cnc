"""Session-scoped WordPress exports with persistent, global hostname history."""

import argparse
import fcntl
import json
import os
import sqlite3
import time
import uuid
from contextlib import closing, contextmanager
from datetime import datetime, timedelta, timezone
from pathlib import Path

import wordpress_bulk as bulk


@contextmanager
def locked(state):
    with (state.parent / "output-publication.lock").open("a") as stream:
        fcntl.flock(stream.fileno(), fcntl.LOCK_EX)
        try:
            yield
        finally:
            fcntl.flock(stream.fileno(), fcntl.LOCK_UN)


def schema(db):
    db.executescript("""
        CREATE TABLE IF NOT EXISTS output_sessions (
            id TEXT PRIMARY KEY, started_at INTEGER NOT NULL, closed_at INTEGER,
            filename TEXT NOT NULL, verified INTEGER NOT NULL DEFAULT 0
        );
        CREATE TABLE IF NOT EXISTS output_current (
            slot INTEGER PRIMARY KEY CHECK(slot=1), session_id TEXT NOT NULL
        );
        CREATE TABLE IF NOT EXISTS output_hosts (
            session_id TEXT NOT NULL,host TEXT NOT NULL,url TEXT NOT NULL,
            PRIMARY KEY(session_id,host)
        ) WITHOUT ROWID;
        CREATE TRIGGER IF NOT EXISTS output_host_count AFTER INSERT ON output_hosts BEGIN
            UPDATE output_sessions SET verified=verified+1 WHERE id=NEW.session_id;
        END;
        CREATE TABLE IF NOT EXISTS output_batch_sessions (
            batch TEXT PRIMARY KEY,session_id TEXT NOT NULL
        ) WITHOUT ROWID;
        CREATE TABLE IF NOT EXISTS output_signatures (
            session_id TEXT PRIMARY KEY,device INTEGER,inode INTEGER,size INTEGER,modified INTEGER
        );
    """)


def session(db, ident=None):
    query = "SELECT id,started_at,closed_at,filename,verified FROM output_sessions WHERE id="
    cursor = db.execute(query + ("?" if ident else "(SELECT session_id FROM output_current WHERE slot=1)"),
                        (ident,) if ident else ())
    row = cursor.fetchone()
    return dict(zip((column[0] for column in cursor.description), row)) if row else None


def signature(path):
    info = path.stat()
    return info.st_dev, info.st_ino, info.st_size, info.st_mtime_ns


def remember_signature(db, row, path):
    with db:
        db.execute("INSERT OR REPLACE INTO output_signatures VALUES (?,?,?,?,?)", (row['id'], *signature(path)))


def changed(db, row, path):
    old = db.execute("SELECT device,inode,size,modified FROM output_signatures WHERE session_id=?", (row['id'],)).fetchone()
    try:
        return signature(path) != old
    except FileNotFoundError:
        return True


def new_session(db):
    ident = uuid.uuid4().hex
    with db:
        db.execute("INSERT INTO output_sessions(id,started_at,filename) VALUES (?,?,'wordpress.txt')", (ident,time.time_ns()))
        db.execute("INSERT OR REPLACE INTO output_current VALUES (1,?)", (ident,))
    return session(db, ident)


def bootstrap(db, merged):
    row = session(db)
    if row:
        return row
    row = new_session(db)
    target = merged / 'wordpress.txt'
    # Adopt the actual current export once. Missing/renamed files must never be
    # rebuilt from the all-time merged_hosts table in a subsequent session.
    if target.is_file():
        with target.open(encoding='utf-8-sig') as stream, db:
            db.executemany("INSERT OR IGNORE INTO output_hosts VALUES (?,?,?)",
                           ((row['id'],*item) for line in stream if (item := bulk.homepage(line))))
            db.execute("INSERT OR IGNORE INTO merged_hosts SELECT host,url FROM output_hosts WHERE session_id=?", (row['id'],))
        remember_signature(db,row,target)
    with db:
        tables={name for name, in db.execute("SELECT name FROM sqlite_schema WHERE type='table'")}
        for table in ('active','shard_leases','publications'):
            if table in tables:
                clause=" WHERE launched!=0" if table=='shard_leases' else ''
                db.execute(f"INSERT OR IGNORE INTO output_batch_sessions SELECT batch,? FROM {table}{clause}", (row['id'],))
    return session(db,row['id'])


def ingest(db, row, sources):
    db.execute("CREATE TEMP TABLE IF NOT EXISTS output_incoming(host TEXT PRIMARY KEY,url TEXT NOT NULL) WITHOUT ROWID")
    with db:
        db.execute("DELETE FROM output_incoming")
        for path in sources:
            if not path.is_file():
                continue
            if path.is_symlink():
                raise ValueError('export sources must not be symlinks')
            with path.open(encoding='utf-8-sig') as stream:
                db.executemany("INSERT OR IGNORE INTO output_incoming VALUES (?,?)",
                               (item for line in stream if (item := bulk.homepage(line))))
        # Only new global hosts enter this session. Replaying old result files
        # cannot move an earlier run's hosts into a new wordpress.txt.
        added = db.execute("""
            INSERT OR IGNORE INTO output_hosts
            SELECT ?,i.host,i.url FROM output_incoming i
            WHERE NOT EXISTS (SELECT 1 FROM merged_hosts m WHERE m.host=i.host)
        """, (row['id'],)).rowcount
        db.execute("INSERT OR IGNORE INTO merged_hosts SELECT host,url FROM output_incoming")
        if added:
            db.execute("DELETE FROM output_signatures WHERE session_id=?", (row['id'],))
    return added


def render(db, row, merged):
    target=merged/row['filename']
    if not changed(db,row,target):
        return
    temporary=merged/('.wordpress-output-'+row['id']+'.tmp')
    with temporary.open('w',encoding='ascii') as stream:
        temporary.chmod(0o600)
        for url, in db.execute("SELECT url FROM output_hosts WHERE session_id=? ORDER BY host", (row['id'],)):
            stream.write('https://'+url.partition('://')[2].removesuffix('/')+'\n')
        stream.flush();os.fsync(stream.fileno())
    temporary.replace(target)
    sync_directory(merged)
    remember_signature(db,row,target)


def sync_directory(path):
    fd=os.open(path,os.O_RDONLY)
    try:os.fsync(fd)
    finally:os.close(fd)


def archive_name(db,merged):
    stamp=datetime.fromtimestamp(time.time(),timezone(timedelta(hours=7))).strftime('%Y%m%d-%H%M%S-%f')
    suffix=0
    while True:
        name=f'wp-{stamp}'+(f'-{suffix}' if suffix else '')+'.txt'
        if not (merged/name).exists() and not db.execute("SELECT 1 FROM output_sessions WHERE filename=?",(name,)).fetchone():
            return name
        suffix+=1


def finish_archive(db,row,merged):
    target=merged/'wordpress.txt';archive=merged/row['filename']
    if target.exists():
        if archive.exists():
            if target.samefile(archive):
                target.unlink()
            else:
                # Preserve an unexpected user-created export instead of
                # replacing either it or the existing archive.
                os.link(target,merged/archive_name(db,merged));target.unlink()
        else:
            os.link(target,archive);target.unlink()
        sync_directory(merged)
    if archive.exists() or row['verified']:
        render(db,row,merged)


def synchronize_locked(db,state,merged,enabled):
    row=bootstrap(db,merged)
    pause=state.parent/'paused'
    if row['closed_at'] is not None:
        finish_archive(db,row,merged)
    if enabled:
        if row['closed_at'] is not None:
            row=new_session(db)
        target=merged/row['filename']
        if target.exists() and changed(db,row,target):ingest(db,row,[target])
        render(db,session(db,row['id']),merged)
        pause.unlink(missing_ok=True)
    else:
        if not pause.exists():pause.touch(mode=0o600)
        if row['closed_at'] is None:
            target=merged/'wordpress.txt'
            if target.exists() and changed(db,row,target):ingest(db,row,[target])
            row=session(db,row['id'])
            if target.exists() or row['verified']:render(db,row,merged)
            with db:
                db.execute("UPDATE output_sessions SET closed_at=?,filename=? WHERE id=?",
                           (time.time_ns(),archive_name(db,merged),row['id']))
            row=session(db,row['id'])
            finish_archive(db,row,merged)
    return session(db)


def status(db):
    if not db.execute("SELECT 1 FROM sqlite_schema WHERE name='output_current'").fetchone():return None
    row=session(db)
    if not row:return None
    archive=db.execute("SELECT filename FROM output_sessions WHERE closed_at IS NOT NULL ORDER BY closed_at DESC LIMIT 1").fetchone()
    return {'output_session':row['id'],'output_file':row['filename'],
            'session_verified':row['verified'],'session_started_at':row['started_at']//1_000_000_000,
            'session_closed_at':row['closed_at']//1_000_000_000 if row['closed_at'] is not None else None,
            'last_archive':archive[0] if archive else None}


def manifest(db,state):
    data=status(db)
    target=state.parent/'output-session.json'
    temporary=target.with_name('.output-session-'+uuid.uuid4().hex+'.tmp')
    with temporary.open('x',encoding='utf-8') as stream:
        temporary.chmod(0o600);json.dump(data,stream);stream.flush();os.fsync(stream.fileno())
    temporary.replace(target)
    return data


def set_enabled(state,merged,enabled):
    merged.mkdir(parents=True,exist_ok=True)
    with locked(state),closing(sqlite3.connect(state,timeout=30)) as db:
        schema(db);synchronize_locked(db,state,merged,enabled)
        return {'enabled':enabled,**manifest(db,state)}


def synchronize(state,merged):
    merged.mkdir(parents=True,exist_ok=True)
    with locked(state),closing(sqlite3.connect(state,timeout=30)) as db:
        schema(db)
        synchronize_locked(db,state,merged,not (state.parent/'paused').exists())
        return manifest(db,state)


def bind_batch(state,merged,batch):
    with locked(state),closing(sqlite3.connect(state,timeout=30)) as db:
        schema(db)
        row=synchronize_locked(db,state,merged,not (state.parent/'paused').exists())
        if row['closed_at'] is not None:return False
        with db:db.execute("INSERT OR REPLACE INTO output_batch_sessions VALUES (?,?)", (batch,row['id']))
        manifest(db,state)
        return True


def publish(state,merged,sources,batch=None):
    with locked(state),closing(sqlite3.connect(state,timeout=30)) as db:
        schema(db);current=synchronize_locked(db,state,merged,not (state.parent/'paused').exists())
        row=current
        if batch:
            mapped=db.execute("SELECT session_id FROM output_batch_sessions WHERE batch=?",(batch,)).fetchone()
            if mapped:row=session(db,mapped[0])
            else:
                with db:db.execute("INSERT INTO output_batch_sessions VALUES (?,?)",(batch,row['id']))
        target=merged/row['filename']
        paths=([target] if target.is_file() and changed(db,row,target) else [])+list(sources)
        added=ingest(db,row,paths)
        render(db,session(db,row['id']),merged)
        manifest(db,state)
        return added


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root',type=Path,default=Path('/opt/cnc/wordpress-generator'))
    parser.add_argument('--merged',type=Path,default=Path('/opt/cnc/merged'))
    parser.add_argument('--control',choices=('on','off'),required=True)
    args=parser.parse_args();state=args.root/'generator-state.db'
    if not state.is_file():parser.error('generator state is required')
    print(json.dumps(set_enabled(state,args.merged,args.control=='on')))


if __name__=='__main__':main()
