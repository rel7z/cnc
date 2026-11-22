"""One signed, compressed, deployable Internet Map recovery file."""
from contextlib import closing
import gzip
import hashlib
import hmac
import json
import os
from pathlib import Path
import re
import shutil
import sqlite3
import tarfile
import tempfile
import time
import uuid

from backup import snapshot

DATABASES = ('data/catalog.db', 'data/discovery/generator-state.db', 'data/discovery/candidates.db')
SETTINGS = ('sources.json', 'commoncrawl-mode.json', 'output-session.json')
CODE = ('api.py', 'main.py', 'intel.py', 'scanner.py', 'worker.py', 'cluster_worker.py',
        'org_zone.py', 'run.py', 'coordination.py', 'backup.py', 'bundle.py',
        'catalog_sync.py', 'maintenance.py', 'infra/install-services.py', 'infra/install-discovery.py')


def digest(path):
    value = hashlib.sha256()
    with Path(path).open('rb') as stream:
        for block in iter(lambda: stream.read(1 << 20), b''):
            value.update(block)
    return value.hexdigest()


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':')).encode()


def source_files(root):
    names = list(CODE)
    for folder in ('discovery', 'web/app', 'web/public'):
        path = root / folder
        if path.exists():
            for item in path.rglob('*'):
                if item.is_symlink():
                    raise ValueError('Source symlinks are not included in recovery files')
                if item.is_file() and item.suffix in ('.py', '.ts', '.tsx', '.css', '.svg', '.png', '.jpg', '.ico', '.woff2'):
                    names.append(item.relative_to(root).as_posix())
    names += ['web/' + name for name in ('package.json', 'package-lock.json', 'next.config.ts',
              'tsconfig.json', 'next-env.d.ts', 'postcss.config.mjs')]
    return sorted({name for name in names if (root / name).is_file()})


def retain_pending_inputs(stage, root):
    """A restored controller must not refer to vanished leased input files."""
    with closing(sqlite3.connect(stage / DATABASES[1])) as db:
        tables = {row[0] for row in db.execute("SELECT name FROM sqlite_schema WHERE type='table'")}
        if not {'shard_leases', 'shard_hosts'} <= tables:
            return
        for batch, in db.execute('SELECT batch FROM shard_leases'):
            if not re.fullmatch('[a-f0-9]{32}', batch):
                raise ValueError('Invalid pending input identifier')
            source = root / 'data/discovery' / (batch + '.txt')
            target = stage / 'data/discovery' / source.name
            if source.is_symlink():
                raise ValueError('Pending input cannot be a symlink')
            if source.is_file():
                shutil.copy2(source, target)
            else:
                # A lease can finish after its SQLite snapshot was pinned. Its
                # snapshot hosts still provide a complete recoverable input.
                hosts = [row[0] for row in db.execute('SELECT host FROM shard_hosts WHERE batch=? ORDER BY host', (batch,))]
                if not hosts or any(not isinstance(host, str) or any(c in host for c in '\r\n') for host in hosts):
                    raise ValueError('Pending lease has no recoverable input')
                target.write_text(''.join('https://' + host + '\n' for host in hosts), encoding='ascii')


