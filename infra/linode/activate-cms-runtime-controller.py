#!/usr/bin/env python3
"""Activate a reviewed staged CMS/runtime release; explicit approval required."""
import sys
if '--apply' not in sys.argv:
 raise SystemExit('No production changes made. Explicit owner approval and --apply are required.')
import hashlib,http.cookiejar,json,os,shutil,sqlite3,subprocess,time,urllib.request,urllib.error
from pathlib import Path
app=Path('/opt/cnc-controller');proc=Path('/opt/cnc-processor');stage=app/'cms-runtime-staging';m=json.loads((stage/'manifest.json').read_text());release=app/'releases'/m['release'];backup=release/'rollback'
def sha(p):return hashlib.sha256(p.read_bytes()).hexdigest()
def envfile(p):return dict(line.split('=',1) for line in p.read_text().splitlines() if '=' in line and not line.startswith('#'))
env=envfile(app/'processor.env');op=urllib.request.build_opener(urllib.request.ProxyHandler({}));base='http://192.168.136.27:8080'
def call(path,processor=False):
 req=urllib.request.Request(('http://127.0.0.1:9191' if processor else base)+path,headers={'Authorization':'Bearer '+env['CNC_PROCESSOR_TOKEN']} if processor else {})
 with op.open(req,timeout=15) as response:return json.load(response)
health=call('/v1/health',True);jobs=call('/api/jobs');jobs=jobs if isinstance(jobs,list) else jobs.get('jobs',[]);workers=call('/api/workers');ids={w['id'] for w in workers}
if health['enabled'] or health['counts'].get('running',0) or health['counts'].get('queued',0) or any(j['status'] in ('running','queued','pending') for j in jobs) or any(w.get('current_load',0) for w in workers):raise RuntimeError('Deployment requires paused idle processing')
if len(ids)!=7 or not all(w['status']=='online' for w in workers):raise RuntimeError('Worker inventory unhealthy')
pins=env.get('CNC_PROCESSOR_PINNED_PROGRAMS','')
if not pins:raise RuntimeError('Retained program pins required; no program-source access permitted')
for name in ['cnc-server','cnc-worker','cnc-processor','cnc-enum']:
 if sha(stage/name)!=m['sha256'][name]:raise RuntimeError('Binary checksum mismatch')
backup.mkdir(mode=0o700)
shutil.copy2(app/'cnc-server',backup/'cnc-server');shutil.copy2(proc/'cnc-processor',backup/'cnc-processor')
for source,target in [(app/'cnc-worker-linux','cnc-worker-linux'),(Path('/opt/cnc')/'tools/cnc-enum','cnc-enum')]:
 if source.exists():shutil.copy2(source,backup/target)
with sqlite3.connect(proc/'processor.db') as source,sqlite3.connect(backup/'processor.db') as dest:
 source.backup(dest);versions=dict(source.execute('SELECT program_id,version FROM processor_programs'))
old_templates=call('/v1/version-check-templates',True)
package=Path('/opt/cnc/worker-tools.tar.gz')
if package.exists():shutil.copy2(package,backup/'worker-tools.tar.gz')
validation=release/'retained-validation';validation.mkdir(mode=0o700);shutil.copy2(proc/'config.json',validation/'config.json')
(validation/'queue-v2').symlink_to(proc/'queue-v2',target_is_directory=True)
with sqlite3.connect(backup/'processor.db') as source,sqlite3.connect(validation/'processor.db') as dest:source.backup(dest)
venv=dict(os.environ,CNC_APP_DIR=str(validation),CNC_PROCESSOR_DB=str(validation/'processor.db'),CNC_PROCESSOR_PINNED_PROGRAMS=pins)
r=subprocess.run([str(stage/'cnc-processor'),'validate-retained'],env=venv,capture_output=True,text=True)
if r.returncode or not json.loads(r.stdout).get('retained_config_valid'):raise RuntimeError('Retained processor config validation failed')
expected=json.loads((stage/'default-version-checks.json').read_text())
with sqlite3.connect(validation/'processor.db') as db:
 defaults=db.execute('SELECT checks FROM version_check_templates WHERE is_default=1').fetchall()
 if len(defaults)!=1 or json.loads(defaults[0][0])!=expected:raise RuntimeError('Default template migration failed in copied database')
