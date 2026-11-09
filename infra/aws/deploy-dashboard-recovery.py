#!/usr/bin/env python3
"""Stage and deploy bounded dashboard bootstrap; never package private tools."""
import argparse
import base64
import importlib.util
import json
import os
from pathlib import Path
import subprocess
from provision import aws, STATE_FILE, STATE_DIR, PROFILE, REGION
from release import prepare

spec=importlib.util.spec_from_file_location('scheduler_release',Path(__file__).with_name('deploy-scheduler-fairness.py'))
scheduler=importlib.util.module_from_spec(spec);spec.loader.exec_module(scheduler)

BUILD=r'''
import hashlib,json,os,shutil,subprocess,tarfile,urllib.request
from pathlib import Path
p=json.loads(__import__('base64').b64decode(PAYLOAD))
release=Path('/opt/cnc/dashboard-releases')/p['digest'][:16];release.mkdir(mode=0o700,parents=True,exist_ok=True)
archive=release/'source.tar.gz'
with urllib.request.urlopen(p['url'],timeout=120) as response:archive.write_bytes(response.read())
archive.chmod(0o600)
assert hashlib.sha256(archive.read_bytes()).hexdigest()==p['digest']
source=release/'source';source.mkdir(mode=0o700,exist_ok=True)
with tarfile.open(archive) as package:
 for member in package.getmembers():
  if member.name.startswith('processor/tools/') or not member.isfile():raise RuntimeError('Invalid release input')
 package.extractall(source,filter='data')
nodebin=Path('/opt/cnc/node-path').read_text().strip()+'/bin'
config=json.loads(Path('/opt/cnc/config.json').read_text())
env=dict(os.environ,PATH=nodebin+':'+os.environ['PATH'],GOPATH='/opt/cnc/go-work',GOMODCACHE='/opt/cnc/go-work/pkg/mod',GOCACHE='/opt/cnc/go-cache',GO_API_URL='http://'+config['http_addr'],NEXT_TELEMETRY_DISABLED='1')
commands=[('cnc-api',['/opt/cnc/go/bin/go','test','-race','.','-run','TestDashboard|TestBrokerAdmitsSmall|TestDispatcherSharesMemory|TestBrokerCountsOrdinary|TestWorkerCapacity','-count=1']),('cnc-api',['/opt/cnc/go/bin/go','build','-trimpath','-ldflags=-s -w','-o',str(release/'cnc-server'),'./cmd/server']),('cnc-ui',[nodebin+'/npm','ci','--no-audit','--no-fund']),('cnc-ui',[nodebin+'/npm','run','build','--','--webpack'])]
with (release/'build.log').open('w') as log:
 for directory,command in commands:
  e=dict(env,CGO_ENABLED='1' if 'test' in command else '0')
  subprocess.run(command,cwd=source/directory,env=e,stdout=log,stderr=subprocess.STDOUT,check=True,timeout=600)
ui=source/'cnc-ui';standalone=ui/'.next/standalone'
shutil.copytree(ui/'.next/static',standalone/'.next/static',dirs_exist_ok=True)
if (ui/'public').is_dir():shutil.copytree(ui/'public',standalone/'public',dirs_exist_ok=True)
result={'release':str(release),'source_digest':p['digest'],'tests_passed':True,'binaries':{'cnc-server':hashlib.sha256((release/'cnc-server').read_bytes()).hexdigest()},'activated':False}
(release/'verified.json').write_text(json.dumps(result));(release/'verified.json').chmod(0o600)
print(json.dumps(result))
'''

