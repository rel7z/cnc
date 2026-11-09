"""Install the generic spread/summary core without reopening private tools."""
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import sqlite3
import subprocess
import time
import urllib.request

ARTIFACTS = {}  # supplied by the release driver
EXPECTED_OLD = '0ff0d26a5d60857772bc2fcf4132d3150a1a4c752791737d1d4a97317d28f96e'
INPUT = 'wordpress.txt'
POLICY = {'distribution': 'spread', 'require_summary': True}
RESUME_DISPATCH = True  # owner requested the queued parsing work to run
app = Path('/opt/cnc-processor')
env_path = Path('/opt/cnc/processor.env')
os.umask(0o077)
release = app / 'spread-releases' / str(time.time_ns())
release.mkdir(parents=True, mode=0o700)

def digest(path):
    h = hashlib.sha256()
    with Path(path).open('rb') as f:
        for block in iter(lambda: f.read(1024 * 1024), b''):
            h.update(block)
    return h.hexdigest()

def environment(data):
    result = {}
    for line in data.decode().splitlines():
        if '=' in line and not line.startswith('#'):
            key, value = line.split('=', 1)
            result[key] = value.strip().strip('"').strip("'")
    return result

env = environment(env_path.read_bytes())
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
def api(path, data=None):
    req = urllib.request.Request('http://127.0.0.1:9191/v1/' + path,
        data=json.dumps(data).encode() if data is not None else None,
        method='PUT' if data is not None else 'GET',
        headers={'Authorization': 'Bearer ' + env['CNC_PROCESSOR_TOKEN'], 'Content-Type': 'application/json'})
    with opener.open(req, timeout=3) as response:
        return json.load(response)

def snapshot():
    with sqlite3.connect('file:' + str(app / 'processor.db') + '?mode=ro', uri=True) as db:
        return dict(enabled=bool(db.execute('SELECT enabled FROM system_state WHERE id=1').fetchone()[0]),
            versions=db.execute('SELECT program_id,version FROM processor_programs ORDER BY program_id').fetchall(),
            attempts=db.execute("SELECT id,phase,lease_until FROM processor_attempts WHERE phase IN ('assigned','running','uploading')").fetchall())

if digest(app / 'cnc-processor') != EXPECTED_OLD:
    raise RuntimeError('Existing core changed; refusing to overwrite an unreviewed release')
for name, spec in ARTIFACTS.items():
    if name not in ('cnc-processor', 'distributed-tests', 'store-tests', 'watcher-tests'):
        raise RuntimeError('Unexpected core artifact')
    target = release / name
    with urllib.request.urlopen(spec['url'], timeout=90) as source, target.open('xb') as dest:
        shutil.copyfileobj(source, dest)
    if digest(target) != spec['sha256']:
        raise RuntimeError('Core artifact checksum mismatch')
    target.chmod(0o700)

patterns = {
    'distributed-tests': '^Test(SpreadBackgroundSummaryEndToEnd|CompletionRequiresSavedMatchingSummary|CPUHoldDoesNotInterruptBackgroundWork|ExplicitEditorApplyRefreshesRetainedSelectedBundle|RetainedConnectedProgramsStartWithoutReopeningSources|StagedPublicationSurvivesProcessorRestart)$',
    'store-tests': '^Test(SpreadAtomicCursorAndWorkerAffinity|RebalancePendingSharesPreservesActiveInputVersionAndCursor)$',
    'watcher-tests': '^TestSpread',
}
for name, pattern in patterns.items():
    r = subprocess.run([str(release / name), '-test.run=' + pattern, '-test.v'], capture_output=True, text=True, timeout=45)
    (release / (name + '.log')).write_text(r.stdout + r.stderr)
    if r.returncode:
        raise RuntimeError('Linux dummy tests failed; production remains unchanged')

original_config = (app / 'config.json').read_bytes()
original_env = env_path.read_bytes()
config = json.loads(original_config)
if INPUT not in config.get('routes', {}):
    raise RuntimeError('Requested input route is absent')
new_config = json.loads(original_config)
new_config.setdefault('file_processing', {})[INPUT] = POLICY
new_config.setdefault('auto_balance', {})['interrupt_running'] = False
initial = snapshot()
ids = [row[0] for row in initial['versions']]
if not ids or any(not re.fullmatch(r'[A-Za-z0-9_-]+', value) for value in ids):
    raise RuntimeError('Unexpected program identity')
