"""Run on controller: plan by default; explicit --apply moves only enum component outputs.

Local copies remain in a private rollback directory. Matching files in the
configured Drive folder are trashed, never permanently deleted. CMS lists stay.
"""
import hashlib
import json
from pathlib import Path
import re
import sys
import time
import urllib.parse
import urllib.request

root = Path('/opt/cnc')
config = json.loads((root / 'config.json').read_text())
drive = config['gdrive']
watch = Path(drive['watch_dir']).expanduser()
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
base = 'http://' + config['http_addr']
with opener.open(base + '/api/jobs', timeout=30) as response:
    jobs = json.load(response)
assert not any(j['status'] in ('running', 'pending') for j in jobs), 'Reset requires idle jobs'
names = {'plugins_name.txt'}
prefix = {'component': 'com_', 'module': 'mod_', 'library': 'lib_', 'template': 'tpl_', 'package': 'pkg_'}
for job in jobs:
    if not job.get('enum'):
        continue
    folder = Path(config['data_dir']) / 'enum-jobs' / job['id']
    state = json.loads((folder / 'state.json').read_text())
    artifact = json.loads((folder / state['artifact_filename']).read_text())
    for rule in artifact['rules']:
        parts = rule['component'].split(':')
        if parts[-1] == 'core':
            continue
        if parts[:2] == ['wordpress', 'plugin'] and len(parts) == 3:
            names.add(parts[2] + '.txt')
        elif parts[:2] == ['joomla', 'plugin'] and len(parts) == 4:
            names.add('plugin:' + parts[2] + ':' + parts[3] + '.txt')
        elif parts[0] == 'joomla' and len(parts) == 3 and parts[1] in prefix:
            names.add(prefix[parts[1]] + parts[2] + '.txt')
preserved = {name: hashlib.sha256((watch / name).read_bytes()).hexdigest()
             for name in ('wordpress.txt', 'joomla.txt')}
assert not {'wordpress.txt', 'joomla.txt', 'admin.txt', 'cpanel.txt'} & names
assert all(re.fullmatch(r'[a-z0-9_:.-]+\.txt', name) for name in names)
local = [watch / name for name in sorted(names) if (watch / name).exists()]
assert all(path.is_file() and not path.is_symlink() for path in local)
expected = next((int(arg.split('=', 1)[1]) for arg in sys.argv if arg.startswith('--expected-count=')), None)
assert expected is None or len(local) == expected, 'Result count changed; inspect before reset'
if '--apply' not in sys.argv:
    print(json.dumps({'plan_only': True, 'local_files': [p.name for p in local], 'count': len(local),
                      'bytes': sum(p.stat().st_size for p in local), 'cms_hashes': preserved}))
    raise SystemExit(0)
token = json.loads(Path(drive['token_path']).expanduser().read_text())
body = urllib.parse.urlencode({'client_id': drive['client_id'], 'client_secret': drive['client_secret'],
                              'refresh_token': token['refresh_token'], 'grant_type': 'refresh_token'}).encode()
with opener.open(urllib.request.Request('https://oauth2.googleapis.com/token', body), timeout=30) as response:
    access = json.load(response)['access_token']


def google(path, body=None, method=None):
    data = None if body is None else json.dumps(body).encode()
    request = urllib.request.Request('https://www.googleapis.com/drive/v3/' + path, data,
                                     {'Authorization': 'Bearer ' + access, 'Content-Type': 'application/json'}, method=method)
    with opener.open(request, timeout=30) as response:
        return json.load(response)


def remote_files():
    parent = drive.get('parent_folder_id') or 'root'
    query = "trashed = false and '" + parent.replace("'", "\\'") + "' in parents"
    found, page = [], None
    while True:
        params = {'q': query, 'fields': 'nextPageToken,files(id,name)', 'pageSize': 1000,
                  'supportsAllDrives': 'true', 'includeItemsFromAllDrives': 'true'}
        if page:
            params['pageToken'] = page
        result = google('files?' + urllib.parse.urlencode(params))
        found.extend(f for f in result.get('files', []) if f['name'] in names)
        page = result.get('nextPageToken')
        if not page:
            return found


remote = remote_files()  # Verify Drive access before moving any local file.
backup = root / 'plugin-result-reset' / str(time.time_ns())
backup.mkdir(parents=True, mode=0o700)
report = {'local_files': [], 'drive_files': remote, 'cms_hashes': preserved}
for path in local:
    report['local_files'].append({'name': path.name, 'sha256': hashlib.sha256(path.read_bytes()).hexdigest()})
    path.rename(backup / path.name)
(backup / 'reset.json').write_text(json.dumps(report, indent=2))
for item in remote:
    google('files/' + item['id'] + '?supportsAllDrives=true', {'trashed': True}, method='PATCH')
assert not remote_files(), 'Plugin files remain in Drive'
assert all(not (watch / name).exists() for name in names)
assert preserved == {name: hashlib.sha256((watch / name).read_bytes()).hexdigest() for name in preserved}
print(json.dumps({'local_removed': len(local), 'drive_trashed': len(remote), 'backup': str(backup),
                  'cms_preserved': True, 'cms_hashes': preserved, 'remaining_plugin_files': 0}))
