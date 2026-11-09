"""Controller-only: launch one enum rescan of preserved CMS lists with the old rules."""
import hashlib
import hmac
import json
from pathlib import Path
import time
import urllib.request

root = Path('/opt/cnc')
config = json.loads((root / 'config.json').read_text())
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
base = 'http://' + config['http_addr']


def api(path):
    with opener.open(base + path, timeout=30) as response:
        return json.load(response)


release = root / 'plugin-verification-releases/94443abd84698108'
assert hashlib.sha256((root / 'tools/cnc-enum').read_bytes()).hexdigest() == 'bab0fada09149b43d4f38785ecc4b717d544169bd5b3e40284965d3f92839ee0'
pointer = release / 'rescan-job.json'
if pointer.exists():
    saved = json.loads(pointer.read_text())
    existing = api('/api/jobs/' + saved['job_id'])
    print(json.dumps({'existing_job': saved['job_id'], 'status': existing['status'], 'duplicate_launch': False}))
    raise SystemExit(0)
workers = api('/api/workers')
assert len(workers) == 11
assert all(w['status'] == 'online' and w['current_load'] == 0 and
           w['max_tasks'] == (8 if w['id'].startswith('cnc-linode-') else 4) for w in workers), 'Worker rollout incomplete or tasks already active'
assert not any(j['status'] in ('running', 'pending') for j in api('/api/jobs'))
old_id = 'enum_20fcfe31e2d3a79de3472793'
old = api('/api/jobs/' + old_id)
assert old['status'] == 'cancelled'
directory = Path(config['data_dir']) / 'enum-jobs' / old_id
state = json.loads((directory / 'state.json').read_text())
artifact = json.loads((directory / state['artifact_filename']).read_text())
watch = Path(config['gdrive']['watch_dir']).expanduser()
targets = {}
counts = {}
for name in ('wordpress.txt', 'joomla.txt'):
    lines = [line.strip() for line in (watch / name).read_text().splitlines() if line.strip()]
    counts[name] = len(lines)
    for target in lines:
        targets.setdefault(target, None)
assert targets
input_path = release / 'wordpress-joomla-reverification.txt'
input_path.write_text('\n'.join(targets) + '\n')
input_path.chmod(0o600)
settings = old['enum']
payload = {'tool_id': 'enum', 'input_file': str(input_path), 'mode': 'spread',
           'timeout_seconds': old['timeout_seconds'], 'version_rules': artifact['rules'],
           'options': {'threads': settings['threads'], 'auto_concurrency': settings['automatic'],
                       'timeout': settings['request_timeout_ms'] // 1000}}
for name in settings.get('wordlists', []):
    assert name in ('wp_plugins.txt', 'joomla_exts.txt')
    payload[name.removesuffix('.txt')] = (directory / name).read_text()
marker = release / 'rescan-submitted'
assert not marker.exists(), 'Prior submission outcome must be inspected before any retry'
body = json.dumps(payload).encode()
actor = settings['actor']
env = dict(line.split('=', 1) for line in (root / 'dashboard-audit.env').read_text().splitlines() if '=' in line)
secret = env['CNC_DASHBOARD_AUDIT_SECRET']
stamp = str(int(time.time()*1000))
signature = hmac.new(secret.encode(), (actor+'\n'+stamp+'\nPOST\n/api/tools/launch\n'+hashlib.sha256(body).hexdigest()).encode(), hashlib.sha256).hexdigest()
request = urllib.request.Request(base+'/api/tools/launch', body, {'Content-Type': 'application/json',
                                 'X-CNC-Actor': actor, 'X-CNC-Audit-Time': stamp, 'X-CNC-Audit-Signature': signature})
marker.write_text(stamp+'\n')
with opener.open(request, timeout=600) as response:
    created = json.load(response)
report = {'job_id': created['job_id'], 'old_job': old_id, 'cms_input_counts': counts, 'unique_targets': len(targets),
          'rules': len(artifact['rules']), 'same_rules_sha256': hashlib.sha256(json.dumps(artifact['rules'], sort_keys=True).encode()).hexdigest(),
          'old_job_retained_cancelled': True}
pointer.write_text(json.dumps(report, indent=2)+'\n')
print(json.dumps(report))
