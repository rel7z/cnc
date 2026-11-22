"""Idempotently ingest discovery classifications without inventing HTML evidence."""
import json
from pathlib import Path
from datetime import datetime, timezone

from main import connect


def sync_verified(database, discovery_state, limit=5000):
    if not 1 <= limit <= 50000:
        raise ValueError('Invalid classification import batch size')
    state = Path(discovery_state).resolve()
    if not state.is_file():
        return {'imported': 0}
    db = connect(database)
    try:
        db.execute('ATTACH DATABASE ? AS discovery', (state.as_uri() + '?mode=ro',))
        db.executescript('''
            CREATE TABLE IF NOT EXISTS discovery_results (
                host TEXT PRIMARY KEY, url TEXT NOT NULL, imported_at TEXT NOT NULL,
                source TEXT NOT NULL, classification TEXT NOT NULL
            ) WITHOUT ROWID;
            CREATE TABLE IF NOT EXISTS discovery_catalog_import (
                slot INTEGER PRIMARY KEY CHECK(slot=1), import_id INTEGER NOT NULL
            );
        ''')
        now = datetime.now(timezone.utc).isoformat()
        with db:
            db.execute('BEGIN IMMEDIATE')
            row = db.execute('SELECT import_id FROM discovery_catalog_import WHERE slot=1').fetchone()
            if not row:
                ident = db.execute('INSERT INTO imports(source,imported_at) VALUES (?,?)',
                                   ('internet-map:cnc-generator-results', now)).lastrowid
                db.execute('INSERT INTO discovery_catalog_import VALUES (1,?)', (ident,))
            else:
                ident = row[0]
            rows = db.execute('''SELECT host,url FROM discovery.merged_hosts AS d
                WHERE NOT EXISTS(SELECT 1 FROM discovery_results r WHERE r.host=d.host)
                ORDER BY host LIMIT ?''', (limit,)).fetchall()
            for host, url in rows:
                db.execute('INSERT OR IGNORE INTO sites(url,host) VALUES (?,?)', (url, host))
                db.execute('INSERT INTO discovery_results VALUES (?,?,?,?,?)',
                           (host, url, now, 'cnc-generator', 'wordpress'))
                db.execute('INSERT OR IGNORE INTO observations VALUES (?,?,?)',
                           (ident, url, json.dumps(['CNC WordPress generator classification'])))
            db.execute('UPDATE imports SET accepted=accepted+? WHERE id=?', (len(rows), ident))
        return {'imported': len(rows)}
    finally:
        db.close()
