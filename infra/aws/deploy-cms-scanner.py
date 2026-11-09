#!/usr/bin/env python3
"""Deploy the built-in CMS binary only, retaining backups and using local fixtures."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import time

from provision import aws, ROOT, STATE_FILE, PROFILE, REGION

REMOTE = r'''
import hashlib,io,json,os,shutil,subprocess,tarfile,urllib.request
from pathlib import Path
root=Path('/opt/cnc');release=root/'cms-releases'/REVISION
release.mkdir(parents=True,exist_ok=False);release.chmod(0o700)
archive=release/'release.tar.gz'
with urllib.request.urlopen(DOWNLOAD,timeout=60) as response:archive.write_bytes(response.read())
archive.chmod(0o600)
if hashlib.sha256(archive.read_bytes()).hexdigest()!=SHA:raise RuntimeError('Release checksum mismatch')
approved=release/'approved';approved.mkdir(mode=0o700)
with tarfile.open(archive) as package:
 members=package.getmembers()
 if sorted(m.name for m in members)!=sorted(FILES+['cms-scan']) or not all(m.isfile() for m in members):raise RuntimeError('Unexpected release members')
 package.extractall(approved,filter='data')
binary=approved/'cms-scan';binary.chmod(0o700)
if hashlib.sha256(binary.read_bytes()).hexdigest()!=BINARY_SHA:raise RuntimeError('Binary checksum mismatch')
fixture=subprocess.run(['python3',str(approved/'infra/aws/verify-cms-scanner.py'),str(binary)],capture_output=True,text=True,check=True,timeout=45)
verification=json.loads(fixture.stdout)
api_base=API_BASE
def api(path):return json.load(urllib.request.urlopen(api_base+path,timeout=10))
if CONTROLLER:
 config=json.loads((root/'config.json').read_text());api_base='http://'+config['http_addr']
 jobs=api('/api/jobs');jobs=jobs.get('jobs',[]) if isinstance(jobs,dict) else jobs
 active_jobs=sum(j.get('status') in ('queued','running','pending') for j in jobs)
rollback=release/'rollback';rollback.mkdir(mode=0o700)
target=root/'tools/cms-scan'
if not target.is_file() or target.is_symlink():raise RuntimeError('Unsafe binary destination')
shutil.copy2(target,rollback/'cms-scan')
sources=[n for n in FILES if n.startswith('cnc-backend/')]
absent=[];copied=[];archive_switched=False;binary_switched=False
bundle=root/'worker-tools.tar.gz';stage_bundle=release/'worker-tools.tar.gz'
if CONTROLLER:
 if not bundle.is_file() or bundle.is_symlink():raise RuntimeError('Unsafe worker archive')
 shutil.copy2(bundle,rollback/'worker-tools.tar.gz')
 matches=0
 with tarfile.open(bundle,'r:gz') as old,tarfile.open(stage_bundle,'w:gz') as new:
  for member in old:
   if member.name.lstrip('./')=='tools/cms-scan':
    if not member.isfile():raise RuntimeError('Unsafe CMS archive member')
    member.size=binary.stat().st_size;member.mode=0o700
    new.addfile(member,io.BytesIO(binary.read_bytes()));matches+=1
   else:new.addfile(member,old.extractfile(member) if member.isfile() else None)
 if matches!=1:raise RuntimeError('CMS archive member missing or duplicated')
 with tarfile.open(stage_bundle) as package:
  member=next(m for m in package if m.name.lstrip('./')=='tools/cms-scan')
  if hashlib.sha256(package.extractfile(member).read()).hexdigest()!=BINARY_SHA:raise RuntimeError('Worker archive checksum mismatch')
 for name in sources:
  destination=root/'source'/name;backup=rollback/'source'/name
  if destination.exists():
   if destination.is_symlink() or not destination.is_file():raise RuntimeError('Unsafe source destination')
   backup.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(destination,backup)
  else:absent.append(name)
 (rollback/'absent.json').write_text(json.dumps(absent))
try:
 stage=target.with_name('cms-scan.'+REVISION+'.new');shutil.copy2(binary,stage);stage.chmod(0o700);stage.replace(target);binary_switched=True
 if CONTROLLER:
  for name in sources:
   destination=root/'source'/name;destination.parent.mkdir(parents=True,exist_ok=True)
   copied.append(name);shutil.copy2(approved/name,destination)
  stage_bundle.replace(bundle);archive_switched=True
 live=subprocess.run(['python3',str(approved/'infra/aws/verify-cms-scanner.py'),str(target)],capture_output=True,text=True,check=True,timeout=45)
 verification=json.loads(live.stdout)
 if verification['sha256']!=BINARY_SHA:raise RuntimeError('Installed binary checksum mismatch')
 if CONTROLLER:
  verification['workers_online']=sum(w.get('status')=='online' for w in api('/api/workers'))
  verification['drive_authorized']=api('/api/config/gdrive/auth-status').get('authorized')
  verification['worker_archive_updated']=True
  verification['active_jobs_preserved']=active_jobs
 verification.update(revision=REVISION,rollback=str(rollback),services_restarted=False)
 (release/'verification.json').write_text(json.dumps(verification,indent=2)+'\n')
 (release/'DEPLOYED').write_text(REVISION+'\n')
 print(json.dumps(verification))
except Exception:
 if binary_switched:
  stage=target.with_name('cms-scan.rollback');shutil.copy2(rollback/'cms-scan',stage);stage.replace(target)
 if archive_switched:shutil.copy2(rollback/'worker-tools.tar.gz',bundle)
 for name in copied:
  destination=root/'source'/name;backup=rollback/'source'/name
  if backup.exists():shutil.copy2(backup,destination)
  elif name in absent:destination.unlink(missing_ok=True)
 raise
'''


def main():
    state = json.loads(STATE_FILE.read_text())
    def invoke(instance, command, comment):
        command_id = aws('ssm', 'send-command', '--instance-ids', instance, '--document-name', 'AWS-RunShellScript', '--comment', comment, '--parameters', json.dumps({'commands': [command], 'executionTimeout': ['300']}))['Command']['CommandId']
        print(json.dumps({'instance': instance, 'command_id': command_id}), flush=True)
        while True:
            try:
                result = aws('ssm', 'get-command-invocation', '--command-id', command_id, '--instance-id', instance)
            except RuntimeError as error:
                if 'InvocationDoesNotExist' in str(error):
                    time.sleep(2)
                    continue
                raise
            if result['Status'] not in ('Pending', 'InProgress', 'Delayed'):
                break
            time.sleep(3)
        if result['Status'] != 'Success':
            raise RuntimeError(instance + ': ' + result.get('StandardErrorContent', '') + result.get('StandardOutputContent', ''))
        return command_id, json.loads(result['StandardOutputContent'])
    preflight = """python3 - <<'CMS_PREFLIGHT'
