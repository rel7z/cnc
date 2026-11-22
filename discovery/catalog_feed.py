"""Feed Internet Map's canonical catalog into discovery without HTTP requests."""
from contextlib import closing
import logging
from pathlib import Path
import sqlite3
import time

import wordpress_bulk as bulk

LOG = logging.getLogger('wordpress-generator')


def stage(root, state, catalog, limit=5000):
    if not (root / 'cutover.json').exists() or not catalog.exists():
        return 0
    with closing(sqlite3.connect(state, timeout=30)) as db:
        db.execute('CREATE TABLE IF NOT EXISTS map_catalog_cursor(slot INTEGER PRIMARY KEY CHECK(slot=1), url TEXT NOT NULL)')
        db.commit()
        with db:
            db.execute('BEGIN IMMEDIATE')
            available, target, refilling = db.execute('SELECT available,target,refilling FROM pool_policy').fetchone()
            if not refilling or available >= target:
                return 0
            row = db.execute('SELECT url FROM map_catalog_cursor WHERE slot=1').fetchone()
            cursor = row[0] if row else ''
            with closing(sqlite3.connect(catalog.resolve().as_uri() + '?mode=ro', uri=True, timeout=30)) as source:
                rows = source.execute('SELECT url FROM sites WHERE url>? ORDER BY url LIMIT ?',
                                      (cursor, min(limit, target - available))).fetchall()
            if not rows:
                db.execute("INSERT OR REPLACE INTO map_catalog_cursor VALUES (1,'')")
                return 0
            added = 0
            for url, in rows:
                item = bulk.homepage(url)
                if not item:
                    continue
                host, normalized = item
                if db.execute('''SELECT 1 WHERE EXISTS(SELECT 1 FROM generator_checked WHERE host=?)
                    OR EXISTS(SELECT 1 FROM merged_hosts WHERE host=?)
                    OR EXISTS(SELECT 1 FROM shard_hosts WHERE host=?)
                    OR EXISTS(SELECT 1 FROM deferred WHERE host=? AND until>unixepoch())''',
                              (host, host, host, host)).fetchone():
                    continue
                added += db.execute("INSERT OR IGNORE INTO ready_candidates VALUES (?,?,'catalog')",
                                    (host, normalized)).rowcount
            db.execute('INSERT OR REPLACE INTO map_catalog_cursor VALUES (1,?)', (rows[-1][0],))
            db.execute('UPDATE pool_policy SET started=max(started,available>=10000),refilling=(available<target)')
            return added


def run(args, state):
    catalog = args.root.parent / 'catalog.db'
    while True:
        try:
            added = stage(args.root, state, catalog)
            if added and getattr(args, 'shard_ready_event', None):
                args.shard_ready_event.set()
        except (OSError, ValueError, sqlite3.Error) as error:
            LOG.warning('Internet Map catalog feed deferred (%s)', type(error).__name__)
            added = 0
        time.sleep(0.1 if added else 2)
