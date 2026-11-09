#!/usr/bin/env python3
"""Run on the CNC server: automatic six-worker fixture and live Drive proof.

Creates fixtures under examples/, never inside the private tools directory.
Preserves the production route config; all result names are unique to this run.
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

app=Path('/opt/cnc-processor')
settings=dict(line.split('=',1) for line in Path('/opt/cnc/processor.env').read_text().splitlines() if '=' in line and not line.startswith('#'))
token=settings['CNC_PROCESSOR_TOKEN'].strip('"')
watch=Path(settings['CNC_WATCH_DIR'].strip('"')).expanduser()
config_path=app/'config.json'
original=config_path.read_bytes()
name='queue-proof-'+uuid.uuid4().hex
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


def runs():
 with sqlite3.connect(app/'processor.db',timeout=15) as db:
  return db.execute("SELECT r.id,r.program_id,r.status,COALESCE(a.worker_id,'') FROM runs r LEFT JOIN processor_attempts a ON a.id=(SELECT id FROM processor_attempts WHERE run_id=r.id ORDER BY created_at DESC,id DESC LIMIT 1) WHERE r.program_id IN (?,?) ORDER BY r.id",(producer_id,consumer_id)).fetchall()


if gdrive.get('delete_after_upload'):
 raise RuntimeError('Fixture verification requires retaining source files during the temporary route configuration')
initial=api('health')['enabled']
restore_enabled=initial
if os.environ.get('CNC_QUEUE_ROLLBACK_STATE'):
 restore_enabled=json.loads(Path(os.environ['CNC_QUEUE_ROLLBACK_STATE']).read_text())['enabled']
proof={'fixture':name}
succeeded=False
try:
 api('processing-state',{'enabled':False})
 if api('health')['counts']['running']:raise RuntimeError('verification requires idle processor')
 producer.parent.mkdir(parents=True,exist_ok=True)
 producer.write_text("import pathlib,sys,time\nfrom processor_publish import publish\np=pathlib.Path(sys.argv[2],"+repr(result_name)+")\nlines=pathlib.Path(sys.argv[1]).read_text().splitlines(True)\np.write_text(lines[0])\npublish(p)\nprint('first result published',flush=True)\ntime.sleep(35)\nwith p.open('a') as f:f.writelines(lines[1:])\npublish(p)\n")
 consumer.write_text("import pathlib,sys\nfrom processor_publish import publish\np=pathlib.Path(sys.argv[2],"+repr(chain_name)+")\np.write_text(''.join('chain:'+line for line in pathlib.Path(sys.argv[1]).read_text().splitlines(True)))\npublish(p)\n")
 config={'execution_mode':'queue','worker_limits':{'max_parallel':2,'cpu_percent':80,'reserve_memory_mb':512},'routes':{source_name:[producer_id],result_name:[consumer_id],chain_name:[producer_id,consumer_id]},'programs':{}}
 for identifier,path in [(producer_id,producer),(consumer_id,consumer)]:
  config['programs'][identifier]={'entrypoint':str(path.relative_to(app)),'files':[],'cpu_cores':1,'memory_mb':512,'threads':1,'timeout_seconds':-1,'max_parallel':6,'retry_safe':False}
 config_path.write_text(json.dumps(config))
 subprocess.run(['systemctl','restart','cnc-processor'],check=True);ready()
 deadline=time.monotonic()+30
 while True:
  cluster=api('cluster');workers=cluster['workers']
  if len(workers)==6 and all(w['processor_protocol']=='processor-v2' and w['resources']['sampled_at']>0 for w in workers):break
  if time.monotonic()>deadline:raise AssertionError('six v2 workers did not register')
  time.sleep(1)
 proof['worker_resources']=[{'id':w['id'],'cpu_cores':w['resources']['cpu_cores'],'cgroups':w['resources']['cgroups'],'available_memory_mb':w['resources']['available_memory_mb']} for w in workers]
 source.write_text(''.join(name+':'+str(i)+'\n' for i in range(6000)))
 started=time.monotonic();api('processing-state',{'enabled':True})
 deadline=time.monotonic()+25
 while True:
  current=runs();primary=[r for r in current if r[1]==producer_id]
  if len(primary)==6:
   source.unlink(missing_ok=True)
   break
  if time.monotonic()>deadline:raise AssertionError('automatic capture did not create six chunks')
  time.sleep(.5)
 deadline=started+33
 while True:
  identifier,text=drive_file(result_name)
  primary=[r for r in runs() if r[1]==producer_id]
  if identifier and text and any(r[2]=='running' for r in primary):
   proof['early_drive']={'id':identifier,'lines':len(text.splitlines()),'seconds':round(time.monotonic()-started,2),'producer_jobs_running':sum(r[2]=='running' for r in primary)}
   break
  if time.monotonic()>deadline:raise AssertionError('no Drive result while producers were still running')
  time.sleep(1)
 deadline=started+180
 while True:
  current=runs();primary=[r for r in current if r[1]==producer_id]
  if any(r[2] in ('failed','interrupted') for r in current):raise AssertionError('fixture failure: '+repr(current))
  if len(primary)==6 and len(current)==18 and all(r[2]=='completed' for r in current):break
  if time.monotonic()>deadline:raise AssertionError('queue completion timeout: '+repr(current))
  time.sleep(1)
 used={r[3] for r in primary}
 if len(used)!=6:raise AssertionError('producer chunks did not use all six workers: '+repr(used))
 expected={name+':'+str(i) for i in range(6000)}
 deadline=time.monotonic()+60
 while True:
  identifier,text=drive_file(result_name);chain_id,chain_text=drive_file(chain_name)
  if text and chain_text and set(text.splitlines())==expected and set(chain_text.splitlines())=={'chain:'+line for line in expected}:
   if len(text.splitlines())!=6000 or len(chain_text.splitlines())!=6000:raise AssertionError('duplicate Drive result lines')
   proof['final_drive']={'results_id':identifier,'chain_id':chain_id,'result_lines':6000,'chain_lines':6000};break
  if time.monotonic()>deadline:raise AssertionError('Drive final results did not converge')
  time.sleep(2)
 # Both final filenames route back to visited tools: ancestry must prevent cycles.
 time.sleep(3)
 if len(runs())!=18:raise AssertionError('tool chain generated a cycle')
 proof.update({'producer_chunks':6,'consumer_jobs':12,'workers_used':sorted(used),'duplicate_lines':0,'cycles':0,'job_ids':[r[0] for r in current]})
 succeeded=True
finally:
 try:
  api('processing-state',{'enabled':False})
  if not succeeded:
   for identifier,_,status,_ in runs():
    if status in ('queued','running'):
     try:api('runs/'+identifier+'/cancel',method='POST')
     except OSError:pass
 finally:
  config_path.write_bytes(original)
  source.unlink(missing_ok=True)
  producer.unlink(missing_ok=True);consumer.unlink(missing_ok=True)
  subprocess.run(['systemctl','restart','cnc-processor'],check=True);ready()
  api('processing-state',{'enabled':restore_enabled if succeeded else False})
if succeeded:
 proof['production_health']=api('health')
 proof['production_routes_restored']=True
 print(json.dumps(proof,indent=2))
