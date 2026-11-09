"""Narrow controller-side enum rollout. Input is a reviewed, Git-recorded payload."""
import base64
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import urllib.request

payload = json.loads(gzip.decompress(base64.b64decode(SOURCE_PAYLOAD)))
root = Path('/opt/cnc')
release = root / 'plugin-verification-releases' / payload['revision'][:16]
release.mkdir(parents=True, exist_ok=True, mode=0o700)
rollback = release / 'rollback'
rollback.mkdir(exist_ok=True, mode=0o700)
config = json.loads((root / 'config.json').read_text())
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
with opener.open('http://' + config['http_addr'] + '/api/jobs', timeout=30) as response:
    jobs = json.load(response)
assert not any(j['status'] in ('running', 'pending') for j in jobs), 'Wait for active jobs before rollout'
source = release / 'source'
source.mkdir(exist_ok=True)
baseline = root / 'source/cnc-backend/enum'
build_source = source / 'cnc-backend/enum'
build_source.mkdir(parents=True, exist_ok=True)
for directory in ('', 'version', 'detection'):
    destination = build_source / directory
    destination.mkdir(exist_ok=True)
    for path in (baseline / directory).iterdir():
        if path.is_file() and (path.suffix == '.go' or path.name in ('go.mod', 'go.sum')):
            shutil.copy2(path, destination / path.name)
for name, content in payload['files'].items():
    if name.startswith('cnc-backend/enum/'):
        destination = source / name
        destination.parent.mkdir(parents=True, exist_ok=True)
        destination.write_text(content)
proof = release / 'verify-plugin-rules.py'
proof.write_text(payload['files']['infra/aws/verify-plugin-rules.py'])
env = dict(os.environ, GOPATH=str(root / 'go-work'), GOMODCACHE=str(root / 'go-work/pkg/mod'), GOCACHE=str(root / 'go-cache'), GOMAXPROCS='2')
go = str(root / 'go/bin/go')
binary = release / 'cnc-enum'
with (release / 'verification.log').open('w') as log:
    subprocess.run([go, 'test', '-race', '-count=1', './version', '.'], cwd=build_source, env=env, stdout=log, stderr=subprocess.STDOUT, check=True, timeout=240)
    subprocess.run([go, 'build', '-trimpath', '-ldflags=-s -w', '-o', str(binary), '.'], cwd=build_source, env=env, stdout=log, stderr=subprocess.STDOUT, check=True, timeout=180)
result = subprocess.run(['python3', str(proof), str(binary)], capture_output=True, text=True, check=True, timeout=90)
verification = json.loads(result.stdout)
sha = hashlib.sha256(binary.read_bytes()).hexdigest()
bundle = root / 'worker-tools.tar.gz'
staged = release / 'worker-tools.tar.gz'
found = set()
with tarfile.open(bundle) as old, tarfile.open(staged, 'w:gz') as new:
    for member in old:
        name = member.name.lstrip('./')
        if name == 'tools/cnc-enum':
            data = binary.read_bytes()
            member.size = len(data)
            new.addfile(member, io.BytesIO(data))
            found.add('binary')
        elif name == 'tools/.cnc-manifest.json':
            manifest = json.load(old.extractfile(member))
            matches = [f for f in manifest['files'] if f['path'] == 'cnc-enum']
            assert len(matches) == 1
            matches[0]['sha256'] = sha
            data = json.dumps(manifest, sort_keys=True).encode()
            member.size = len(data)
            new.addfile(member, io.BytesIO(data))
            found.add('manifest')
        else:
            new.addfile(member, old.extractfile(member) if member.isfile() else None)
assert found == {'binary', 'manifest'}
target = root / 'tools/cnc-enum'
for original, name in ((target, 'cnc-enum'), (bundle, 'worker-tools.tar.gz')):
    if not (rollback / name).exists():
        shutil.copy2(original, rollback / name)
for name, content in payload['files'].items():
    if not name.startswith('cnc-backend/enum/'):
        continue
    destination = root / 'source' / name
    backup = rollback / 'source' / name
    if destination.exists() and not backup.exists():
        backup.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(destination, backup)
    destination.parent.mkdir(parents=True, exist_ok=True)
    destination.write_text(content)
stage = target.with_name('cnc-enum.new')
shutil.copy2(binary, stage)
stage.chmod(0o700)
os.replace(stage, target)
os.replace(staged, bundle)
(release / 'DEPLOYED.json').write_text(json.dumps({'sha256': sha, 'revision': payload['revision'], 'proof': verification}))
print(json.dumps({'release': str(release), 'sha256': sha, 'proof': verification, 'controller_restarted': False, 'rollback': str(rollback)}))