new_ui=release/'ui-runtime';shutil.copytree(release/'ui/.next/standalone',new_ui,symlinks=True);shutil.copytree(release/'ui/.next/static',new_ui/'.next/static',dirs_exist_ok=True)
if (release/'ui/public').is_dir():shutil.copytree(release/'ui/public',new_ui/'public',dirs_exist_ok=True)
if not (new_ui/'server.js').is_file():raise RuntimeError('UI entry missing')
changed=False
try:
 subprocess.run(['systemctl','stop','cnc-ui','cnc-server','cnc-processor'],check=True,timeout=50)
 (app/'ui').rename(backup/'ui');new_ui.rename(app/'ui');changed=True
 for source,target in [(stage/'cnc-server',app/'cnc-server'),(stage/'cnc-processor',proc/'cnc-processor'),(stage/'cnc-worker',app/'cnc-worker-linux'),(stage/'cnc-enum',Path('/opt/cnc')/'tools/cnc-enum')]:
  tmp=target.with_name(target.name+'.runtime-new');shutil.copy2(source,tmp);tmp.chmod(0o700);tmp.replace(target)
 if (backup/'worker-tools.tar.gz').exists():
  # Update only the canonical scanner and its manifest in the existing generic download.
  import tarfile,io
  with tarfile.open(backup/'worker-tools.tar.gz','r:gz') as old,tarfile.open(package.with_suffix('.runtime-new'),'w:gz') as new:
   seen_scanner=False;seen_manifest=False
   for member in old.getmembers():
    if not member.isfile():raise RuntimeError('Unexpected generic package member')
    content=old.extractfile(member).read()
    if member.name=='tools/cnc-enum':content=(stage/'cnc-enum').read_bytes();seen_scanner=True
    elif member.name=='tools/.cnc-manifest.json':
     metadata=json.loads(content)
     item=next(f for f in metadata['files'] if f['path']=='cnc-enum')
     item['sha256']=sha(stage/'cnc-enum');content=json.dumps(metadata,sort_keys=True).encode();seen_manifest=True
    member.size=len(content);new.addfile(member,io.BytesIO(content))
   if not seen_scanner or not seen_manifest:raise RuntimeError('Generic scanner package incomplete')
  package.with_suffix('.runtime-new').chmod(0o600);package.with_suffix('.runtime-new').replace(package)
 subprocess.run(['systemctl','start','cnc-processor','cnc-server','cnc-ui'],check=True,timeout=50)
 deadline=time.monotonic()+70
 while True:
  try:
   now=call('/v1/health',True);online=call('/api/workers')
   if not now['enabled'] and now['counts']==health['counts'] and {w['id'] for w in online if w['status']=='online'}==ids:break
  except Exception:pass
  if time.monotonic()>deadline:raise RuntimeError('Service recovery failed')
  time.sleep(2)
 templates=call('/v1/version-check-templates',True);defaults=[t for t in templates if t['is_default']]
 if len(defaults)!=1 or defaults[0]['checks']!=expected:raise RuntimeError('Deployed default does not match requested template')
 for old in old_templates:
  new=next((t for t in templates if t['id']==old['id']),None)
  if new is None or new['checks']!=old['checks']:raise RuntimeError('Prior template data changed')
 with sqlite3.connect(proc/'processor.db') as db:
  if dict(db.execute('SELECT program_id,version FROM processor_programs'))!=versions:raise RuntimeError('Program version changed')
 jar=http.cookiejar.CookieJar();client=urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPCookieProcessor(jar));ui='http://127.0.0.1:3000';login=envfile(app/'login.env')
 req=urllib.request.Request(ui+'/api/auth/login',data=json.dumps({'username':login['CNC_LOGIN_USERNAME'],'password':login['CNC_LOGIN_PASSWORD']}).encode(),headers={'Content-Type':'application/json','Origin':ui},method='POST')
 with client.open(req,timeout=15) as response:
  if response.status!=200:raise RuntimeError('UI sign-in failed')
 with client.open(ui+'/api/version-check-templates',timeout=15) as response:
  if [t for t in json.load(response) if t['is_default']][0]['checks']!=expected:raise RuntimeError('UI template mismatch')
 with client.open(ui+'/default-version-checks.txt',timeout=15) as response:
  text=response.read().decode();local=(release/'ui/public/default-version-checks.txt').read_text()
  if text!=local or 'translatepress-multilingual <= 3.3.1' not in text:raise RuntimeError('Downloaded default mismatch')
 try:op.open(ui+'/api/version-check-templates',timeout=10);raise RuntimeError('Unauthenticated API accepted')
 except urllib.error.HTTPError as e:
  if e.code!=401:raise
 receipt={'release':m['release'],'default_checks':7,'previous_templates_preserved':True,'processor_paused':True,'processor_counts':now['counts'],'program_versions_preserved':True,'workers_online':7,'ui_default_and_download':'matched','authenticated_api':200,'unauthenticated_api':401,'rollback':str(backup),'private_tools_accessed':False,'sha256':{n:sha(stage/n) for n in ['cnc-server','cnc-worker','cnc-processor','cnc-enum']}}
 (release/'controller-verification.json').write_text(json.dumps(receipt,indent=2)+'\n');print(json.dumps(receipt))
except Exception:
 subprocess.run(['systemctl','stop','cnc-ui','cnc-server','cnc-processor'],timeout=50)
 for source,target in [(backup/'cnc-server',app/'cnc-server'),(backup/'cnc-processor',proc/'cnc-processor'),(backup/'cnc-worker-linux',app/'cnc-worker-linux'),(backup/'cnc-enum',Path('/opt/cnc')/'tools/cnc-enum')]:
  if source.exists():shutil.copy2(source,target)
 if (backup/'worker-tools.tar.gz').exists():shutil.copy2(backup/'worker-tools.tar.gz',Path('/opt/cnc/worker-tools.tar.gz'))
 with sqlite3.connect(backup/'processor.db') as source,sqlite3.connect(proc/'processor.db') as dest:source.backup(dest)
 if changed:(app/'ui').rename(release/'failed-ui');(backup/'ui').rename(app/'ui')
 subprocess.run(['systemctl','start','cnc-processor','cnc-server','cnc-ui'],timeout=50)
 raise
