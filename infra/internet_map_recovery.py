"""Trusted recovery of one signed Internet Map bundle on a fresh controller.

Only the release-installed verifier runs before authentication. Existing app
installations are never overwritten; recovered scanning stays paused.
"""
from contextlib import closing
import argparse
import fcntl
import hashlib
import hmac
import json
import os
from pathlib import Path
import re
import shutil
import sqlite3
import subprocess
import tarfile
import tempfile
import time
import urllib.parse
import urllib.request

DATABASES = ('data/catalog.db', 'data/discovery/generator-state.db', 'data/discovery/candidates.db')
BUNDLE = re.compile(r'^internet-map-backup-([0-9]{10,})-[a-f0-9]{32}\.tar\.gz$')


def digest(path):
    value = hashlib.sha256()
    with Path(path).open('rb') as stream:
        for block in iter(lambda: stream.read(1 << 20), b''):
            value.update(block)
    return value.hexdigest()


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':')).encode()


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


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args):
        return None


def recover(config_path, destination, key_path):
    """Download and authenticate the latest single-file backup, without launching work."""
    destination, key_path = Path(destination), Path(key_path)
    if destination.is_symlink() or destination.exists():
        return dict(status='existing', restored=False)
    key = key_path.read_bytes()
    if len(key) < 32:
        raise ValueError('Private recovery key is required')
    cfg = json.loads(Path(config_path).read_text())['gdrive']
    if not cfg.get('enabled'):
        return dict(status='drive-disabled', restored=False)
    token = json.loads(Path(cfg['token_path']).read_text())
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    body = urllib.parse.urlencode(dict(grant_type='refresh_token', refresh_token=token['refresh_token'],
        client_id=cfg['client_id'], client_secret=cfg['client_secret'])).encode()
    with opener.open(urllib.request.Request('https://oauth2.googleapis.com/token', data=body), timeout=30) as response:
        access = json.load(response)['access_token']
    headers = {'Authorization': 'Bearer ' + access}
    files, page = [], None
    while True:
        query = "trashed=false and name contains 'internet-map-backup-'"
        if cfg.get('parent_folder_id'):
            query += " and '" + cfg['parent_folder_id'].replace("'", "\\'") + "' in parents"
        params = dict(q=query, fields='nextPageToken,files(id,name,size)', pageSize=1000)
        if page:
            params['pageToken'] = page
        url = 'https://www.googleapis.com/drive/v3/files?' + urllib.parse.urlencode(params)
        with opener.open(urllib.request.Request(url, headers=headers), timeout=30) as response:
            result = json.load(response)
        files.extend(item for item in result.get('files', []) if BUNDLE.fullmatch(item['name']))
        page = result.get('nextPageToken')
        if not page:
            break
    if not files:
        return dict(status='no-backup', restored=False)
    selected = max(files, key=lambda item: (int(BUNDLE.fullmatch(item['name'])[1]), item['name']))
    # Keep all downloaded bytes outside the upload watcher. Failed verification
    # never replaces an existing installation or executes recovered code.
    destination.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='.map-download-', dir=destination.parent) as temporary:
        archive = Path(temporary) / selected['name']
        url = 'https://www.googleapis.com/drive/v3/files/' + urllib.parse.quote(selected['id'], safe='') + '?alt=media'
        length = 0
        maximum = int(selected['size'])
        with opener.open(urllib.request.Request(url, headers=headers), timeout=120) as response, archive.open('xb') as output:
            while block := response.read(1 << 20):
                length += len(block)
                if length > maximum:
                    raise ValueError('Cloud backup exceeds its advertised size')
                output.write(block)
        if length != maximum:
            raise ValueError('Cloud backup is incomplete')
        result = restore_bundle(archive, destination, key)
        result.update(status='restored', filename=selected['name'], bytes=length, sha256=digest(archive))
    (destination / 'data/drive-recovery.json').write_text(json.dumps(result, indent=2) + '\n')
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', type=Path, default=Path('/opt/cnc/config.json'))
    parser.add_argument('--destination', type=Path, default=Path('/opt/internet-map'))
    parser.add_argument('--key', type=Path, default=Path('/opt/cnc/data/internet-map-recovery.key'))
    parser.add_argument('--install', action='store_true', help='Install authenticated recovered application, keeping scanning paused')
    args = parser.parse_args()
    if args.install and args.destination != Path('/opt/internet-map'):
        parser.error('Service installation requires /opt/internet-map; use an alternate destination only for data verification')
    with args.key.with_suffix('.lock').open('a') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            return
        result = recover(args.config, args.destination, args.key)
        if args.install and (args.destination / 'data/drive-recovery.json').is_file():
            install_application(args.destination)
            result['application_installed'] = True
        print(json.dumps(result))


def install_application(root):
    """Finish/retry installation only for a freshly authenticated restored tree."""
    marker = root / 'data/recovery-installed.json'
    if marker.exists():
        return
    # Recovery never silently resumes the old controller's queued activity.
    if not (root / 'data/discovery/paused').is_file():
        raise RuntimeError('Recovered discovery must remain paused before installation')
    dependency = subprocess.run(['/usr/bin/python3', '-c', 'import cryptography'], capture_output=True)
    if dependency.returncode:
        subprocess.run(['apt-get', 'install', '-y', 'python3-cryptography'], check=True)
    node = next(Path('/opt/cnc/node-runtime').glob('*/bin/node'))
    env = dict(os.environ, PATH=str(node.parent)+':'+os.environ.get('PATH','/usr/bin:/bin'), NEXT_TELEMETRY_DISABLED='1')
    subprocess.run([str(node.parent/'npm'), 'ci', '--no-audit', '--no-fund'], cwd=root/'web', env=env, check=True)
    subprocess.run([str(node.parent/'npm'), 'run', 'build'], cwd=root/'web', env=env, check=True)
    subprocess.run(['/usr/bin/python3', str(root/'discovery/install-wordpress-index.py'),
                    '--root', str(root/'data/discovery')], check=True)
    for installer in ('install-services.py', 'install-discovery.py'):
        subprocess.run(['/usr/bin/python3', str(root/'infra'/installer)], check=True)
    subprocess.run(['systemctl', 'restart', 'cnc-ui.service'], check=True)
    marker.write_text(json.dumps(dict(installed_at=int(time.time()), scanning_paused=True))+'\n')


if __name__ == '__main__':
    main()
