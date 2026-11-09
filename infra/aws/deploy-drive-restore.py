#!/usr/bin/env python3
"""Deploy only Drive recovery UI through Git, encrypted S3, and SSM."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import time

from provision import aws, ROOT, STATE_FILE, PROFILE, REGION

FILES = ['cnc-api/gdrive_restore.go', 'cnc-api/gdrive_restore_test.go', 'cnc-ui/components/dashboard/GDriveSettings.tsx', 'infra/aws/patch-drive-restore.py', 'infra/aws/deploy-drive-restore.py']

REMOTE = r'''
import hashlib,json,os,shutil,subprocess,tarfile,time,urllib.request,urllib.error
from pathlib import Path
root=Path('/opt/cnc');source=root/'source';ui=source/'cnc-ui'
release=root/'drive-restore-releases'/REVISION;release.mkdir(parents=True,exist_ok=False);release.chmod(0o700)
archive=release/'source.tar.gz'
with urllib.request.urlopen(DOWNLOAD,timeout=60) as response:archive.write_bytes(response.read())
if hashlib.sha256(archive.read_bytes()).hexdigest()!=SHA:raise RuntimeError('Release checksum mismatch')
archive.chmod(0o600);approved=release/'approved';approved.mkdir(mode=0o700)
with tarfile.open(archive) as package:
 members=package.getmembers()
 if sorted(m.name for m in members)!=sorted(FILES) or not all(m.isfile() for m in members):raise RuntimeError('Unexpected release members')
 package.extractall(approved,filter='data')
config=json.loads((root/'config.json').read_text());base='http://'+config['http_addr']
def api(path):return json.load(urllib.request.urlopen(base+path,timeout=10))
jobs=api('/api/jobs');jobs=jobs.get('jobs',[]) if isinstance(jobs,dict) else jobs
if any(j.get('status') in ('queued','running','pending') for j in jobs):raise RuntimeError('Active controller jobs; retry after completion')
workers_before=sum(w.get('status')=='online' for w in api('/api/workers'))
rollback=release/'rollback';rollback.mkdir(mode=0o700)
changed=['cnc-api/gdrive.go','cnc-api/server.go','cnc-api/gdrive_restore.go','cnc-api/gdrive_restore_test.go','cnc-ui/components/dashboard/GDriveSettings.tsx']
absent=[]
for name in changed:
 p=source/name;b=rollback/'source'/name
 if p.exists():
  if p.is_symlink() or not p.is_file():raise RuntimeError('Unsafe source path')
  b.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(p,b)
 else:absent.append(name)
(rollback/'absent.json').write_text(json.dumps(absent))
binary=root/'cnc-server';shutil.copy2(binary,rollback/'cnc-server')
dropin=Path('/etc/systemd/system/cnc-ui.service.d/zzz-drive-restore.conf');old_dropin=dropin.read_bytes() if dropin.exists() else None
if old_dropin is not None:(rollback/'dropin.conf').write_bytes(old_dropin)
node=Path((root/'node-path').read_text().strip())/'bin'
env=dict(os.environ,PATH=str(node)+':'+os.environ.get('PATH',''),GO_API_URL=base,NEXT_TELEMETRY_DISABLED='1',GOPATH=str(root/'go-work'),GOMODCACHE=str(root/'go-work/pkg/mod'),GOCACHE=str(root/'go-cache'))
buildlog=release/'build.log';switched=False
try:
 for name in ['cnc-api/gdrive_restore.go','cnc-api/gdrive_restore_test.go']:
  shutil.copy2(approved/name,source/name)
 subprocess.run(['python3',str(approved/'infra/aws/patch-drive-restore.py'),str(source)],check=True)
 subprocess.run([str(root/'go/bin/gofmt'),'-w',*[str(source/p) for p in changed if p.endswith('.go')]],check=True)
 with buildlog.open('w') as output:
  subprocess.run([str(root/'go/bin/go'),'test','.','-run','TestDrive|TestGDrive|TestRevoked','-count=1'],cwd=source/'cnc-api',env=env,stdout=output,stderr=subprocess.STDOUT,check=True,timeout=180)
  subprocess.run([str(root/'go/bin/go'),'build','-ldflags=-s -w','-o',str(release/'cnc-server'),'./cmd/server'],cwd=source/'cnc-api',env=env,stdout=output,stderr=subprocess.STDOUT,check=True,timeout=180)
  build=release/'ui';shutil.copytree(ui,build,symlinks=True,ignore=shutil.ignore_patterns('node_modules','.next','.git','.env*'))
  (build/'node_modules').symlink_to(ui/'node_modules',target_is_directory=True)
  shutil.copy2(approved/'cnc-ui/components/dashboard/GDriveSettings.tsx',build/'components/dashboard/GDriveSettings.tsx')
  subprocess.run([str(node/'npm'),'run','build','--','--webpack'],cwd=build,env=env,stdout=output,stderr=subprocess.STDOUT,check=True,timeout=600)
 buildlog.chmod(0o600)
 target=build/'.next/standalone';shutil.copytree(build/'.next/static',target/'.next/static',dirs_exist_ok=True)
 if (build/'public').is_dir():shutil.copytree(build/'public',target/'public',dirs_exist_ok=True)
 if not (target/'server.js').is_file():raise RuntimeError('Missing standalone dashboard')
 shutil.copy2(release/'cnc-server',root/'cnc-server.restore-new');(root/'cnc-server.restore-new').chmod(0o700)
 (root/'cnc-server.restore-new').replace(binary);switched=True
 subprocess.run(['systemctl','restart','cnc-server'],check=True)
 for attempt in range(30):
  try:api('/api/config/gdrive/restore');break
  except OSError:time.sleep(1)
 else:raise RuntimeError('Restore API health failed')
 dropin.parent.mkdir(parents=True,exist_ok=True);stage=dropin.with_suffix('.new')
 stage.write_text('[Service]\nExecStart=\nExecStart='+str(node/'node')+' '+str(target/'server.js')+'\nWorkingDirectory='+str(target)+'\nEnvironment=HOSTNAME=127.0.0.1\nEnvironment=PORT=3000\nEnvironment=GO_API_URL='+base+'\n');stage.chmod(0o600);stage.replace(dropin)
 subprocess.run(['systemctl','daemon-reload'],check=True);subprocess.run(['systemctl','restart','cnc-ui'],check=True)
 for attempt in range(30):
  try:
   if urllib.request.urlopen('http://127.0.0.1:3000/login',timeout=5).status==200:break
  except OSError:pass
  time.sleep(1)
 else:raise RuntimeError('Dashboard health failed')
 for attempt in range(30):
  workers_after=sum(w.get('status')=='online' for w in api('/api/workers'))
  if workers_after>=workers_before:break
  time.sleep(2)
 else:raise RuntimeError('Workers did not reconnect')
 shutil.copy2(approved/'cnc-ui/components/dashboard/GDriveSettings.tsx',ui/'components/dashboard/GDriveSettings.tsx')
 (release/'DEPLOYED').write_text(REVISION+'\n')
 print(json.dumps({'revision':REVISION,'restore_api':api('/api/config/gdrive/restore'),'workers_online':workers_after,'drive_authorized':api('/api/config/gdrive/auth-status').get('authorized'),'rollback':str(rollback)}))
except Exception:
 for name in changed:
  b=rollback/'source'/name;p=source/name
  if b.exists():shutil.copy2(b,p)
  elif name in absent:p.unlink(missing_ok=True)
 if old_dropin is None:dropin.unlink(missing_ok=True)
 else:dropin.write_bytes(old_dropin)
 if switched:
  shutil.copy2(rollback/'cnc-server',root/'cnc-server.restore-rollback');(root/'cnc-server.restore-rollback').replace(binary)
  subprocess.run(['systemctl','restart','cnc-server'],check=False)
 subprocess.run(['systemctl','daemon-reload'],check=False);subprocess.run(['systemctl','restart','cnc-ui'],check=False)
 raise

'''

def main():
    state=json.loads(STATE_FILE.read_text())
    instance=state['nodes']['cnc-aws-server']['id']
    with tempfile.TemporaryDirectory(prefix='cnc-drive-release-') as directory:
        env=dict(os.environ,GIT_INDEX_FILE=str(Path(directory)/'index'))
        def git(*args):return subprocess.check_output(['git',*args],cwd=ROOT,env=env,text=True).strip()
        git('read-tree','--empty');git('add','--',*FILES)
        revision=git('commit-tree',git('write-tree'),'-m','Add restore from Google Drive to merged folder')
        git('update-ref','refs/heads/codex/drive-restore-aws',revision)
        archive=Path(directory)/'source.tar.gz'
        with tarfile.open(archive,'w:gz') as package:
            for name in FILES:
                p=ROOT/name
                if p.is_symlink() or not p.is_file():raise RuntimeError('Invalid release file '+name)
                package.add(p,arcname=name,recursive=False)
        sha=hashlib.sha256(archive.read_bytes()).hexdigest()
        bucket=state['release_bucket'];key='releases/drive-restore-'+revision+'.tar.gz'
        aws('s3api','put-object','--bucket',bucket,'--key',key,'--body',str(archive),'--server-side-encryption','AES256')
        url=subprocess.check_output(['aws','s3','presign','s3://'+bucket+'/'+key,'--expires-in','1800','--profile',PROFILE,'--region',REGION],text=True).strip()
        values={'REVISION':revision,'SHA':sha,'DOWNLOAD':url,'FILES':FILES}
        code='\n'.join(k+'='+repr(v) for k,v in values.items())+'\n'+REMOTE
        import shlex
        command='python3 - <<\'CNC_DRIVE_DEPLOY\'\n'+code+'\nCNC_DRIVE_DEPLOY\n'
        result=aws('ssm','send-command','--instance-ids',instance,'--document-name','AWS-RunShellScript','--comment','CNC Drive restore deployment','--parameters',json.dumps({'commands':[command],'executionTimeout':['900']}))
        command_id=result['Command']['CommandId']
        print(json.dumps({'revision':revision,'command_id':command_id}),flush=True)
        while True:
            try:result=aws('ssm','get-command-invocation','--command-id',command_id,'--instance-id',instance)
            except RuntimeError as error:
                if 'InvocationDoesNotExist' in str(error):time.sleep(3);continue
                raise
            if result['Status'] not in ('Pending','InProgress','Delayed'):
                print(json.dumps({k:result.get(k) for k in ('Status','StandardOutputContent','StandardErrorContent')},indent=2))
                if result['Status']!='Success':raise RuntimeError('Deployment failed; inspect the release build log')
                break
            time.sleep(5)

if __name__=='__main__':main()
