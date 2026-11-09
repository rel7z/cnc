#!/usr/bin/env python3
"""Production acceptance against a temporary HTTP fixture on the CNC server.

Run only after explicit production rollout/fixture authorization. This script
creates its own input, job, results and Drive objects. It does not change CNC
configuration, Drive credentials, processor routes, or firewall rules. The
fixture port must already be reachable from the six workers. It never accesses
Kiro or processor/tools. Credentials are read internally and never printed.
"""
import argparse
import hashlib
import hmac
import http.server
import http.cookiejar
import ipaddress
import json
from pathlib import Path
import shlex
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid


def environment(path):
    result={}
    for line in Path(path).read_text().splitlines():
        if not line or line.startswith('#') or '=' not in line:continue
        key,value=line.split('=',1)
        parts=shlex.split(value)
        result[key]=parts[0] if len(parts)==1 else value
    return result


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--port',type=int,default=18083)
    parser.add_argument('--without-scope',action='store_true',help='Omit optional target authorization entirely')
    parser.add_argument('--dashboard',action='store_true',help='Sign in and launch through the real dashboard route')
    parser.add_argument('--output',required=True,help='Path for non-secret acceptance evidence')
    args=parser.parse_args()
    if args.port<1024 or args.port>65535:raise RuntimeError('Use a dedicated unprivileged fixture port')
    config=json.loads(Path('/opt/cnc/config.json').read_text())
    address=urllib.parse.urlsplit('http://'+config['http_addr'])
    host=address.hostname
    ip=ipaddress.ip_address(host)
    if not ip.is_private or ip.is_unspecified:raise RuntimeError('Fixture must bind the configured private server address')
    base='http://'+config['http_addr']
    drive=config['gdrive']
    if not drive.get('enabled') or drive.get('delete_after_upload'):raise RuntimeError('Acceptance needs an enabled uploader retaining local results')
    watch=Path(drive['watch_dir']).expanduser()
    secret=environment('/opt/cnc/enum-audit.env')['CNC_DASHBOARD_AUDIT_SECRET']
    actor=environment('/opt/cnc/login.env').get('CNC_LOGIN_USERNAME','fahrel')
    if len(secret)<32:raise RuntimeError('Invalid dashboard audit secret')
    def api(path):
        with urllib.request.urlopen(base+'/api/'+path,timeout=15) as response:return json.load(response)
    workers=api('workers')
    if len(workers)!=6 or any(w['status']!='online' or w['current_load'] for w in workers):raise RuntimeError('Expected six idle workers before fixture verification')
    root=Path('/opt/cnc/enum-fixtures')/('version-proof-'+uuid.uuid4().hex)
    root.mkdir(parents=True,mode=0o700)
    counts={'active':0,'peak':0,'requests':0}
    lock=threading.Lock()
    class Fixture(http.server.BaseHTTPRequestHandler):
        protocol_version='HTTP/1.1'
        def log_message(self,*arguments):pass
        def do_GET(self):
            with lock:
                counts['active']+=1;counts['requests']+=1
                counts['peak']=max(counts['peak'],counts['active'])
            try:
                time.sleep(.075)
                status,mime,body=404,'text/plain',b'owned missing fixture\n'
                if self.path=='/wp-json/':status,mime,body=200,'application/json',b'{"generator":{"name":"WordPress","version":"6.4.1"}}'
                elif self.path=='/':status,mime,body=200,'text/html',b'<html><meta name="generator" content="WordPress 6.4.1"></html>'
                elif self.path=='/readme.html':status,mime,body=200,'text/html',b'<html>WordPress<h1>Version 6.4.1</h1></html>'
                elif self.path=='/wp-content/plugins/fixture/readme.txt':status,mime,body=200,'text/plain',b'=== Owned fixture ===\nStable tag: 1.2.3\n'
                elif self.path=='/wp-content/plugins/fixture/package.json':status,mime,body=200,'application/json',b'{"name":"fixture","version":"1.3.0"}'
                self.send_response(status);self.send_header('Content-Type',mime);self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body)
            except (BrokenPipeError,ConnectionResetError):pass
            finally:
                with lock:counts['active']-=1
    server=http.server.ThreadingHTTPServer((host,args.port),Fixture)
    server.daemon_threads=True
    thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start()
    access=None
    def drive_request(path,query):
        nonlocal access
        token_path=Path(drive.get('token_path') or str(watch/'.gdrive_token.json')).expanduser()
        if access is None:access=json.loads(token_path.read_text())['access_token']
        url='https://www.googleapis.com/drive/v3/'+path+'?'+urllib.parse.urlencode(query)
        for attempt in range(2):
            try:
                request=urllib.request.Request(url,headers={'Authorization':'Bearer '+access})
                return urllib.request.urlopen(request,timeout=15)
            except urllib.error.HTTPError as error:
                status=error.code;error.close()
                if status!=401 or attempt:raise RuntimeError('Drive fixture verification request failed: '+str(status)) from None
                stored=json.loads(token_path.read_text())
                data=urllib.parse.urlencode({'grant_type':'refresh_token','refresh_token':stored['refresh_token'],'client_id':drive['client_id'],'client_secret':drive['client_secret']}).encode()
                with urllib.request.urlopen('https://oauth2.googleapis.com/token',data=data,timeout=15) as response:access=json.load(response)['access_token']
    def drive_files(job_id):
        query="trashed=false and name contains 'enum-versions-"+job_id+"-' and '"+(drive.get('parent_folder_id') or 'root')+"' in parents"
        with drive_request('files',{'q':query,'fields':'files(id,name,size)','pageSize':100}) as response:return json.load(response)['files']
    job_id=None
    try:
        input_path=root/'targets.txt'
        input_path.write_text(''.join(f'http://{host}:{args.port}/owned/{i}\n' for i in range(6001)))
        rules=[
            {'id':'core-range','name':'Owned core range','component':'wordpress:core','clauses':[{'operator':'>=','version':'6.4'},{'operator':'<','version':'7'}]},
            {'id':'core-exact','name':'Owned core exact','component':'wordpress:core','clauses':[{'operator':'=','version':'6.4.1'}]},
            {'id':'conflict','name':'Conflicting plugin must not match','component':'wordpress:plugin:fixture','clauses':[{'operator':'>=','version':'1'}]},
        ]
        payload={'tool_id':'enum','input_file':str(input_path),'mode':'spread','options':{'threads':8,'timeout':10},'version_rules':rules,'target_authorization':{'acknowledged':True,'scope':{'hosts':[],'cidrs':[str(ip)+'/'+str(ip.max_prefixlen)],'schemes':['http'],'ports':[args.port]}}}
        if args.without_scope:payload.pop('target_authorization')
        data=json.dumps(payload,separators=(',',':')).encode();stamp=str(int(time.time()*1000))
        signed=actor+'\n'+stamp+'\nPOST\n/api/tools/launch\n'+hashlib.sha256(data).hexdigest()
        signature=hmac.new(secret.encode(),signed.encode(),hashlib.sha256).hexdigest()
        request=urllib.request.Request(base+'/api/tools/launch',data=data,headers={'Content-Type':'application/json','X-CNC-Actor':actor,'X-CNC-Audit-Time':stamp,'X-CNC-Audit-Signature':signature},method='POST')
        if args.dashboard:
            dashboard='http://127.0.0.1:3000'
            login=environment('/opt/cnc/login.env')
            opener=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
            credentials=json.dumps({'username':login.get('CNC_LOGIN_USERNAME','fahrel'),'password':login.get('CNC_LOGIN_PASSWORD','fahrel')}).encode()
            login_request=urllib.request.Request(dashboard+'/api/auth/login',data=credentials,headers={'Content-Type':'application/json','Origin':dashboard})
            with opener.open(login_request,timeout=15) as response:assert json.load(response)['ok']
            request=urllib.request.Request(dashboard+'/api/tools/launch',data=data,headers={'Content-Type':'application/json','Origin':dashboard})
            with opener.open(request,timeout=30) as response:job_id=json.load(response)['job_id']
        else:
            with urllib.request.urlopen(request,timeout=30) as response:job_id=json.load(response)['job_id']
        started=time.monotonic();deadline=started+240
        maximum={};early_merged=False;early_drive=False;next_drive_check=started
        while True:
            job=next(j for j in api('jobs') if j['id']==job_id)
            tasks=[t for t in api('tasks') if t['job_id']==job_id]
            workers=api('workers')
            for worker in workers:
                for metric in (worker.get('enum_requests') or {}).values():
                    maximum[worker['id']]=max(maximum.get(worker['id'],0),metric['active'])
                    if metric['active']>8:raise AssertionError('Worker request budget exceeded')
            files=list(watch.glob('enum-versions-'+job_id+'-*.jsonl'))
            active=job['status'] not in ['completed','completed_with_errors','failed','cancelled']
            if files and active:early_merged=True
            if active and time.monotonic()>=next_drive_check:
                if drive_files(job_id):early_drive=True
                next_drive_check=time.monotonic()+1
            if job['status'] in ['failed','cancelled','completed_with_errors']:raise AssertionError('Fixture job did not complete cleanly')
            if job['status']=='completed' and len(tasks)==7 and all(t.get('enum_publication')=='accepted' for t in tasks):break
            if time.monotonic()>deadline:raise AssertionError('Fixture execution/publication deadline exceeded; check worker reachability of the private fixture port')
            time.sleep(.25)
        assert len({t['assigned_to'] for t in tasks})==6
        assert all(t['status']=='completed' and not t.get('retry_count') for t in tasks)
        assert job['enum']['actor']==actor and job['timeout_seconds']==-1
        if args.without_scope:
            assert not any(job['enum']['target_scope'].values())
            assert job['enum']['acknowledged_at']=='0001-01-01T00:00:00Z'
        assert early_merged and early_drive, 'Results were not visible in merged and Drive before job completion'
        hashes={};targets=set();record_count=0
        for path in files:
            digest=hashlib.sha256()
            with path.open('rb') as file:
                for line in file:
                    assert line.endswith(b'\n');digest.update(line);record=json.loads(line)
                    assert record['job_id']==job_id and record['observation']['component']=='wordpress:core'
                    assert record['observation']['version']=='6.4.1' and record['observation']['confidence']=='authoritative'
                    assert len(record['observation']['evidence_candidates'])==3
                    assert [rule['id'] for rule in record['matched_rules']]==['core-exact','core-range']
                    locator=record['target'];key=(locator['host'],locator['port'],locator['path'])
                    assert key not in targets;targets.add(key);record_count+=1
            hashes[path.name]=digest.hexdigest()
        assert record_count==6001 and len(hashes)==7
        final={};deadline=time.monotonic()+120
        while len(final)!=7:
            remote=drive_files(job_id)
            assert len({f['name'] for f in remote})==len(remote), 'Duplicate Drive filenames'
            for item in remote:
                if item['name'] not in hashes:raise AssertionError('Unexpected fixture filename on Drive')
                digest=hashlib.sha256()
                with drive_request('files/'+item['id'],{'alt':'media'}) as response:
                    while chunk:=response.read(64*1024):digest.update(chunk)
                if digest.hexdigest()==hashes[item['name']]:final[item['name']]={'id':item['id'],'sha256':digest.hexdigest()}
            if time.monotonic()>deadline:raise AssertionError('Drive bytes did not converge with merged bytes')
            if len(final)!=7:time.sleep(1)
        result={'target_scope_enabled':not args.without_scope,'dashboard_launch':args.dashboard,'job_id':job_id,'targets':6001,'records':record_count,'shards':7,'workers_used':6,'job_status':'completed','publications_accepted':7,'early_merged':early_merged,'early_drive':early_drive,'max_active_requests_per_worker':maximum,'fixture_peak':counts['peak'],'fixture_requests':counts['requests'],'conflicting_plugin_excluded':True,'drive_files':final,'fixture_root':str(root),'config_changed':False,'credentials_changed':False,'kiro_accessed':False,'private_tools_accessed':False}
        Path(args.output).write_text(json.dumps(result,indent=2)+'\n')
        print(json.dumps(result))
    except Exception:
        if job_id:
            try:
                current=next(j for j in api('jobs') if j['id']==job_id)
                if current['status'] not in ['completed','completed_with_errors','failed','cancelled']:
                    request=urllib.request.Request(base+'/api/jobs/'+job_id+'/cancel',data=b'',method='POST')
                    with urllib.request.urlopen(request,timeout=15) as response:response.read()
            except Exception:pass
        raise
    finally:
        server.shutdown();server.server_close()


if __name__=='__main__':main()
