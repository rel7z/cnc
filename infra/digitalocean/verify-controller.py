#!/usr/bin/env python3
"""Verify new controller and harmless broadcast roundtrip; no scanners are run."""
import hashlib,http.cookiejar,json,subprocess,time,urllib.request,urllib.error
from pathlib import Path
APP=Path('/opt/cnc');STAGE=Path('/opt/cnc-deploy');manifest=json.loads((STAGE/'manifest.json').read_text());config=json.loads((APP/'config.json').read_text());base='http://'+config['http_addr'];op=urllib.request.build_opener(urllib.request.ProxyHandler({}))
def request(path,body=None):
 r=urllib.request.Request(base+path,data=json.dumps(body).encode() if body is not None else None,headers={'Content-Type':'application/json'})
 with op.open(r,timeout=12) as response:return json.load(response)
workers=request('/api/workers');expected={'cnc-do-worker-1','cnc-do-worker-2'}
if {w['id'] for w in workers if w['status']=='online'}!=expected:raise RuntimeError('Two online workers required')
# Print-only command executes in disposable task workspaces, not private programs.
check=STAGE/'verification-job.json'
if check.exists():job_id=json.loads(check.read_text())['job_id']
else:
 result=request('/api/jobs',{'name':'DigitalOcean deployment verification','mode':'broadcast','command':"printf 'cnc-digitalocean-ready\\n'",'timeout_seconds':10})
 job_id=result['job_id'];check.write_text(json.dumps({'job_id':job_id}))
deadline=time.monotonic()+40
while True:
 job=request('/api/jobs/'+job_id)
 if job['status']=='completed':break
 if job['status'] in ('failed','cancelled') or time.monotonic()>deadline:raise RuntimeError('Harmless verification job did not complete')
 time.sleep(1)
tasks=request('/api/jobs/'+job_id+'/tasks')
if len(tasks)!=2 or {t['assigned_to'] for t in tasks}!=expected or any(t['status']!='completed' or t.get('result',{}).get('exit_code')!=0 or t.get('result',{}).get('stdout','').strip()!='cnc-digitalocean-ready' for t in tasks):
 raise RuntimeError('Worker roundtrip output or exit code mismatch')
def envfile(path):return dict(l.split('=',1) for l in path.read_text().splitlines() if '=' in l and not l.startswith('#'))
env=envfile(APP/'processor.env');r=urllib.request.Request('http://127.0.0.1:9191/v1/health',headers={'Authorization':'Bearer '+env['CNC_PROCESSOR_TOKEN']})
with op.open(r,timeout=10) as response:health=json.load(response)
if health['enabled'] or health['configured_programs']!=0:raise RuntimeError('Fresh processor must be paused without private programs')
r=urllib.request.Request('http://127.0.0.1:9191/v1/version-check-templates',headers={'Authorization':'Bearer '+env['CNC_PROCESSOR_TOKEN']})
with op.open(r,timeout=10) as response:templates=json.load(response)
defaults=[t for t in templates if t['is_default']]
if len(defaults)!=1 or len(defaults[0]['checks'])!=7:raise RuntimeError('Seven-check default missing')
ui='http://127.0.0.1:3000';login=envfile(APP/'login.env');jar=http.cookiejar.CookieJar();client=urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPCookieProcessor(jar))
r=urllib.request.Request(ui+'/api/auth/login',data=json.dumps({'username':login['CNC_LOGIN_USERNAME'],'password':login['CNC_LOGIN_PASSWORD']}).encode(),headers={'Content-Type':'application/json','Origin':ui},method='POST')
with client.open(r,timeout=10) as response:
 if response.status!=200:raise RuntimeError('Dashboard login failed')
with client.open(ui+'/dashboard',timeout=10) as response:
 if response.status!=200:raise RuntimeError('Dashboard unavailable')
try:op.open(ui+'/api/workers',timeout=10);raise RuntimeError('Unauthenticated API accepted')
except urllib.error.HTTPError as e:
 if e.code!=401:raise
for unit in ['cnc-server','cnc-ui','cnc-processor']:subprocess.run(['systemctl','is-active','--quiet',unit],check=True)
result={'controller':'cnc-do-controller','services':'active','workers_online':2,'worker_ids':sorted(expected),'harmless_job_id':job_id,'harmless_job_status':'completed','successful_worker_tasks':len(tasks),'worker_output_verified':True,'dashboard_authenticated':200,'dashboard_unauthenticated_api':401,'processor_paused':True,'private_programs_configured':0,'default_version_checks':7,'private_tools_accessed':False,'server_sha256':hashlib.sha256((APP/'cnc-server').read_bytes()).hexdigest()}
if result['server_sha256']!=manifest['sha256']['bin/cnc-server']:raise RuntimeError('Controller binary mismatch')
(STAGE/'final-verification.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(result))
