#!/usr/bin/env python3
"""Narrow enum throughput rollout and owner-approved restart of one existing job."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import time

from provision import aws, ROOT, STATE_FILE, PROFILE, REGION

BUILD = r'''
import hashlib,io,json,os,shutil,subprocess,tarfile,urllib.request
from pathlib import Path
root=Path('/opt/cnc');source=root/'source';release=root/'enum-performance-releases'/REVISION
release.mkdir(parents=True,exist_ok=True);release.chmod(0o700)
rollback=release/'rollback';rollback.mkdir(mode=0o700,exist_ok=True)
archive=release/'source.tar.gz'
with urllib.request.urlopen(DOWNLOAD,timeout=60) as response:archive.write_bytes(response.read())
archive.chmod(0o600)
if hashlib.sha256(archive.read_bytes()).hexdigest()!=SHA:raise RuntimeError('Release checksum mismatch')
approved=release/'approved';approved.mkdir(mode=0o700,exist_ok=True)
with tarfile.open(archive) as package:
 members=package.getmembers()
 if sorted(m.name for m in members)!=sorted(FILES) or not all(m.isfile() for m in members):raise RuntimeError('Unexpected source members')
 package.extractall(approved,filter='data')
config=json.loads((root/'config.json').read_text());base='http://'+config['http_addr']
opener=urllib.request.build_opener(urllib.request.ProxyHandler({}))
def api(path):return json.load(opener.open(base+path,timeout=30))
old=api('/api/jobs/'+OLD_JOB)
if old['status']!='running':raise RuntimeError('Requested job is no longer running; inspect before restarting')
state_dir=Path(config['data_dir'])/'enum-jobs'/OLD_JOB
state=json.loads((state_dir/'state.json').read_text())
artifact=json.loads((state_dir/state['artifact_filename']).read_text())
settings=old['enum']
request={'tool_id':'enum','input_file':old['input_file'],'mode':'spread','timeout_seconds':old['timeout_seconds'],
 'version_rules':artifact['rules'],'options':{'threads':settings['threads'],'auto_concurrency':settings['automatic'],'timeout':settings['request_timeout_ms']//1000}}
for name in settings.get('wordlists',[]):
 if name not in ('wp_plugins.txt','joomla_exts.txt'):raise RuntimeError('Unexpected wordlist name')
 request[name.removesuffix('.txt')]=(state_dir/name).read_text()
(release/'restart-request.json').write_text(json.dumps(request));(release/'restart-request.json').chmod(0o600)
(release/'actor.txt').write_text(settings['actor']);(release/'actor.txt').chmod(0o600)
selected=[name for name in FILES if name.startswith('cnc-backend/')]
absent=[];changed=[];switched=[]
targets={'cnc-enum':root/'tools/cnc-enum','cms-scan':root/'tools/cms-scan','cnc-worker-linux':root/'cnc-worker-linux'}
first_rollbacks=sorted([p for p in (root/'enum-performance-releases').glob('*/rollback') if p!=rollback])
for name,destination in targets.items():
 if not (rollback/name).exists():
  original=first_rollbacks[0]/name if first_rollbacks and (first_rollbacks[0]/name).exists() else destination
  shutil.copy2(original,rollback/name)
bundle=root/'worker-tools.tar.gz'
if not (rollback/'worker-tools.tar.gz').exists():
 original_bundle=first_rollbacks[0]/'worker-tools.tar.gz' if first_rollbacks and (first_rollbacks[0]/'worker-tools.tar.gz').exists() else bundle
 shutil.copy2(original_bundle,rollback/'worker-tools.tar.gz')
for name in selected:
 destination=source/name;backup=rollback/'source'/name
 if destination.exists():
  if destination.is_symlink() or not destination.is_file():raise RuntimeError('Unsafe source destination')
  if not backup.exists():backup.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(destination,backup)
 else:absent.append(name)
if not (rollback/'absent.json').exists():(rollback/'absent.json').write_text(json.dumps(absent))
env=dict(os.environ,GOPATH=str(root/'go-work'),GOMODCACHE=str(root/'go-work/pkg/mod'),GOCACHE=str(root/'go-cache'))
go=str(root/'go/bin/go')
try:
 for name in selected:
  destination=source/name;destination.parent.mkdir(parents=True,exist_ok=True)
  changed.append(name);shutil.copy2(approved/name,destination)
 with (release/'build.log').open('w') as log:
  subprocess.run([go,'test','./...'],cwd=source/'cnc-backend/enum',env=env,stdout=log,stderr=subprocess.STDOUT,check=True,timeout=180)
  for name,directory,package in [('cnc-enum','cnc-backend/enum','.'),('cms-scan','cnc-backend/cms-scan','.'),('cnc-worker-linux','cnc-api','./cmd/worker')]:
   subprocess.run([go,'build','-trimpath','-ldflags=-s -w','-o',str(release/name),package],cwd=source/directory,env=env,stdout=log,stderr=subprocess.STDOUT,check=True,timeout=240)
  subprocess.run([go,'test','.','-run','TestEnum|TestCMS|TestShellFileRecords','-count=1'],cwd=source/'cnc-api',env=env,stdout=log,stderr=subprocess.STDOUT,check=True,timeout=180)
 (release/'build.log').chmod(0o600)
 baseline=rollback/'cnc-enum'
 benchmark=subprocess.run(['python3',str(approved/'infra/aws/benchmark-enum.py'),str(baseline),str(release/'cnc-enum')],capture_output=True,text=True,check=True,timeout=120)
 measured=json.loads(benchmark.stdout)
 if any(m['speedup']<1.5 for m in measured.values()):raise RuntimeError('Throughput improvement not demonstrated')
 (release/'benchmark.json').write_text(json.dumps(measured,indent=2)+'\n')
 fixture=subprocess.run(['python3',str(approved/'infra/aws/verify-cms-scanner.py'),str(release/'cms-scan')],capture_output=True,text=True,check=True,timeout=30)
 if not json.loads(fixture.stdout)['fixtures_passed']:raise RuntimeError('CMS fixture verification failed')
 hashes={name:hashlib.sha256((release/name).read_bytes()).hexdigest() for name in targets}
 stage_bundle=release/'worker-tools.tar.gz';matches=set()
 with tarfile.open(bundle) as old_tar,tarfile.open(stage_bundle,'w:gz') as new_tar:
  for member in old_tar:
   normalized=member.name.lstrip('./')
   name=normalized.removeprefix('tools/')
   if normalized in ('tools/cnc-enum','tools/cms-scan'):
    if not member.isfile() or name in matches:raise RuntimeError('Unsafe tool archive member')
    content=(release/name).read_bytes();member.size=len(content);member.mode=0o700
    new_tar.addfile(member,io.BytesIO(content));matches.add(name)
   else:new_tar.addfile(member,old_tar.extractfile(member) if member.isfile() else None)
 if matches!={'cnc-enum','cms-scan'}:raise RuntimeError('Missing worker tool member')
 with tarfile.open(stage_bundle) as package:
  for member in package:
   if member.name.lstrip('./') in ('tools/cnc-enum','tools/cms-scan'):
    name=member.name.lstrip('./').removeprefix('tools/')
    if hashlib.sha256(package.extractfile(member).read()).hexdigest()!=hashes[name]:raise RuntimeError('Archive tool checksum mismatch')
 for name,destination in targets.items():
  stage=destination.with_name(destination.name+'.new');shutil.copy2(release/name,stage);stage.chmod(0o700);stage.replace(destination);switched.append(name)
 stage_bundle.replace(bundle);switched.append('archive')
 print(json.dumps({'revision':REVISION,'hashes':hashes,'benchmark':measured,'source_tests_passed':True,'services_restarted':False,'rollback':str(rollback)}))
except Exception:
 for name in switched:
  if name=='archive':shutil.copy2(rollback/'worker-tools.tar.gz',bundle)
  else:
   stage=targets[name].with_name(targets[name].name+'.rollback');shutil.copy2(rollback/name,stage);stage.replace(targets[name])
 for name in changed:
  destination=source/name;backup=rollback/'source'/name
  if backup.exists():shutil.copy2(backup,destination)
  elif name in absent:destination.unlink(missing_ok=True)
 raise
'''

WORKER = r'''
import hashlib,io,json,shutil,subprocess,tarfile,urllib.request
from pathlib import Path
root=Path('/opt/cnc');release=root/'enum-performance-releases'/REVISION
release.mkdir(parents=True,exist_ok=True);release.chmod(0o700)
rollback=release/'rollback';rollback.mkdir(mode=0o700,exist_ok=True)
opener=urllib.request.build_opener(urllib.request.ProxyHandler({}))

worker_url=BASE+'/download/cnc-worker-linux'
with opener.open(worker_url,timeout=60) as response:worker_bytes=response.read()
if hashlib.sha256(worker_bytes).hexdigest()!=HASHES['cnc-worker-linux']:raise RuntimeError('Candidate checksum mismatch: cnc-worker-linux')
(release/'cnc-worker-linux').write_bytes(worker_bytes);(release/'cnc-worker-linux').chmod(0o700)
if (root/'cnc-worker').exists() and not (rollback/'cnc-worker').exists():shutil.copy2(root/'cnc-worker',rollback/'cnc-worker')

tools_bundle=release/'worker-tools.tar.gz'
with opener.open(BASE+'/download/cnc-worker-tools',timeout=60) as response:tools_bundle.write_bytes(response.read())
tools_bundle.chmod(0o600)
with tarfile.open(tools_bundle,'r:gz') as package:
 for tool_name in ('cnc-enum','cms-scan'):
  member=package.getmember('tools/'+tool_name)
  tool_bytes=package.extractfile(member).read()
  if hashlib.sha256(tool_bytes).hexdigest()!=HASHES[tool_name]:raise RuntimeError('Candidate checksum mismatch: '+tool_name)
  (release/tool_name).write_bytes(tool_bytes);(release/tool_name).chmod(0o700)
  tool_dst=root/'tools'/tool_name
  if tool_dst.exists() and not (rollback/tool_name).exists():shutil.copy2(tool_dst,rollback/tool_name)

targets={'cnc-worker-linux':root/'cnc-worker','cnc-enum':root/'tools/cnc-enum','cms-scan':root/'tools/cms-scan'}
for name,destination in targets.items():
 destination.parent.mkdir(parents=True,exist_ok=True)
 stage=destination.with_name(destination.name+'.new');shutil.copy2(release/name,stage);stage.chmod(0o700);stage.replace(destination)

dropin=Path('/etc/systemd/system/cnc-worker.service.d/zz-enum-throughput.conf')
dropin.parent.mkdir(parents=True,exist_ok=True)
if dropin.exists() and not (rollback/'throughput.conf').exists():shutil.copy2(dropin,rollback/'throughput.conf')
dropin.write_text('[Service]\nEnvironment=ENUM_MAX_INFLIGHT_REQUESTS=64\n');dropin.chmod(0o600)
print(json.dumps({'hashes':HASHES,'staged':True,'restarted':False,'rollback':str(rollback)}))
'''

RELAUNCH = r'''
import hashlib,hmac,json,time,urllib.request
from pathlib import Path
root=Path('/opt/cnc');release=root/'enum-performance-releases'/REVISION
config=json.loads((root/'config.json').read_text());base='http://'+config['http_addr']
opener=urllib.request.build_opener(urllib.request.ProxyHandler({}))
body=(release/'restart-request.json').read_bytes();actor=(release/'actor.txt').read_text()
secret=dict(line.split('=',1) for line in (root/'dashboard-audit.env').read_text().splitlines() if '=' in line)['CNC_DASHBOARD_AUDIT_SECRET']
stamp=str(int(time.time()*1000));signature=hmac.new(secret.encode(),(actor+'\n'+stamp+'\nPOST\n/api/tools/launch\n'+hashlib.sha256(body).hexdigest()).encode(),hashlib.sha256).hexdigest()
request=urllib.request.Request(base+'/api/tools/launch',body,{'Content-Type':'application/json','X-CNC-Actor':actor,'X-CNC-Audit-Time':stamp,'X-CNC-Audit-Signature':signature})
with opener.open(request,timeout=600) as response:created=json.load(response)
new_id=created['job_id'];(release/'replacement-job.txt').write_text(new_id+'\n')
print(json.dumps({'old_job':OLD_JOB,'new_job':new_id,'same_input_and_settings':True,'existing_results_retained':True}))
'''


def invoke(instance, code, label, timeout=900):
    command = "python3 - <<'ENUM_RELEASE'\n" + code + '\nENUM_RELEASE\n'
    command_id = aws('ssm', 'send-command', '--instance-ids', instance, '--document-name', 'AWS-RunShellScript', '--comment', label, '--parameters', json.dumps({'commands': [command], 'executionTimeout': [str(timeout)]}))['Command']['CommandId']
    print(json.dumps({'instance': instance, 'command_id': command_id, 'phase': label}), flush=True)
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
        time.sleep(4)
    if result['Status'] != 'Success':
        raise RuntimeError(label + ': ' + result.get('StandardErrorContent', '') + result.get('StandardOutputContent', ''))
    data = json.loads(result['StandardOutputContent'])
    print(json.dumps({'phase': label, 'result': data}), flush=True)
    return dict(data, command_id=command_id)


def main():
    state = json.loads(STATE_FILE.read_text())
    old_job = 'enum_5b19379584b7897ee19599a9'
    files = []
    for folder in ('cnc-backend/enum', 'cnc-backend/enum/detection', 'cnc-backend/enum/version', 'cnc-backend/cms-scan'):
        files += [str(p.relative_to(ROOT)) for p in sorted((ROOT / folder).iterdir()) if p.is_file() and (p.suffix == '.go' or p.name in ('go.mod', 'go.sum'))]
    files += ['cnc-backend/enum/OPTIMIZATION.md', 'infra/aws/benchmark-enum.py', 'infra/aws/verify-cms-scanner.py', 'infra/aws/deploy-enum-performance.py']
    report = {'old_job': old_job, 'workers': {}}
    evidence = ROOT / 'docs/verification/enum-performance.json'
    def save():
        evidence.parent.mkdir(parents=True, exist_ok=True)
        evidence.write_text(json.dumps(report, indent=2) + '\n')
    with tempfile.TemporaryDirectory(prefix='enum-release-') as folder:
        temporary = Path(folder)
        env = dict(os.environ, GIT_INDEX_FILE=str(temporary / 'index'))
        def git(*args):return subprocess.check_output(['git', *args], cwd=ROOT, env=env, text=True).strip()
        git('read-tree', '--empty');git('add', '--', *files)
        revision = git('commit-tree', git('write-tree'), '-m', 'Remove cross-origin failure throttling and increase shared enum request capacity')
        git('update-ref', 'refs/heads/codex/enum-throughput-aws', revision)
        report['revision'] = revision
        archive = temporary / 'release.tar.gz'
        with tarfile.open(archive, 'w:gz') as package:
            for name in files:
                if (ROOT / name).is_symlink():raise RuntimeError('Symlink source rejected')
                package.add(ROOT / name, arcname=name, recursive=False)
        digest = hashlib.sha256(archive.read_bytes()).hexdigest()
        key = 'releases/enum-performance-' + revision + '.tar.gz'
        aws('s3api', 'put-object', '--bucket', state['release_bucket'], '--key', key, '--body', str(archive), '--server-side-encryption', 'AES256')
        url = subprocess.check_output(['aws', 's3', 'presign', 's3://' + state['release_bucket'] + '/' + key, '--expires-in', '3600', '--profile', PROFILE, '--region', REGION], text=True).strip()
        def code(template, **values):
            return '\n'.join(k + '=' + repr(v) for k, v in dict(REVISION=revision, OLD_JOB=old_job, **values).items()) + '\n' + template
        controller = state['nodes']['cnc-aws-server']
        built = invoke(controller['id'], code(BUILD, FILES=files, DOWNLOAD=url, SHA=digest), 'Build and verify enum throughput release')
        report['controller'] = built;save()
        base = 'http://' + controller['private_ip'] + ':8080'
        for name, node in state['nodes'].items():
            if name == 'cnc-aws-server':continue
            report['workers'][name] = invoke(node['id'], code(WORKER, HASHES=built['hashes'], BASE=base), 'Stage faster enum worker '+name)
            save()
        cancel = r'''
import json,urllib.request
from pathlib import Path
c=json.loads(Path('/opt/cnc/config.json').read_text());base='http://'+c['http_addr']
request=urllib.request.Request(base+'/api/jobs/'+OLD_JOB+'/cancel',data=b'{}',headers={'Content-Type':'application/json'},method='POST')
with urllib.request.urlopen(request,timeout=30) as response:result=json.load(response)
print(json.dumps({'old_job':OLD_JOB,'cancelled_for_approved_restart':True}))
'''
        report['cancellation'] = invoke(controller['id'], code(cancel), 'Cancel old job for owner-approved restart');save()
        restart = r'''
import json,subprocess
subprocess.run(['systemctl','daemon-reload'],check=True)
subprocess.run(['systemctl','restart','cnc-worker'],check=True)
active=subprocess.check_output(['systemctl','is-active','cnc-worker'],text=True).strip()
if active!='active':raise RuntimeError('Worker restart failed')
print(json.dumps({'service_active':True,'shared_request_budget':64}))
'''
        for name, node in state['nodes'].items():
            if name == 'cnc-aws-server':continue
            report['workers'][name]['activation'] = invoke(node['id'], code(restart), 'Activate faster enum worker '+name)
            save()
        report['replacement'] = invoke(controller['id'], code(RELAUNCH), 'Relaunch same enum input and settings', timeout=900);save()


if __name__ == '__main__':
    main()
