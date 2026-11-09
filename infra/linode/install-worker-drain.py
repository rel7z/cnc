"""Install core worker restart protection; never inspect or execute tool files.

Run on an existing worker as root with the expected core-binary SHA256 argument.
The asynchronous restart may remain pending until legacy active tasks finish.
"""
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import time


def command(*args):
    return subprocess.check_output(args, text=True).strip()


source = Path('/opt/cnc/cnc-worker-drain-fix.staged')
expected = sys.argv[1]
if hashlib.sha256(source.read_bytes()).hexdigest() != expected:
    raise SystemExit('Core worker artifact checksum mismatch')
if json.loads(command(str(source), '-drain-support')) != {'drain_stop': True}:
    raise SystemExit('Artifact does not support safe shutdown')

stamp = time.strftime('%Y%m%dT%H%M%SZ', time.gmtime())
backup = Path('/opt/cnc/restart-rollback') / (stamp + '-' + str(os.getpid()))
backup.mkdir(parents=True, mode=0o700)
dropin = Path('/etc/systemd/system/cnc-worker.service.d/zzzz-drain-safety.conf')
targets = [Path('/opt/cnc/cnc-worker'), Path('/opt/cnc/worker-stop-guard'), dropin]
manifest = {'sha256': expected, 'files': [], 'previous_pid': command('systemctl', 'show', 'cnc-worker', '-p', 'MainPID', '--value')}
for index, path in enumerate(targets):
    entry = {'path': str(path), 'existed': path.exists()}
    if path.exists():
        saved = backup / str(index)
        shutil.copy2(path, saved)
        entry['backup'] = str(saved)
    manifest['files'].append(entry)
(backup / 'manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')

for target in targets[:2]:
    temp = target.with_name(target.name + '.drain-new')
    shutil.copyfile(source, temp)
    temp.chmod(0o700)
    os.replace(temp, target)
dropin.parent.mkdir(parents=True, exist_ok=True)
temp = dropin.with_suffix('.new')
temp.write_text('''[Service]
ExecStop=
ExecStop=/opt/cnc/worker-stop-guard -config=/opt/cnc/config.json -drain-stop $MAINPID
TimeoutStopSec=infinity
KillMode=mixed
KillSignal=SIGTERM
''')
os.replace(temp, dropin)
command('systemctl', 'daemon-reload')
properties = command('systemctl', 'show', 'cnc-worker', '-p', 'ExecStop', '-p', 'TimeoutStopUSec', '-p', 'KillMode', '-p', 'KillSignal')
if 'TimeoutStopUSec=infinity' not in properties or 'KillMode=mixed' not in properties or '/opt/cnc/worker-stop-guard' not in properties:
    raise SystemExit('Restart protection verification failed; restart was not requested')
command('systemctl', 'restart', '--no-block', 'cnc-worker')
print(json.dumps({'restart_requested': True, 'previous_pid': manifest['previous_pid'], 'rollback': str(backup), 'effective_stop': properties}))
