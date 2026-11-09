#!/usr/bin/env python3
"""Controller-side owned fixtures; no private tools or external targets."""
import http.cookiejar
import json
from pathlib import Path
import shlex
import time
import urllib.request
import uuid

base = 'http://127.0.0.1:3000'
opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))


def api(path, body=None):
    request = urllib.request.Request(base+path,data=None if body is None else json.dumps(body).encode(),headers={'Content-Type':'application/json','Origin':base})
    return json.load(opener.open(request,timeout=20))


def read_env(path):
    result = {}
    for line in Path(path).read_text().splitlines():
        k,_,v=line.partition('=');result[k]=v.strip('"')
    return result


def cluster():
    env=read_env('/opt/cnc/processor.env')
    request=urllib.request.Request(env['CNC_PROCESSOR_BROKER_URL'].rstrip('/')+'/api/processor/cluster/workers',headers={'Authorization':'Bearer '+env['CNC_PROCESSOR_TOKEN']})
    return json.load(urllib.request.urlopen(request,timeout=15))


def main():
    login=read_env('/opt/cnc/login.env')
    api('/api/auth/login',{'username':login['CNC_LOGIN_USERNAME'],'password':login['CNC_LOGIN_PASSWORD']})
    initial=cluster()
    assert len(initial)==6 and all(w['status']=='online' and w['current_load']==0 for w in initial)
    tag='memory-burst-'+uuid.uuid4().hex
    name=tag+'.txt'
    config=json.loads(Path('/opt/cnc/config.json').read_text())
    merged=Path(config['gdrive']['watch_dir']).expanduser()/name
    code="""import json,pathlib,socket,time
total=int(next(x.split()[1] for x in pathlib.Path('/proc/meminfo').read_text().splitlines() if x.startswith('MemTotal:')))//1024
allocated=total*70//100+32
data=bytearray(allocated*1024*1024)
cg=pathlib.Path('/proc/self/cgroup').read_text().strip().split(':',2)[2]
p=pathlib.Path('/sys/fs/cgroup')/cg.lstrip('/')
total=int(next(x.split()[1] for x in pathlib.Path('/proc/meminfo').read_text().splitlines() if x.startswith('MemTotal:')))//1024
proof={'worker':socket.gethostname(),'total_mb':total,'child_mb':int((p/'memory.max').read_text())//(1024*1024),'pool_mb':int((p.parent/'memory.max').read_text())//(1024*1024),'soft_mb':total*70//100,'kernel_high':(p.parent/'memory.high').read_text().strip(),'swap':int((p.parent/'memory.swap.max').read_text()),'allocated_mb':allocated,'cgroup':cg}
pathlib.Path(OUTPUT).write_text(json.dumps(proof)+'\\n')
time.sleep(20)
""".replace('OUTPUT',repr(name))
    def launch(command, output):
        r=api('/api/jobs',{'name':'Owned memory burst fixture','mode':'broadcast','command':command,'output_file':output,'workers':6,'timeout_seconds':150})
        return r.get('id') or r['job_id']
    first=launch('python3 -c '+shlex.quote(code),name)
    deadline=time.monotonic()+120
    while time.monotonic()<deadline:
        proofs=[json.loads(line) for line in merged.read_text().splitlines()] if merged.exists() else []
        if len(proofs)==6:break
        assert api('/api/jobs/'+first)['status'] not in ('failed','cancelled')
        time.sleep(.5)
    else:raise RuntimeError('Six live fixture outputs did not arrive')
    # Images can reuse hostnames. Broker assignment IDs identify the workers.
    assigned=api('/api/jobs/'+first+'/tasks')
    assert len(assigned)==6 and len({t['assigned_to'] for t in assigned})==6
    assert len({p['cgroup'] for p in proofs})==6
    for p in proofs:
        assert p['soft_mb']==p['total_mb']*70//100 and p['pool_mb']==min(p['total_mb']*85//100,p['total_mb']-512) and p['child_mb']==p['pool_mb'] and p['allocated_mb']>p['soft_mb'] and p['swap']==0 and p['kernel_high']=='max'
    assert api('/api/jobs/'+first)['status']=='running','Output was not live'
    # Give the five-second heartbeat interval time to report while jobs hold RAM.
    time.sleep(6)
    live=cluster()
    assert len(live)==6 and all(w['status']=='online' and w['current_load']==1 and w['resources']['job_memory_mb']>w['resources']['total_memory_mb']*70//100 and time.time()*1000-w['resources']['sampled_at']<10000 for w in live)
    second=launch("printf '"+tag+"-queued-success\\n'",tag+'-queued.txt')
    time.sleep(1)
    queued=api('/api/jobs/'+second+'/tasks')
    assert len(queued)==6 and all(t['status']=='pending' and 'memory' in t.get('queue_reason','') for t in queued),queued
    for job in [first,second]:
        deadline=time.monotonic()+120
        while time.monotonic()<deadline:
            state=api('/api/jobs/'+job)
            if state['status'] in ('completed','failed','cancelled'):
                assert state['status']=='completed',state['status']
                tasks=api('/api/jobs/'+job+'/tasks')
                assert len(tasks)==6 and all(t['status']=='completed' and t['retry_count']==0 for t in tasks)
                break
            time.sleep(.5)
        else:raise RuntimeError('Fixture job did not finish: '+job)
    assert len(merged.read_text().splitlines())==6
    final=cluster()
    assert len(final)==6 and all(w['status']=='online' and w['current_load']==0 for w in final)
    print(json.dumps({'fixture_jobs':[first,second],'workers_verified':6,'jobs_ram_percent':70,'emergency_limit_percent':85,'minimum_worker_memory_mb':512,'shared_pool_mb':sorted({p['pool_mb'] for p in proofs}),'child_limit_mb_range':[min(p['child_mb'] for p in proofs),max(p['child_mb'] for p in proofs)],'allocated_mb_per_worker':sorted({p['allocated_mb'] for p in proofs}),'live_results':True,'heartbeats_while_allocated':True,'second_job_queued_for_memory':True,'queued_job_completed_after_release':True,'retries':0,'duplicate_results':False,'private_tools_accessed':False}))


if __name__=='__main__':
    main()
