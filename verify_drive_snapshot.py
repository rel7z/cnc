"""Download an exact backup from Drive and restore it into an isolated database."""
import argparse
from concurrent.futures import ThreadPoolExecutor
import gzip
import hashlib
import json
from pathlib import Path
import sqlite3
import urllib.error
import urllib.parse
import urllib.request

from backup import restore_snapshot


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args):
        return None


def verify(manifest, destination, config_path=Path('/opt/cnc/config.json')):
    manifest, destination = Path(manifest), Path(destination)
    if destination.exists():
        raise ValueError('Verification destination must not exist')
    config = json.loads(Path(config_path).read_text())['gdrive']
    token_path = Path(config.get('token_path') or str(Path(config['watch_dir']) / '.gdrive_token.json'))
    token = json.loads(token_path.read_text())
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    # Refresh through the configured Google client without exporting credentials.
    body = urllib.parse.urlencode(dict(grant_type='refresh_token',
        refresh_token=token['refresh_token'], client_id=config['client_id'],
        client_secret=config['client_secret'])).encode()
    with opener.open(urllib.request.Request('https://oauth2.googleapis.com/token', data=body), timeout=30) as response:
        access = json.load(response)['access_token']
    headers = {'Authorization': 'Bearer ' + access}
    destination.mkdir(parents=True, mode=0o700)
    expected_manifest = manifest.read_bytes()
    receipt = json.loads(gzip.decompress(expected_manifest))

    def download(name, expected_sha, maximum):
        if Path(name).name != name or not name.startswith('internet-map-'):
            raise ValueError('Invalid backup filename')
        query = "trashed=false and name='" + name.replace("'", "\\'") + "'"
        parent = config.get('parent_folder_id')
        if parent:
            query += " and '" + parent.replace("'", "\\'") + "' in parents"
        url = 'https://www.googleapis.com/drive/v3/files?' + urllib.parse.urlencode({'q': query, 'fields': 'files(id,name)', 'pageSize': 100})
        with opener.open(urllib.request.Request(url, headers=headers), timeout=30) as response:
            files = json.load(response)['files']
        if len(files) != 1:
            raise ValueError('Expected exactly one uploaded file: ' + name)
        path = destination / name
        digest, length = hashlib.sha256(), 0
        url = 'https://www.googleapis.com/drive/v3/files/' + urllib.parse.quote(files[0]['id'], safe='') + '?alt=media'
        with opener.open(urllib.request.Request(url, headers=headers), timeout=120) as response, path.open('xb') as output:
            while block := response.read(1 << 20):
                length += len(block)
                if length > maximum:
                    raise ValueError('Downloaded backup exceeds expected size')
                digest.update(block); output.write(block)
        if digest.hexdigest() != expected_sha or length != maximum:
            raise ValueError('Drive backup checksum mismatch: ' + name)
        return path

    downloaded_manifest = download(manifest.name, hashlib.sha256(expected_manifest).hexdigest(), len(expected_manifest))
    def part(item):
        return download(item['filename'], item['sha256'], item['bytes'])
    with ThreadPoolExecutor(max_workers=3) as pool:
        list(pool.map(part, receipt['parts']))
    restored = destination / 'restored.db'
    result = restore_snapshot(downloaded_manifest, restored)
    db = sqlite3.connect(restored)
    try:
        tables = {row[0] for row in db.execute("SELECT name FROM sqlite_schema WHERE type='table'")}
        counts = {name: db.execute('SELECT count(*) FROM "' + name + '"').fetchone()[0]
                  for name in ('sites', 'observations', 'merged_hosts', 'ready_candidates', 'candidates') if name in tables}
    finally:
        db.close()
    result.update(database=receipt['database'], downloaded_parts=len(receipt['parts']),
                  drive_manifest_verified=True, restored_rows=counts)
    (destination / 'verification.json').write_text(json.dumps(result, indent=2) + '\n')
    return result


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('manifest', type=Path)
    parser.add_argument('destination', type=Path)
    args = parser.parse_args()
    print(json.dumps(verify(args.manifest, args.destination)))
