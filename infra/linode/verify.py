#!/usr/bin/env python3
"""Verify private worker registration, cloud integration, public auth and a smoke job."""
import base64,json,subprocess,time,urllib.request,urllib.error,http.cookiejar
from pathlib import Path
ROOT=Path(__file__).resolve().parents[2]
STATE=ROOT/'.linode'
state=json.loads((STATE/'state.json').read_text())
access=json.loads((STATE/'dashboard-access.json').read_text())
base='http://'+state['servers']['cnc-linode-server']['public_ip']
opener=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
def request(path,body=None):
 req=urllib.request.Request(base+path,data=None if body is None else json.dumps(body).encode(),headers={'Origin':base,'Content-Type':'application/json'})
 with opener.open(req,timeout=60) as response:return json.load(response)
try:
 urllib.request.urlopen(base+'/api/workers',timeout=15)
 raise RuntimeError('Dashboard has no authentication')
except urllib.error.HTTPError as error:
 if error.code!=401:raise
request('/api/auth/login',{'username':access['username'],'password':access['password']})
with opener.open(base+'/dashboard/server',timeout=30) as response:
 if response.status!=200:raise RuntimeError('Server UI unavailable')
settings=request('/api/linode/settings')
if settings!={'configured':True}:raise RuntimeError('Cloud integration not connected or token exposed')
catalog=request('/api/linode/catalog')
for plan,memory in [('g6-standard-4',8192),('g6-standard-2',4096)]:
 if not any(item['id']==plan and item['memory']==memory for item in catalog['types']):raise RuntimeError('Requested size missing')
cloud=request('/api/linode/instances')
for name,node in state['servers'].items():
 if not any(item['id']==node['id'] and item['type']==node['type'] and item['status']=='running' for item in cloud):raise RuntimeError('Cloud node not running: '+name)
expected={name for name in state['servers'] if '-worker-' in name}
workers=request('/api/workers')
if {item['id'] for item in workers if item['status'] in ('online','busy')}!=expected:raise RuntimeError('Worker registration mismatch')
job=request('/api/jobs',{'name':'Linode deployment smoke test','mode':'broadcast','command':'printf cnc-linode-git-ok'})
deadline=time.monotonic()+60
while True:
 result=request('/api/jobs/'+job['job_id'])
 if result['status'] in ('completed','failed','cancelled'):break
 if time.monotonic()>deadline:raise RuntimeError('Smoke job timeout')
 time.sleep(2)
tasks=request('/api/jobs/'+job['job_id']+'/tasks')
if result['status']!='completed' or len(tasks)!=6 or {task['assigned_to'] for task in tasks}!=expected or any(task['status']!='completed' or task.get('result',{}).get('exit_code')!=0 or task.get('result',{}).get('stdout')!='cnc-linode-git-ok' for task in tasks):raise RuntimeError('Smoke job did not succeed on all six workers')
state.update(smoke_job_id=job['job_id'],smoke_tasks_completed=len(tasks),linode_integration_verified=True)
(STATE/'state.json').write_text(json.dumps(state,indent=2)+'\n');(STATE/'state.json').chmod(0o600)
print(json.dumps({'dashboard':base+'/dashboard','cloud_nodes_running':len(state['servers']),'workers_online':len(expected),'smoke_tasks_completed':len(tasks),'integration':'verified'}))
