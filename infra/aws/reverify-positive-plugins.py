"""Controller-side, idempotent launch of the approved positive-target verification."""
import hashlib
import hmac
import json
from pathlib import Path
import time
import urllib.request

root = Path('/opt/cnc')
folder = root / 'plugin-positive-reverification/20261019'
config = json.loads((root / 'config.json').read_text())
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
base = 'http://' + config['http_addr']


def api(path):
    with opener.open(base + path, timeout=30) as response:
        return json.load(response)


pointer = folder / 'job.json'
if pointer.exists():
    saved = json.loads(pointer.read_text())
    print(json.dumps({'job_id': saved['job_id'], 'status': api('/api/jobs/' + saved['job_id'])['status'], 'new_launch': False}))
    raise SystemExit(0)
assert hashlib.sha256((root / 'tools/cnc-enum').read_bytes()).hexdigest() == 'c6b28f73208d3994d40de4a986aed781a2cf6f4de12df2aa6bd03b420e3c4418'
assert not any(j['status'] in ('running', 'pending') for j in api('/api/jobs'))
workers = api('/api/workers')
assert len(workers) == 11 and all(w['current_load'] == 0 and w['status'] == 'online' and w['max_tasks'] == (8 if w['id'].startswith('cnc-linode-') else 4) for w in workers)
input_path = folder / 'input.txt'
assert hashlib.sha256(input_path.read_bytes()).hexdigest() == '8b1c928d2ca0b85195aa583665555c2646e371fa071630ff56fb26f7d2cb758d'
assert len(input_path.read_text().splitlines()) == 2129
old_id = 'enum_1187ac70423421c4ba73c4eb'
old = api('/api/jobs/' + old_id)
assert old['status'] == 'completed'
directory = Path(config['data_dir']) / 'enum-jobs' / old_id
state = json.loads((directory / 'state.json').read_text())
artifact = json.loads((directory / state['artifact_filename']).read_text())
settings = old['enum']
payload = {'tool_id': 'enum', 'input_file': str(input_path), 'mode': 'spread',
           'timeout_seconds': old['timeout_seconds'], 'version_rules': artifact['rules'],
           'options': {'threads': settings['threads'], 'auto_concurrency': settings['automatic'], 'timeout': settings['request_timeout_ms'] // 1000}}
for name in settings.get('wordlists', []):
    assert name in ('wp_plugins.txt', 'joomla_exts.txt')
    payload[name.removesuffix('.txt')] = (directory / name).read_text()
marker = folder / 'submitted'
assert not marker.exists(), 'Prior launch outcome must be inspected before retry'
body = json.dumps(payload).encode()
actor = settings['actor']
env = dict(line.split('=', 1) for line in (root / 'dashboard-audit.env').read_text().splitlines() if '=' in line)
stamp = str(int(time.time()*1000))
signature = hmac.new(env['CNC_DASHBOARD_AUDIT_SECRET'].encode(), (actor+'\n'+stamp+'\nPOST\n/api/tools/launch\n'+hashlib.sha256(body).hexdigest()).encode(), hashlib.sha256).hexdigest()
request = urllib.request.Request(base + '/api/tools/launch', body, {'Content-Type': 'application/json',
                                'X-CNC-Actor': actor, 'X-CNC-Audit-Time': stamp, 'X-CNC-Audit-Signature': signature})
marker.write_text(stamp + '\n')
with opener.open(request, timeout=600) as response:
    result = json.load(response)
report = {'job_id': result['job_id'], 'input_targets': 2129, 'same_rules': len(artifact['rules']), 'source_completed_scan': old_id,
          'rules_sha256': hashlib.sha256(json.dumps(artifact['rules'], sort_keys=True).encode()).hexdigest()}
pointer.write_text(json.dumps(report, indent=2)+'\n')
print(json.dumps(report))
