"""Core-only CPU policy rollout; preserves the operator's processing switch."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import sqlite3
import subprocess
import time
import urllib.request

ARTIFACTS = {}  # supplied by the local release driver
POLICY = dict(enabled=True, pause_cpu_percent=75, emergency_cpu_percent=90,
              recover_cpu_percent=85, cooldown_seconds=10)
app = Path('/opt/cnc-processor')
os.umask(0o077)
release = app / 'balance-releases' / str(time.time_ns())
release.mkdir(parents=True, mode=0o700)

def digest(path):
    h = hashlib.sha256()
    with Path(path).open('rb') as f:
        for block in iter(lambda: f.read(1024 * 1024), b''):
            h.update(block)
    return h.hexdigest()

def snapshot():
    with sqlite3.connect('file:' + str(app / 'processor.db') + '?mode=ro', uri=True) as db:
        return dict(enabled=db.execute('SELECT enabled FROM system_state WHERE id=1').fetchone()[0],
                    versions=db.execute('SELECT program_id,version FROM processor_programs ORDER BY program_id').fetchall(),
                    attempts=db.execute("SELECT id,phase,lease_until FROM processor_attempts WHERE phase IN ('assigned','running','uploading')").fetchall())

for name, spec in ARTIFACTS.items():
    if name not in ('cnc-processor', 'dummy-tests'):
        raise RuntimeError('Unexpected artifact')
    target = release / name
    with urllib.request.urlopen(spec['url'], timeout=90) as source, target.open('xb') as dest:
        shutil.copyfileobj(source, dest)
    if digest(target) != spec['sha256']:
        raise RuntimeError('Artifact checksum mismatch')
    target.chmod(0o700)
smoke = subprocess.run([str(release / 'dummy-tests'),
    '-test.run=^Test(AutoBalance|Isolated|BrokerRefusal|WorkerRejection|RankedAdmission)', '-test.v'],
    capture_output=True, text=True, timeout=60)
(release / 'dummy-tests.log').write_text(smoke.stdout + smoke.stderr)
if smoke.returncode:
    raise RuntimeError('Linux fixture tests failed; service unchanged')

original = (app / 'config.json').read_bytes()
config = json.loads(original)
updated = dict(config, auto_balance=POLICY)
shutil.copy2(app / 'config.json', release / 'config-backup.json')
shutil.copy2(app / 'cnc-processor', release / 'previous-cnc-processor')
with sqlite3.connect(str(app / 'processor.db')) as source, sqlite3.connect(str(release / 'processor-backup.db')) as dest:
    source.backup(dest)
shutil.copy2(release / 'cnc-processor', app / 'cnc-processor.next')
(app / 'config.json.next').write_text(json.dumps(updated, indent=2) + '\n')

env = {}
for line in Path('/opt/cnc/processor.env').read_text().splitlines():
    if '=' in line and not line.startswith('#'):
        key, value = line.split('=', 1)
        env[key] = value.strip().strip('"').strip("'")
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
def api(path):
    request = urllib.request.Request('http://127.0.0.1:9191/v1/' + path,
        headers={'Authorization': 'Bearer ' + env['CNC_PROCESSOR_TOKEN']})
    with opener.open(request, timeout=2) as response:
        return json.load(response)

# Stage and test everything first. Renewals happen every five seconds; leave
# sufficient lease time for a bounded core-only restart. Remote workers stay up.
deadline = time.monotonic() + 30
while True:
    before = snapshot()
    minimum = min((row[2] / 1000 - time.time() for row in before['attempts']), default=30)
    if minimum >= 22:
        break
    if time.monotonic() >= deadline:
        raise RuntimeError('Could not obtain a safe renewal window; service unchanged')
    time.sleep(0.25)
if (app / 'config.json').read_bytes() != original:
    raise RuntimeError('Configuration changed during staging')
started = time.monotonic()
try:
    subprocess.run(['systemctl', 'stop', 'cnc-processor'], check=True, timeout=8)
    os.replace(app / 'config.json.next', app / 'config.json')
    os.replace(app / 'cnc-processor.next', app / 'cnc-processor')
    subprocess.run(['systemctl', 'start', 'cnc-processor'], check=True, timeout=5)
    health = cluster = None
    while time.monotonic() - started < 17:
        try:
            health = api('health')
            cluster = api('cluster')
            if health.get('status') == 'ok' and cluster.get('auto_balance') == POLICY:
                break
        except Exception:
            pass
        time.sleep(0.2)
    else:
        raise RuntimeError('New core did not become healthy inside the lease window')
    after = snapshot()
    if after['enabled'] != before['enabled'] or after['versions'] != before['versions']:
        raise RuntimeError('Processing switch or registered program versions changed')
    actual = json.loads((app / 'config.json').read_text())
    expected = dict(updated)
    if actual != expected:
        raise RuntimeError('Configuration verification failed')
    pid = subprocess.check_output(['systemctl', 'show', 'cnc-processor', '--property=MainPID', '--value'], text=True).strip()
    if digest('/proc/' + pid + '/exe') != ARTIFACTS['cnc-processor']['sha256']:
        raise RuntimeError('Running executable hash mismatch')
    with sqlite3.connect('file:' + str(app / 'processor.db') + '?mode=ro', uri=True) as db:
        # Legitimate completions may occur during restart. A healthy renewal
        # must not turn any pre-existing attempt into an interrupted one.
        phases = [db.execute('SELECT phase,error FROM processor_attempts WHERE id=?', (row[0],)).fetchone()
                  for row in before['attempts']]
        shed = sum(phase == 'interrupted' and reason.startswith('CPU protection:') for phase, reason in phases)
        lost = sum(phase == 'interrupted' and not reason.startswith('CPU protection:') for phase, reason in phases)
    if lost:
        raise RuntimeError('Existing attempt interrupted during rollout')
except BaseException:
    subprocess.run(['systemctl', 'stop', 'cnc-processor'], timeout=8)
    shutil.copy2(release / 'previous-cnc-processor', app / 'cnc-processor.restore')
    os.replace(app / 'cnc-processor.restore', app / 'cnc-processor')
    shutil.copy2(release / 'config-backup.json', app / 'config.json')
    subprocess.run(['systemctl', 'start', 'cnc-processor'], timeout=5)
    # Never restore the database over live workers' legitimate progress.
    raise

receipt = dict(release=str(release), binary_sha256=digest(app / 'cnc-processor'),
               policy=POLICY, processing_enabled=bool(after['enabled']),
               existing_attempts_checked=len(before['attempts']),
               unexpected_interruptions=lost, cpu_policy_interruptions=shed,
               restart_seconds=round(time.monotonic() - started, 3),
               program_versions_preserved=True, only_auto_balance_config_changed=True,
               linux_fixture_tests_passed=True, controller_and_workers_restarted=False,
               rollback=str(release / 'previous-cnc-processor'))
(release / 'receipt.json').write_text(json.dumps(receipt, indent=2) + '\n')
print(json.dumps(receipt))
