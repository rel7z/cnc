"""Consistent SQLite snapshots for publication through CNC's Drive uploader."""
import hashlib
import gzip
import json
import os
from pathlib import Path
import sqlite3
import tempfile
import time
import uuid
from contextlib import closing


def snapshot(database, destination):
    database, destination = Path(database).resolve(), Path(destination).resolve()
    if not database.is_file() or database == destination:
        raise ValueError("Choose an existing database and a separate snapshot path")
    destination.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    fd, temporary = tempfile.mkstemp(prefix=".map-snapshot-", dir=destination.parent)
    os.close(fd)
    temporary = Path(temporary)
    try:
        with closing(sqlite3.connect(database.as_uri() + "?mode=ro", uri=True)) as source:
            # Pin a read snapshot so steady catalog writes cannot restart the backup.
            source.execute("BEGIN")
            source.execute("SELECT name FROM sqlite_schema LIMIT 1").fetchone()
            with closing(sqlite3.connect(temporary)) as target:
                source.backup(target, pages=1024, sleep=0.05)
                if target.execute("PRAGMA quick_check").fetchone() != ("ok",):
                    raise RuntimeError("Snapshot integrity check failed")
        digest = hashlib.sha256()
        with temporary.open("rb") as stream:
            for chunk in iter(lambda: stream.read(1 << 20), b""):
                digest.update(chunk)
        receipt = {"created_at": int(time.time()), "filename": destination.name,
                   "bytes": temporary.stat().st_size, "sha256": digest.hexdigest(),
                   "integrity": "ok"}
        with temporary.open("rb") as stream:
            os.fsync(stream.fileno())
        os.replace(temporary, destination)
        return receipt
    finally:
        temporary.unlink(missing_ok=True)


def publish_snapshot(database, output, chunk_size=32 << 20):
    """Publish immutable bounded parts; CNC uploads these without loading a huge DB."""
    if not 1 << 20 <= chunk_size <= 64 << 20:
        raise ValueError("Snapshot chunks must be between 1 and 64 MiB")
    output = Path(output).resolve()
    output.mkdir(parents=True, exist_ok=True, mode=0o700)
    name = 'internet-map-' + str(int(time.time())) + '-' + uuid.uuid4().hex
    with tempfile.TemporaryDirectory(prefix='.map-backup-', dir=output) as work:
        source = Path(work) / 'snapshot.db'
        receipt = snapshot(database, source)
        parts = []
        with source.open('rb') as stream:
            while block := stream.read(chunk_size):
                filename = name + '.part%06d.gz' % len(parts)
                block = gzip.compress(block, mtime=0)
                staged = Path(work) / filename
                staged.write_bytes(block)
                staged.chmod(0o600)
                os.replace(staged, output / filename)
                parts.append(dict(filename=filename, bytes=len(block),
                                  sha256=hashlib.sha256(block).hexdigest()))
        receipt.update(format='internet-map-sqlite-v1', encoding='gzip-parts', parts=parts,
                       database=Path(database).name)
        manifest = Path(work) / (name + '.manifest.json')
        manifest.write_text(json.dumps(receipt, indent=2) + '\n')
        manifest.chmod(0o600)
        os.replace(manifest, output / manifest.name)
        return dict(receipt, manifest=manifest.name)


def restore_snapshot(manifest, destination):
    """Restore only into a missing destination; verify every part and SQLite first."""
    manifest, destination = Path(manifest).resolve(), Path(destination).resolve()
    if destination.exists():
        raise ValueError('Restore requires a separate, missing database path')
    receipt = json.loads(manifest.read_text())
    if receipt.get('format') != 'internet-map-sqlite-v1' or not receipt.get('parts'):
        raise ValueError('Invalid snapshot manifest')
    destination.parent.mkdir(parents=True, exist_ok=True)
    fd, temp = tempfile.mkstemp(prefix='.restore-', dir=destination.parent)
    try:
        digest, length = hashlib.sha256(), 0
        with os.fdopen(fd, 'wb') as output:
            for part in receipt['parts']:
                name = part['filename']
                if Path(name).name != name or not name.startswith('internet-map-'):
                    raise ValueError('Invalid snapshot part path')
                path = manifest.parent / name
                if path.is_symlink():
                    raise ValueError('Snapshot parts cannot be symlinks')
                data = path.read_bytes()
                if len(data) != part['bytes'] or hashlib.sha256(data).hexdigest() != part['sha256']:
                    raise ValueError('Snapshot part checksum mismatch')
                if receipt.get('encoding') == 'gzip-parts':
                    data = gzip.decompress(data)
                output.write(data); digest.update(data); length += len(data)
            output.flush(); os.fsync(output.fileno())
        if digest.hexdigest() != receipt['sha256'] or length != receipt['bytes']:
            raise ValueError('Snapshot checksum mismatch')
        with closing(sqlite3.connect(temp)) as db:
            if db.execute('PRAGMA quick_check').fetchone() != ('ok',):
                raise ValueError('Restored database integrity check failed')
        os.link(temp, destination)
        return dict(sha256=digest.hexdigest(), bytes=length, integrity='ok')
    finally:
        Path(temp).unlink(missing_ok=True)
