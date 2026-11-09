"""Controller-side local-only end-to-end execution verification, two broadcast rounds plus a spread round."""
import json,pathlib,time,urllib.request
P=pathlib.Path
c=json.loads(P('/opt/cnc/config.json').read_text());base='http://'+c['http_addr'];o=urllib.request.build_opener(urllib.request.ProxyHandler({}))
def api(path,body=None):
 req=urllib.request.Request(base+'/api/'+path,data=None if body is None else json.dumps(body).encode(),headers={'Content-Type':'application/json'})
 with o.open(req,timeout=20) as r:return json.load(r)
fixture=P('/opt/cnc/data/integration-local-fixture.txt');fixture.write_text(''.join('CNC-LOCAL-FIXTURE-'+str(i)+'\n' for i in range(11)))
command='set -eu; test -s "{input}"; ./tools/dork2 --help > cli-help.txt 2>&1; grep -q "Usage: dork2" cli-help.txt; printf "CNC_LOCAL_READY\\n"; sha256sum "{input}"'
results=[]
for number in (1,2,3):
 mode='broadcast' if number<3 else 'spread'
 selected_command=command if mode=='spread' else 'set -eu; ./tools/dork2 --help > cli-help.txt 2>&1; grep -q "Usage: dork2" cli-help.txt; printf "CNC_LOCAL_READY\\n"'
 job=api('jobs',{'name':'Integration local CLI '+mode+' round '+str(number),'mode':mode,'input_file':str(fixture) if mode=='spread' else '', 'command':selected_command,'workers':11,'timeout_seconds':30})['job_id']
 print(json.dumps({'round':number,'job_id':job,'status':'submitted'}),flush=True)
 deadline=time.monotonic()+120
 while True:
  current=api('jobs/'+job);tasks=api('jobs/'+job+'/tasks')
  if current['status'] not in ('pending','running'):break
  if time.monotonic()>deadline:raise RuntimeError('Verification still active; inspect job '+job+' without replay')
  time.sleep(2)
 rows=[{'worker':t.get('assigned_to'),'status':t.get('status'),'exit_code':(t.get('result') or {}).get('exit_code'),'marker_present':'CNC_LOCAL_READY' in ((t.get('result') or {}).get('stdout',''))} for t in tasks]
 result={'round':number,'job_id':job,'status':current['status'],'completed':current['completed'],'failed':current['failed'],'tasks':rows}
 results.append(result);print(json.dumps(result),flush=True)
 assert len(rows)==11 and all(x['status']=='completed' and x['exit_code']==0 and x['marker_present'] for x in rows),'Spread verification failed'
 if mode=='broadcast':assert {x['worker'] for x in rows if x['worker'].startswith('cnc-linode-')}=={'cnc-linode-worker-'+str(i) for i in range(1,9)},'Not every new worker was exercised'
p=P('/opt/cnc/data/linode/spread-readiness-verification.json');p.write_text(json.dumps(results,indent=2));p.chmod(0o600)
print(json.dumps({'rounds_passed':3,'minimum_new_worker_task_successes':16,'all_worker_task_successes':33,'external_queries':0}))
