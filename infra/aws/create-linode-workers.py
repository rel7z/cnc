#!/usr/bin/env python3
"""Create a reviewed eight-worker batch through the active controller API once."""
import base64,json,subprocess
from pathlib import Path
ROOT=Path(__file__).resolve().parents[2]
key=(ROOT/'.linode/id_ed25519.pub').read_text().strip()
payload={'label':'cnc-linode-worker','count':8,'region':'sg-sin-2','type':'g6-standard-4','image':'linode/ubuntu24.04','backups':False,'public_key':key}
script=r'''python3 - <<'CREATE'
import base64,json,pathlib,urllib.request,urllib.error
P=pathlib.Path
payload=json.loads(base64.b64decode('PAYLOAD'))
c=json.loads(P('/opt/cnc/config.json').read_text());o=urllib.request.build_opener(urllib.request.ProxyHandler({}));base='http://'+c['http_addr']+'/api/linode/'
def get(path):
 with o.open(base+path,timeout=60) as r:return json.load(r)
connection=get('connection');assert connection.get('network')=='WireGuard'
catalog=get('catalog');plan=next(p for p in catalog['types'] if p['id']==payload['type']);region=next(r for r in catalog['regions'] if r['id']==payload['region'])
assert plan['memory']==8192 and plan['vcpus']==4
price=next((p for p in plan.get('region_prices',[]) if p['id']==payload['region']),plan['price'])
assert price['monthly']<=48 and price['hourly']<=0.072,'Plan exceeds reviewed price'
assert 'Metadata' in region['capabilities']
existing=get('instances');labels=[payload['label']+'-'+str(i) for i in range(1,9)]
assert not any(n['label'] in labels for n in existing),'Existing batch requires reconciliation, not creation'
request=urllib.request.Request(base+'workers',data=json.dumps(payload).encode(),headers={'Content-Type':'application/json'},method='POST')
try:
 with o.open(request,timeout=900) as response:result=json.load(response);status=response.status
except urllib.error.HTTPError as e:
 print(json.dumps({'http_status':e.code,'creation_failed_or_uncertain':True}));raise SystemExit(1)
except Exception:
 print(json.dumps({'response_uncertain':True,'do_not_retry_creation':True}));raise SystemExit(1)
created=result.get('created',[])
metadata={'http_status':status,'requested':8,'created':[{k:n.get(k) for k in ('id','label','status','region','type','ipv4')} for n in created],'error':result.get('error'),'uncertain_label':result.get('uncertain_label'),'not_attempted':result.get('not_attempted',[]),'monthly_usd':8*price['monthly']}
p=P(c['data_dir'])/'linode/eight-worker-batch.json';p.write_text(json.dumps(metadata,indent=2));p.chmod(0o600)
print(json.dumps(metadata))
assert len(created)==8 and not result.get('error'),'Batch incomplete; inspect existing nodes before further action'
CREATE
'''.replace('PAYLOAD',base64.b64encode(json.dumps(payload).encode()).decode())
p=Path('/private/tmp/cnc-create-linode-workers.sh');p.write_text(script)
subprocess.run(['python3','/private/tmp/cnc-aws-ssm.py',str(p)],check=True)
