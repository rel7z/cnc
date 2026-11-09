#!/usr/bin/env python3
"""Live no-argument manifest fixtures, selected outputs and Drive recovery.

Runs only unrestricted examples; preserves private registered versions.
"""
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
import shutil

app=Path('/opt/cnc-processor')
settings=dict(line.split('=',1) for line in Path('/opt/cnc/processor.env').read_text().splitlines() if '=' in line and not line.startswith('#'))
token=settings['CNC_PROCESSOR_TOKEN'].strip('"')
watch=Path(settings['CNC_WATCH_DIR'].strip('"')).expanduser()
config_path=app/'config.json'
original=config_path.read_bytes()
name='manifest-proof-'+uuid.uuid4().hex
source_name=name+'-input.txt'
result_name=name+'.txt'
chain_name=name+'-chain.txt'
producer_id=name+'-producer';consumer_id=name+'-consumer'
producer=app/'examples'/(name+'-producer.py')
consumer=app/'examples'/(name+'-consumer.py')
source=watch/source_name
headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'}
server_config=json.loads(Path('/opt/cnc/config.json').read_text())
gdrive=server_config['gdrive']
access=None


def api(path,body=None,method=None):
 request=urllib.request.Request('http://127.0.0.1:9191/v1/'+path,data=None if body is None else json.dumps(body).encode(),headers=headers,method=method or ('GET' if body is None else 'PUT'))
 with urllib.request.urlopen(request,timeout=15) as response:return json.load(response)


def ready():
 for _ in range(40):
  try:return api('health')
  except OSError:time.sleep(.5)
 raise RuntimeError('processor failed to start')


def drive_request(path,query=None):
 global access
 token_path=Path(gdrive.get('token_path') or str(watch/'.gdrive_token.json')).expanduser()
 if access is None:
  access=json.loads(token_path.read_text())['access_token']
 url='https://www.googleapis.com/drive/v3/'+path
 if query:url+='?'+urllib.parse.urlencode(query)
 for attempt in range(2):
  try:
   request=urllib.request.Request(url,headers={'Authorization':'Bearer '+access})
   with urllib.request.urlopen(request,timeout=15) as response:return response.read()
  except urllib.error.HTTPError as error:
   code=error.code;error.close()
   if code!=401 or attempt:raise
   stored=json.loads(token_path.read_text())
   data=urllib.parse.urlencode({'grant_type':'refresh_token','refresh_token':stored['refresh_token'],'client_id':gdrive['client_id'],'client_secret':gdrive['client_secret']}).encode()
   with urllib.request.urlopen('https://oauth2.googleapis.com/token',data=data,timeout=15) as response:access=json.load(response)['access_token']


def drive_file(filename):
 query="trashed=false and name='"+filename+"'"
 if gdrive.get('parent_folder_id'):query+=" and '"+gdrive['parent_folder_id']+"' in parents"
 files=json.loads(drive_request('files',{'q':query,'fields':'files(id,name,size)'}))['files']
 if not files:return None,None
 if len(files)!=1:raise AssertionError('duplicate Drive filenames for fixture')
 return files[0]['id'],drive_request('files/'+files[0]['id'],{'alt':'media'}).decode()



# Two independent unchanged scripts both create results.txt.
if gdrive.get('delete_after_upload') or not gdrive.get('enabled'):
 raise RuntimeError('Verification requires an enabled Drive uploader retaining local files')
initial=api('health')['enabled']
restore_enabled=initial
if os.environ.get('CNC_QUEUE_ROLLBACK_STATE'):
 restore_enabled=json.loads(Path(os.environ['CNC_QUEUE_ROLLBACK_STATE']).read_text())['enabled']
identifiers=[name+'--scanner',name+'--checker']
fixture_dir=app/'examples'/name
fixture=fixture_dir/'scanner.py'
files=[identifier+'__results.txt' for identifier in identifiers]
summary_files=[]
proof={'fixture':name,'folder_manifests':True,'programs':identifiers,'helper_imports':False,'command_line_arguments':False}
succeeded=False
drive_restored=True