# Retain existing registered versions: core-only startup must not open a tool.
lines = [line for line in original_env.decode().splitlines() if not line.startswith('CNC_PROCESSOR_PINNED_PROGRAMS=')]
lines.append('CNC_PROCESSOR_PINNED_PROGRAMS=' + ','.join(ids))
new_env = ('\n'.join(lines) + '\n').encode()
shutil.copy2(app / 'cnc-processor', release / 'previous-cnc-processor')
(release / 'config-backup.json').write_bytes(original_config)
(release / 'processor-env-backup').write_bytes(original_env)
with sqlite3.connect(str(app / 'processor.db')) as source, sqlite3.connect(str(release / 'processor-backup.db')) as dest:
    source.backup(dest)

# Validate retained connected-program settings on a disposable metadata copy.
# No broker, watcher, program runner, or network service is started here.
shutil.copy2(release / 'processor-backup.db', release / 'preflight.db')
check_env = dict(os.environ, CNC_APP_DIR=str(app), CNC_PROCESSOR_DB=str(release / 'preflight.db'),
    CNC_PROCESSOR_PINNED_PROGRAMS=','.join(ids))
check = subprocess.run([str(release / 'cnc-processor'), 'validate-retained'], env=check_env,
    capture_output=True, text=True, timeout=15)
(release / 'retained-preflight.log').write_text(check.stdout + check.stderr)
if check.returncode:
    raise RuntimeError('Retained metadata validation failed; live core unchanged')

api('processing-state', {'enabled': False})
time.sleep(2)
env_next = env_path.with_name('processor.env.next')
try:
    shutil.copy2(release / 'cnc-processor', app / 'cnc-processor.next')
    (app / 'config.json.next').write_text(json.dumps(new_config, indent=2) + '\n')
    env_next.write_bytes(new_env)
    env_next.chmod(0o600)
except BaseException:
    api('processing-state', {'enabled': initial['enabled']})
    raise

deadline = time.monotonic() + 30
while True:
    before = snapshot()
    minimum = min((row[2] / 1000 - time.time() for row in before['attempts']), default=30)
    if minimum >= 22:
        break
    if time.monotonic() >= deadline:
        api('processing-state', {'enabled': initial['enabled']})
        raise RuntimeError('No safe lease renewal window; core unchanged')
    time.sleep(0.25)
if (app / 'config.json').read_bytes() != original_config or env_path.read_bytes() != original_env:
    api('processing-state', {'enabled': initial['enabled']})
    raise RuntimeError('Configuration changed while staging')

started = time.monotonic()
try:
    subprocess.run(['systemctl', 'stop', 'cnc-processor'], check=True, timeout=8)
    os.replace(app / 'config.json.next', app / 'config.json')
    os.replace(env_next, env_path)
    os.replace(app / 'cnc-processor.next', app / 'cnc-processor')
    subprocess.run(['systemctl', 'start', 'cnc-processor'], check=True, timeout=5)
    while time.monotonic() - started < 17:
        try:
            health, status = api('health'), api('cluster')
            if (health.get('status') == 'ok' and not health.get('enabled') and
                    status.get('file_processing', {}).get(INPUT) == POLICY and
                    status.get('auto_balance', {}).get('interrupt_running') is False):
                break
        except Exception:
            pass
        time.sleep(0.2)
    else:
        raise RuntimeError('New core did not become healthy within its lease window')
    pid = subprocess.check_output(['systemctl', 'show', 'cnc-processor', '--property=MainPID', '--value'], text=True).strip()
    if digest('/proc/' + pid + '/exe') != ARTIFACTS['cnc-processor']['sha256']:
        raise RuntimeError('Running core hash mismatch')
    after = snapshot()
    if after['versions'] != before['versions'] or after['enabled']:
        raise RuntimeError('Registered program versions or processing switch changed')
    with sqlite3.connect('file:' + str(app / 'processor.db') + '?mode=ro', uri=True) as db:
        lost = sum(db.execute('SELECT phase FROM processor_attempts WHERE id=?', (row[0],)).fetchone()[0] == 'interrupted' for row in before['attempts'])
    if lost:
        raise RuntimeError('Existing attempt interrupted during core restart')
except BaseException:
    subprocess.run(['systemctl', 'stop', 'cnc-processor'], timeout=8)
    shutil.copy2(release / 'previous-cnc-processor', app / 'cnc-processor.restore')
    os.replace(app / 'cnc-processor.restore', app / 'cnc-processor')
    (app / 'config.json').write_bytes(original_config)
    env_path.write_bytes(original_env)
    subprocess.run(['systemctl', 'start', 'cnc-processor'], timeout=5)
    # Do not restore the database over legitimate progress from live workers.
    api('processing-state', {'enabled': initial['enabled']})
    raise