UI_APPLY=r'''
import hashlib,json,os,subprocess,time,urllib.request
from pathlib import Path
p=json.loads(__import__('base64').b64decode(PAYLOAD));release=Path(p['release'])
verified=json.loads((release/'verified.json').read_text())
pid=int(subprocess.check_output(['systemctl','show','-p','MainPID','--value','cnc-server'],text=True))
assert hashlib.sha256(Path('/proc',str(pid),'exe').read_bytes()).hexdigest()==verified['binaries']['cnc-server'],'Controller candidate is not active'
ui=release/'source/cnc-ui/.next/standalone'
assert (ui/'server.js').is_file()
drop=Path('/etc/systemd/system/cnc-ui.service.d/zzzzz-dashboard-recovery.conf');drop.parent.mkdir(parents=True,exist_ok=True)
previous=drop.read_bytes() if drop.exists() else None
if previous is not None:(release/'previous-ui-dropin.conf').write_bytes(previous)
node=Path('/opt/cnc/node-path').read_text().strip()+'/bin/node'
drop.write_text('[Service]\nTimeoutStopSec=10\nWorkingDirectory='+str(ui)+'\nExecStart=\nExecStart='+node+' '+str(ui/'server.js')+'\n')
try:
 subprocess.run(['systemctl','daemon-reload'],check=True)
 assert subprocess.check_output(['systemctl','show','-p','WorkingDirectory','--value','cnc-ui'],text=True).strip()==str(ui),'Another UI override takes precedence'
 subprocess.run(['systemctl','restart','cnc-ui'],check=True,timeout=60)
 deadline=time.monotonic()+40
 while True:
  try:
   with urllib.request.urlopen('http://127.0.0.1:3000/login',timeout=5) as response:assert response.status==200
   break
  except OSError:
   if time.monotonic()>deadline:raise
   time.sleep(1)
except BaseException:
 if previous is None:drop.unlink()
 else:drop.write_bytes(previous)
 subprocess.run(['systemctl','daemon-reload'],check=True);subprocess.run(['systemctl','restart','cnc-ui'],check=True,timeout=60);raise
print(json.dumps({'ui_applied':True,'release':str(release)}))
'''


def main():
 parser=argparse.ArgumentParser();parser.add_argument('--apply',action='store_true');parser.add_argument('--ui-only',action='store_true');args=parser.parse_args()
 os.environ['PATH']=str(Path.home()/'.local/bin')+os.pathsep+os.environ.get('PATH','')
 state=json.loads(STATE_FILE.read_text());record=STATE_DIR/'dashboard-recovery.json';instance=state['nodes']['cnc-aws-server']['id']
 def send(code,payload,label):
  encoded=base64.b64encode(json.dumps(payload).encode()).decode()
  return scheduler.send("python3 - <<'DASHBOARD'\n"+code.replace('PAYLOAD',repr(encoded))+"\nDASHBOARD\n",instance,label)
 if args.apply:
  candidate=json.loads(record.read_text());payload=dict(candidate,component='server',allow_active_enum_interruption=False)
  controller=candidate.get('controller',{'previously_applied':True})
  if not args.ui_only:
   controller=send(scheduler.APPLY,payload,'CNC dashboard bootstrap controller update')
   candidate['controller']=controller;record.write_text(json.dumps(candidate,indent=2))
  ui=send(UI_APPLY,candidate,'CNC dashboard recovery UI update')
  candidate.update(activated=True,controller=controller,ui=ui);record.write_text(json.dumps(candidate,indent=2))
 else:
  archive,digest,files=prepare();assert not any(str(p).startswith('processor/tools/') for p in files)
  bucket=state['release_bucket'];key='dashboard-recovery/'+digest+'.tar.gz'
  aws('s3api','put-object','--bucket',bucket,'--key',key,'--body',str(archive),'--server-side-encryption','AES256')
  url=subprocess.run(['aws','s3','presign',f's3://{bucket}/{key}','--expires-in','3600','--profile',PROFILE,'--region',REGION],capture_output=True,text=True,check=True).stdout.strip()
  result=send(BUILD,{'url':url,'digest':digest},'CNC bounded bootstrap verification and build')
  record.write_text(json.dumps(result,indent=2));record.chmod(0o600)


if __name__=='__main__':main()
