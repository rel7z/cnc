"""Install one pinned enum binary and increase slots on an idle existing worker."""
import hashlib
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import time
import urllib.request

root = Path('/opt/cnc')
pid = subprocess.check_output(['systemctl', 'show', '-p', 'MainPID', '--value', 'cnc-worker'], text=True).strip()
assert pid.isdigit() and int(pid) > 0
assert all(not p.read_text().strip() for p in Path('/proc', pid, 'task').glob('*/children')), 'Worker has active child tasks'
args = Path('/proc', pid, 'cmdline').read_bytes().decode().split('\0')
config_path = None
for i, arg in enumerate(args):
    if arg in ('-config', '--config'):
        config_path = Path(args[i+1])
    elif arg.startswith(('-config=', '--config=')):
        config_path = Path(arg.split('=', 1)[1])
if config_path is None:
    config_path = Path('/proc', pid, 'cwd').resolve() / 'worker_config.json'
if not config_path.is_absolute():
    config_path = Path('/proc', pid, 'cwd').resolve() / config_path
assert config_path.is_file() and not config_path.is_symlink()
config = json.loads(config_path.read_text())
assert config.get('worker_id', '').startswith(('cnc-aws-worker-', 'cnc-linode-worker-'))
old_capacity = config['max_tasks']
capacity = max(4, min(8, (os.cpu_count() or 2)*2))
manifest_path = root / 'tools/.cnc-manifest.json'
manifest = json.loads(manifest_path.read_text())
assert manifest_path.is_file() and not manifest_path.is_symlink()
target = root / 'tools/cnc-enum'
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
with opener.open('http://172.31.4.149:8080/download/cnc-worker-tools', timeout=120) as response:
    archive = response.read()
with tarfile.open(fileobj=io.BytesIO(archive)) as package:
    member = package.getmember('tools/cnc-enum')
    assert member.isfile()
    data = package.extractfile(member).read()
assert hashlib.sha256(data).hexdigest() == EXPECTED_SHA
matches = [f for f in manifest['files'] if f['path'] == 'cnc-enum']
assert len(matches) == 1
backup = root / 'plugin-verification-rollback' / str(time.time_ns())
backup.mkdir(parents=True, mode=0o700)
for path, name in ((target, 'cnc-enum'), (manifest_path, 'manifest.json'), (config_path, 'config.json')):
    shutil.copy2(path, backup/name)
try:
    stage = target.with_name('cnc-enum.new')
    stage.write_bytes(data)
    stage.chmod(0o700)
    os.replace(stage, target)
    matches[0]['sha256'] = EXPECTED_SHA
    stage = manifest_path.with_suffix('.new')
    stage.write_text(json.dumps(manifest, sort_keys=True))
    stage.chmod(0o600)
    os.replace(stage, manifest_path)
    config['max_tasks'] = capacity
    stage = config_path.with_suffix('.new')
    stage.write_text(json.dumps(config, indent=2)+'\n')
    shutil.copymode(config_path, stage)
    os.replace(stage, config_path)
    if old_capacity != capacity:
        subprocess.run(['systemctl', 'restart', 'cnc-worker'], check=True, timeout=60)
        time.sleep(2)
    assert subprocess.check_output(['systemctl', 'is-active', 'cnc-worker'], text=True).strip() == 'active'
except BaseException:
    for path, name in ((target, 'cnc-enum'), (manifest_path, 'manifest.json'), (config_path, 'config.json')):
        shutil.copy2(backup/name, path)
    if old_capacity != capacity:
        subprocess.run(['systemctl', 'restart', 'cnc-worker'], timeout=60)
    raise
print(json.dumps({'worker':config['worker_id'], 'sha256':EXPECTED_SHA, 'old_max_tasks':old_capacity,
                  'max_tasks':capacity, 'cpu_count':os.cpu_count(), 'service':'active', 'worker_restarted':old_capacity != capacity, 'rollback':str(backup)}))