restart_seconds = round(time.monotonic() - started, 3)
(release / 'rollout-state.json').write_text(json.dumps(dict(initial=initial, before=before,
    restart_seconds=restart_seconds, binary_sha256=ARTIFACTS['cnc-processor']['sha256'])) + '\n')

# Repartitioning is a separate atomic maintenance step. A failure leaves the
# healthy new core paused, with original queued input intact and active work up.
deadline = time.monotonic() + 20
workers = []
while time.monotonic() < deadline:
    status = api('cluster')
    workers = sorted(w['id'] for w in (status.get('workers') or [])
        if w.get('status') in ('online', 'busy') and w.get('processor_protocol') == 'processor-v2'
        and w.get('max_tasks', 0) > 0 and time.time() * 1000 - (w.get('resources') or {}).get('sampled_at', 0) <= 15000)
    if workers:
        break
    time.sleep(0.5)
if not workers:
    raise RuntimeError('No fresh worker snapshot; new core remains paused')

def input_fingerprint(preferred):
    with sqlite3.connect('file:' + str(app / 'processor.db') + '?mode=ro', uri=True) as db:
        query = "SELECT b.records_json FROM runs r JOIN batches b ON b.acceptance_id=r.batch_acceptance_id LEFT JOIN processor_batch_workers w ON w.acceptance_id=b.acceptance_id WHERE b.filename=? AND r.status='queued' AND "
        query += "w.worker_id IS NOT NULL" if preferred else "w.worker_id IS NULL AND NOT EXISTS(SELECT 1 FROM processor_attempts a WHERE a.run_id=r.id AND a.phase!='rejected')"
        records = [line for (body,) in db.execute(query, (INPUT,)) for line in json.loads(body)]
    h = hashlib.sha256()
    for line in sorted(records):
        data = line.encode(); h.update(len(data).to_bytes(8, 'big')); h.update(data)
    return len(records), h.hexdigest()

pending_before = input_fingerprint(False)
maintenance_env = dict(os.environ, CNC_PROCESSOR_DB=str(app / 'processor.db'))
r = subprocess.run([str(app / 'cnc-processor'), 'rebalance-queued', INPUT, ','.join(workers)], cwd=app, env=maintenance_env, capture_output=True, text=True, timeout=15)
(release / 'rebalance.log').write_text(r.stdout + r.stderr)
if r.returncode:
    raise RuntimeError('Repartition failed; inspect private release log. Core remains paused.')
rebalance = json.loads(r.stdout)
pending_after = input_fingerprint(True)
if pending_before != pending_after or rebalance['records'] != pending_after[0]:
    raise RuntimeError('Queued input fingerprint mismatch; dispatch remains paused')
with sqlite3.connect('file:' + str(app / 'processor.db') + '?mode=ro', uri=True) as db:
    shares = db.execute("SELECT w.worker_id,SUM(json_array_length(b.records_json)),COUNT(*) FROM runs r JOIN batches b ON b.acceptance_id=r.batch_acceptance_id JOIN processor_batch_workers w ON w.acceptance_id=b.acceptance_id WHERE b.filename=? AND r.status='queued' GROUP BY w.worker_id ORDER BY w.worker_id", (INPUT,)).fetchall()
counts = {worker: 0 for worker in workers}
counts.update({row[0]: row[1] for row in shares})
if counts and max(counts.values()) - min(counts.values()) > 1:
    raise RuntimeError('Shares are uneven; dispatch remains paused')
api('processing-state', {'enabled': RESUME_DISPATCH})
receipt = dict(release=str(release), binary_sha256=digest(app / 'cnc-processor'),
    file_processing=POLICY, interrupt_running=False, worker_ids=workers, queued_shares=shares,
    queued_records_preserved=pending_after[0], queued_records_sha256=pending_after[1],
    repartition=rebalance, running_attempts_preserved=len(before['attempts']),
    restart_seconds=restart_seconds, registered_versions_preserved=True,
    processing_enabled=api('health')['enabled'], linux_dummy_tests_passed=True,
    processing_was_enabled=initial['enabled'],
    controller_and_workers_restarted=False, private_tools_accessed=False)
(release / 'receipt.json').write_text(json.dumps(receipt, indent=2) + '\n')
print(json.dumps(receipt))
