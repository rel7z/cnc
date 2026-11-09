"""Narrow, paused core rollout. Invoked with validated artifact metadata."""
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

app = Path('/opt/cnc-processor')
release = app / 'scheduler-releases' / str(time.time_ns())
os.umask(0o077)
release.mkdir(parents=True, mode=0o700)

def digest(path):
    h = hashlib.sha256()
    with Path(path).open('rb') as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b''):
            h.update(chunk)
    return h.hexdigest()

def snapshot():
    with sqlite3.connect('file:' + str(app / 'processor.db') + '?mode=ro', uri=True) as db:
        return {
            'enabled': db.execute('SELECT enabled FROM system_state WHERE id=1').fetchone()[0],
            'counts': db.execute('SELECT status,COUNT(*) FROM runs GROUP BY status ORDER BY status').fetchall(),
            'active': db.execute("SELECT COUNT(*) FROM processor_attempts WHERE phase IN ('assigned','running','uploading')").fetchone()[0],
            'versions': db.execute('SELECT program_id,version FROM processor_programs ORDER BY program_id').fetchall(),
        }

before = snapshot()
if before['enabled'] or before['active']:
    raise RuntimeError('Rollout requires an already paused processor with no active attempts')
config_hash = digest(app / 'config.json')
old_hash = digest(app / 'cnc-processor')
for name, spec in ARTIFACTS.items():
    if name not in ('cnc-processor', 'dummy-tests'):
        raise RuntimeError('Unexpected core artifact')
    target = release / name
    with urllib.request.urlopen(spec['url'], timeout=90) as source, target.open('xb') as dest:
        shutil.copyfileobj(source, dest)
    if digest(target) != spec['sha256']:
        raise RuntimeError('Artifact checksum mismatch')
    target.chmod(0o700)

smoke = subprocess.run([str(release / 'dummy-tests'),
    '-test.run=^Test(Isolated|BrokerRefusal|WorkerRejection|RankedAdmission)', '-test.v'],
    capture_output=True, text=True, timeout=60)
(release / 'dummy-tests.log').write_text(smoke.stdout + smoke.stderr)
if smoke.returncode:
    raise RuntimeError('Isolated Linux dummy tests failed; production binary unchanged')

shutil.copy2(app / 'cnc-processor', release / 'previous-cnc-processor')
shutil.copy2(app / 'config.json', release / 'config-backup.json')
subprocess.run(['systemctl', 'stop', 'cnc-processor'], check=True, timeout=65)
try:
    if snapshot() != before or digest(app / 'config.json') != config_hash:
        raise RuntimeError('State changed while preparing rollout')
    with sqlite3.connect(str(app / 'processor.db')) as source, sqlite3.connect(str(release / 'processor-backup.db')) as dest:
        source.backup(dest)
    shutil.copy2(release / 'cnc-processor', app / 'cnc-processor.next')
    os.replace(app / 'cnc-processor.next', app / 'cnc-processor')
    subprocess.run(['systemctl', 'start', 'cnc-processor'], check=True, timeout=35)
    env = {}
    for line in (Path('/opt/cnc') / 'processor.env').read_text().splitlines():
        if '=' in line and not line.startswith('#'):
            key, value = line.split('=', 1)
            env[key] = value.strip().strip('"').strip("'")
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    health = None
    for _ in range(30):
        try:
            request = urllib.request.Request('http://127.0.0.1:9191/v1/health',
                headers={'Authorization': 'Bearer ' + env['CNC_PROCESSOR_TOKEN']})
            with opener.open(request, timeout=2) as response:
                health = json.load(response)
            break
        except Exception:
            time.sleep(1)
    if not health or health.get('enabled') or health.get('status') != 'ok':
        raise RuntimeError('New core health verification failed')
    after = snapshot()
    if before != after or digest(app / 'config.json') != config_hash:
        raise RuntimeError('Queue, program versions or configuration changed')
    if digest(app / 'cnc-processor') != ARTIFACTS['cnc-processor']['sha256']:
        raise RuntimeError('Installed binary hash mismatch')
    subprocess.run(['systemctl', 'is-active', '--quiet', 'cnc-processor'], check=True)
except BaseException:
    subprocess.run(['systemctl', 'stop', 'cnc-processor'], timeout=65)
    # Startup may have registered a bundle or applied schema migrations. Restore
    # the database only when no external work or processing-state change occurred.
    backup = release / 'processor-backup.db'
    current = snapshot()
    if backup.exists() and not current['enabled'] and not current['active'] and current['counts'] == before['counts']:
        with sqlite3.connect(str(backup)) as source, sqlite3.connect(str(app / 'processor.db')) as dest:
            source.backup(dest)
    shutil.copy2(release / 'previous-cnc-processor', app / 'cnc-processor.restore')
    os.replace(app / 'cnc-processor.restore', app / 'cnc-processor')
    subprocess.run(['systemctl', 'start', 'cnc-processor'], timeout=35)
    raise

receipt = {
    'release': str(release), 'old_binary_sha256': old_hash,
    'binary_sha256': digest(app / 'cnc-processor'), 'config_sha256': config_hash,
    'paused': True, 'counts': after['counts'], 'active_attempts': after['active'],
    'queue_and_program_versions_preserved': True, 'linux_dummy_tests_passed': True,
    'service': 'active', 'rollback': str(release / 'previous-cnc-processor'),
}
(release / 'receipt.json').write_text(json.dumps(receipt, indent=2) + '\n')
print(json.dumps(receipt))