def create_bundle(root, output, key, include_source=True):
    root, output = Path(root).resolve(), Path(output).resolve()
    if len(key) < 32:
        raise ValueError('A private recovery key is required')
    output.mkdir(parents=True, exist_ok=True, mode=0o700)
    name = 'internet-map-backup-' + str(int(time.time())) + '-' + uuid.uuid4().hex + '.tar.gz'
    with tempfile.TemporaryDirectory(prefix='.map-bundle-', dir=output) as temporary:
        stage = Path(temporary)
        manifest = dict(format='internet-map-bundle-v2', created_at=int(time.time()),
                        source_included=include_source, entries={})
        for relative in DATABASES:
            snapshot(root / relative, stage / relative)
        retain_pending_inputs(stage, root)
        for name_setting in SETTINGS:
            relative = 'data/discovery/' + name_setting
            if (root / relative).is_file():
                if (root / relative).is_symlink():
                    raise ValueError('Recovery settings cannot be symlinks')
                shutil.copy2(root / relative, stage / relative)
        if include_source:
            for relative in source_files(root):
                source = root / relative
                if source.is_symlink():
                    raise ValueError('Recovery source cannot be a symlink')
                target = stage / relative
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(source, target)
            if not (stage / 'api.py').exists() or not (stage / 'infra/install-services.py').exists():
                raise ValueError('Deployable application source is required')
        for path in stage.rglob('*'):
            if path.is_file():
                manifest['entries'][path.relative_to(stage).as_posix()] = dict(bytes=path.stat().st_size, sha256=digest(path))
        manifest['signature'] = hmac.new(key, canonical(manifest), 'sha256').hexdigest()
        (stage / 'manifest.json').write_bytes(canonical(manifest))
        archive = stage / name
        with archive.open('wb') as raw, gzip.GzipFile(fileobj=raw, mode='wb', compresslevel=3, mtime=0) as compressed:
            with tarfile.open(fileobj=compressed, mode='w|') as tar:
                tar.add(stage / 'manifest.json', arcname='manifest.json', recursive=False)
                for relative in sorted(manifest['entries']):
                    tar.add(stage / relative, arcname=relative, recursive=False)
        archive.chmod(0o600)
        result = dict(filename=name, bytes=archive.stat().st_size, sha256=digest(archive),
                      format=manifest['format'], databases=list(DATABASES), source_included=include_source)
        os.replace(archive, output / name)
        return result


def restore_bundle(archive, destination, key):
    destination = Path(destination).resolve()
    if len(key) < 32 or destination.exists():
        raise ValueError('Restore needs the private recovery key and a missing destination')
    destination.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='.map-restore-', dir=destination.parent) as temporary:
        stage = Path(temporary) / 'application'
        stage.mkdir(mode=0o700)
        with tarfile.open(archive, 'r|gz') as tar:
            first = tar.next()
            if first is None or first.name != 'manifest.json' or not first.isfile() or first.size > 2 << 20:
                raise ValueError('Invalid recovery manifest')
            manifest = json.load(tar.extractfile(first))
            signature = manifest.pop('signature', '')
            if manifest.get('format') != 'internet-map-bundle-v2' or not hmac.compare_digest(signature, hmac.new(key, canonical(manifest), 'sha256').hexdigest()):
                raise ValueError('Recovery signature does not match')
            entries = manifest['entries']
            if not set(DATABASES) <= set(entries):
                raise ValueError('Recovery databases are missing')
            seen = set()
            while (member := tar.next()) is not None:
                name = member.name
                path = Path(name)
                if not member.isfile() or path.is_absolute() or '..' in path.parts or name not in entries or name in seen:
                    raise ValueError('Unsafe recovery archive member')
                if member.size != entries[name]['bytes']:
                    raise ValueError('Recovery file size mismatch')
                target = stage / path
                target.parent.mkdir(parents=True, exist_ok=True)
                with tar.extractfile(member) as source, target.open('xb') as output:
                    shutil.copyfileobj(source, output, length=1 << 20)
                target.chmod(0o600)
                if digest(target) != entries[name]['sha256']:
                    raise ValueError('Recovery file checksum mismatch')
                seen.add(name)
            if seen != set(entries):
                raise ValueError('Recovery archive is incomplete')
        for relative in DATABASES:
            with closing(sqlite3.connect(stage / relative)) as db:
                if db.execute('PRAGMA quick_check').fetchone() != ('ok',):
                    raise ValueError('Recovery database integrity check failed')
        (stage / 'data/discovery/paused').touch(mode=0o600)
        (stage / 'data/restore-receipt.json').write_text(json.dumps(dict(format=manifest['format'], source_included=manifest['source_included'], restored_at=int(time.time()))))
        stage.rename(destination)
        return dict(restored=True, databases=list(DATABASES), source_included=manifest['source_included'], integrity='ok')