def fixture_runs():
 with sqlite3.connect(app/'processor.db',timeout=15) as db:
  return db.execute("SELECT r.id,r.program_id,r.status,COALESCE(a.worker_id,''),(SELECT COUNT(*) FROM processor_attempts WHERE run_id=r.id) FROM runs r LEFT JOIN processor_attempts a ON a.id=(SELECT id FROM processor_attempts WHERE run_id=r.id ORDER BY created_at DESC,id DESC LIMIT 1) WHERE r.program_id IN ("+','.join('?' for _ in identifiers)+") ORDER BY r.id",identifiers).fetchall()

def drive_enabled(enabled):
 config=dict(gdrive,enabled=enabled)
 request=urllib.request.Request('http://'+server_config['http_addr']+'/api/config/gdrive',data=json.dumps(config).encode(),headers={'Content-Type':'application/json'},method='POST')
 with urllib.request.urlopen(request,timeout=30) as response:assert json.load(response)['status']=='ok'

try:
 api('processing-state',{'enabled':False})
 if api('health')['counts']['running']:raise RuntimeError('verification requires idle processor')
 fixture_dir.mkdir(parents=True,exist_ok=True)
 (fixture_dir/'nested').mkdir()
 script="import pathlib,sys,time\nassert len(sys.argv)==1\np=pathlib.Path(__file__).resolve().parent\nassert pathlib.Path.cwd()==p\nlines=(p/'targets.txt').read_text().splitlines(True)\nwith (p/'results.txt').open('w') as f:\n f.write(lines[0]);f.flush()\n time.sleep(2 if ':recovery' in lines[0] else 45)\n f.writelines(lines[1:]);f.flush()\n(p/'ignored.txt').write_text('must not publish\\n')\n"
 fixture.write_text(script)
 (fixture_dir/'nested/checker.py').write_text(script)
 (fixture_dir/'tool.json').write_text(json.dumps({'input':'targets.txt','scripts':{'scanner':{'path':'scanner.py','outputs':['results.txt','errors.txt']},'checker':{'path':'nested/checker.py','outputs':['results.txt']}}}))
 config=json.loads(original)
 config['routes'][source_name]=[name]
 config['programs'][name]=str((fixture_dir/'tool.json').relative_to(app))
 config_path.write_text(json.dumps(config))
 subprocess.run(['systemctl','restart','cnc-processor'],check=True);ready()
 deadline=time.monotonic()+30
 while True:
  workers=api('cluster')['workers']
  if len(workers)==6 and all(w['processor_protocol']=='processor-v2' and w['resources']['sampled_at']>0 and w['resources']['cgroups'] for w in workers):break
  if time.monotonic()>deadline:raise AssertionError('six enforced workers did not register')
  time.sleep(1)
 proof['workers']=[w['id'] for w in workers]
 source.write_text(''.join(name+':'+str(i)+'\n' for i in range(6000)))
 started=time.monotonic();api('processing-state',{'enabled':True})
 deadline=started+30
 while len(fixture_runs())<12:
  if time.monotonic()>deadline:raise AssertionError('expected six chunks for each of two scripts')
  time.sleep(.5)
 source.unlink(missing_ok=True)
 # Poll both Drive files while at least one job for each program is running.
 early={};max_running={};deadline=started+180
 while len(early)<2:
  current=fixture_runs()
  if any(r[2] in ('failed','interrupted') for r in current):raise AssertionError('fixture job failed: '+repr(current))
  for worker in proof['workers']:
   count=sum(r[2]=='running' and r[3]==worker for r in current)
   max_running[worker]=max(max_running.get(worker,0),count)
   if count>2:raise AssertionError('worker capacity exceeded')
  for identifier,filename in zip(identifiers,files):
   if identifier in early:continue
   file_id,text=drive_file(filename)
   if text and any(r[1]==identifier and r[2]=='running' for r in current):
    early[identifier]={'id':file_id,'filename':filename,'lines':len(text.splitlines()),'seconds':round(time.monotonic()-started,2)}
  if time.monotonic()>deadline:raise AssertionError('both outputs were not live on Drive: '+repr(early))
  time.sleep(1)
 proof['early_drive']=early
 deadline=started+300
 while True:
  current=fixture_runs()
  for worker in proof['workers']:
   count=sum(r[2]=='running' and r[3]==worker for r in current)
   max_running[worker]=max(max_running.get(worker,0),count)
   if count>2:raise AssertionError('worker capacity exceeded')
  if any(r[2] in ('failed','interrupted') for r in current):raise AssertionError('fixture failure: '+repr(current))
  if len(current)==12 and all(r[2]=='completed' for r in current):break
  if time.monotonic()>deadline:raise AssertionError('fixture completion timeout: '+repr(current))
  time.sleep(1)
 if len({r[3] for r in current})!=6:raise AssertionError('not all six workers used')
 expected={name+':'+str(i) for i in range(6000)}
 final={};deadline=time.monotonic()+120
 while len(final)<2:
  for identifier,filename in zip(identifiers,files):
   file_id,text=drive_file(filename)
   merged=(watch/filename).read_text() if (watch/filename).exists() else ''
   if text and set(text.splitlines())==expected and len(text.splitlines())==6000 and merged==text:
    final[identifier]={'id':file_id,'filename':filename,'lines':6000}
  if time.monotonic()>deadline:raise AssertionError('final output did not converge: '+repr(final))
  time.sleep(2)
 proof['final_drive']=final
 proof['max_running_per_worker']=max_running
 proof['initial_completed_jobs']=12
 proof['all_attempt_counts_one']=all(r[4]==1 for r in current)
 if not proof['all_attempt_counts_one']:raise AssertionError('fixture execution retried')
 # Controlled uploader outage: tools finish, merged retains new output,
 # then Drive catches up without launching another job.
 drive_restored=False;drive_enabled(False);time.sleep(5)
 recovery=name+':recovery'
 source.write_text(recovery+'\n')
 deadline=time.monotonic()+90
 while True:
  current=fixture_runs()
  if len(current)==14 and all(r[2]=='completed' for r in current):break
  if any(r[2] in ('failed','interrupted') for r in current):raise AssertionError('recovery fixture failed')
  if time.monotonic()>deadline:raise AssertionError('recovery fixture did not complete')
  time.sleep(1)
 source.unlink(missing_ok=True)
 for filename in files:
  merged=(watch/filename).read_text()
  if recovery not in merged.splitlines():raise AssertionError('merged missing output while Drive disabled')
  _,text=drive_file(filename)
  if recovery in text.splitlines():raise AssertionError('Drive uploaded while disabled')
 drive_enabled(True);drive_restored=True
 deadline=time.monotonic()+120
 while True:
  complete=True
  for filename in files:
   _,text=drive_file(filename)
   if not text or set(text.splitlines())!=expected|{recovery} or len(text.splitlines())!=6001:complete=False
  if complete:break
  if time.monotonic()>deadline:raise AssertionError('Drive did not recover')
  time.sleep(2)
 if len(fixture_runs())!=14 or any(r[4]!=1 for r in fixture_runs()):raise AssertionError('Drive recovery reran a tool')
 # None of the nested text files may be merged.
 if any((watch/(identifier+'__ignored.txt')).exists() for identifier in identifiers):raise AssertionError('nested text published')
 proof.update({'drive_pause_recovery':True,'final_lines_per_program':6001,'duplicate_lines':0,'completed_jobs':14,'job_ids':[r[0] for r in current]})
 succeeded=True
finally:
 try:
  api('processing-state',{'enabled':False})
  if not succeeded:
   for identifier,_,status,_,_ in fixture_runs():
    if status in ('queued','running'):
     try:api('runs/'+identifier+'/cancel',method='POST')
     except OSError:pass
 finally:
  if not drive_restored:drive_enabled(gdrive['enabled'])
  config_path.write_bytes(original);source.unlink(missing_ok=True)
  shutil.rmtree(fixture_dir)
  subprocess.run(['systemctl','restart','cnc-processor'],check=True);ready()
  api('processing-state',{'enabled':restore_enabled if succeeded else False})
if succeeded:
 proof['production_health']=api('health');proof['production_routes_restored']=True
 Path('/opt/cnc/queue-releases/folder-manifests-415d691f01f9baaf/verification.json').write_text(json.dumps(proof,indent=2))
 print(json.dumps(proof,indent=2))