import json,urllib.request
from pathlib import Path
config=json.loads(Path('/opt/cnc/config.json').read_text())
jobs=json.load(urllib.request.urlopen('http://'+config['http_addr']+'/api/jobs',timeout=10))
jobs=jobs.get('jobs',[]) if isinstance(jobs,dict) else jobs
print(json.dumps({'active_jobs_preserved':sum(j.get('status') in ('queued','running','pending') for j in jobs)}))
CMS_PREFLIGHT
"""
    invoke(state['nodes']['cnc-aws-server']['id'], preflight, 'CMS deployment status preflight')
    files = ['cnc-backend/cms-scan/' + n for n in ('cms.go', 'cms_test.go', 'go.mod', 'go.sum')]
    files += ['cnc-backend/enum/go.mod', 'cnc-backend/enum/go.sum']
    for folder in ('detection', 'version'):
        files += [str(p.relative_to(ROOT)) for p in sorted((ROOT / 'cnc-backend/enum' / folder).glob('*.go'))]
    files += ['infra/aws/deploy-cms-scanner.py', 'infra/aws/verify-cms-scanner.py']
    with tempfile.TemporaryDirectory(prefix='cnc-cms-release-') as directory:
        temporary = Path(directory)
        env = dict(os.environ, GIT_INDEX_FILE=str(temporary / 'index'))
        def git(*args):
            return subprocess.check_output(['git', *args], cwd=ROOT, env=env, text=True).strip()
        git('read-tree', '--empty')
        git('add', '--', *files)
        revision = git('commit-tree', git('write-tree'), '-m', 'Preserve CMS input on startup failure and stop cancelled queued scans')
        git('update-ref', 'refs/heads/codex/cms-scanner-aws', revision)
        binary = temporary / 'cms-scan'
        buildenv = dict(os.environ, GOOS='linux', GOARCH='amd64', CGO_ENABLED='0', GOCACHE='/private/tmp/cnc-restore-go-cache')
        subprocess.run(['go', 'build', '-trimpath', '-ldflags=-s -w', '-o', str(binary), '.'], cwd=ROOT / 'cnc-backend/cms-scan', env=buildenv, check=True)
        binary_sha = hashlib.sha256(binary.read_bytes()).hexdigest()
        archive = temporary / 'release.tar.gz'
        with tarfile.open(archive, 'w:gz') as package:
            for name in files:
                p = ROOT / name
                if p.is_symlink() or not p.is_file():
                    raise RuntimeError('Invalid release file ' + name)
                package.add(p, arcname=name, recursive=False)
            package.add(binary, arcname='cms-scan', recursive=False)
        sha = hashlib.sha256(archive.read_bytes()).hexdigest()
        key = 'releases/cms-scanner-' + revision + '.tar.gz'
        aws('s3api', 'put-object', '--bucket', state['release_bucket'], '--key', key, '--body', str(archive), '--server-side-encryption', 'AES256')
        url = subprocess.check_output(['aws', 's3', 'presign', 's3://' + state['release_bucket'] + '/' + key, '--expires-in', '1800', '--profile', PROFILE, '--region', REGION], text=True).strip()
        report = {'revision': revision, 'sha256': binary_sha, 'nodes': {}}
        # Validate each replacement before switching, workers first; no service restart.
        nodes = sorted(state['nodes'].items(), key=lambda pair: pair[0] == 'cnc-aws-server')
        for name, node in nodes:
            values = dict(REVISION=revision, SHA=sha, DOWNLOAD=url, FILES=files, BINARY_SHA=binary_sha,
                          CONTROLLER=name == 'cnc-aws-server', API_BASE='http://' + state['nodes']['cnc-aws-server']['private_ip'] + ':8080')
            code = '\n'.join(k + '=' + repr(v) for k, v in values.items()) + '\n' + REMOTE
            command = "python3 - <<'CNC_CMS_DEPLOY'\n" + code + '\nCNC_CMS_DEPLOY\n'
            command_id, evidence = invoke(node['id'], command, 'CNC built-in CMS scanner update')
            report['nodes'][name] = dict(evidence, command_id=command_id)
            print(json.dumps({'node': name, 'result': evidence}), flush=True)
            evidence_path = ROOT / 'docs/verification/cms-scanner.json'
            evidence_path.parent.mkdir(parents=True, exist_ok=True)
            evidence_path.write_text(json.dumps(report, indent=2) + '\n')


if __name__ == '__main__':
    main()
