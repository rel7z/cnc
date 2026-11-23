"""Exercise a built dashboard against a fake Map API; never starts real backups."""
import argparse
from http.cookiejar import CookieJar
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import socket
import subprocess
import threading
import time
import urllib.error
import urllib.request


def verify(ui, node):
    calls = []
    class MapAPI(BaseHTTPRequestHandler):
        def log_message(self, *args): pass
        def do_GET(self):
            body = dict(is_backup_running=False, backup=dict(updated_at=1, data=dict(filename='fixture.tar.gz', bytes=123)), drive=dict(is_uploaded=True), key_path='must-not-be-exposed')
            self.reply(body)
        def do_POST(self):
            calls.append(self.path)
            self.reply(dict(success=True, status='started'))
        def reply(self, body):
            assert self.headers.get('Authorization') == 'Bearer fixture-backup-key'
            data=json.dumps(body).encode();self.send_response(200)
            self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(data)))
            self.end_headers();self.wfile.write(data)
    api=ThreadingHTTPServer(('127.0.0.1',0),MapAPI)
    threading.Thread(target=api.serve_forever,daemon=True).start()
    with socket.socket() as probe:
        probe.bind(('127.0.0.1',0));port=probe.getsockname()[1]
    base='http://127.0.0.1:'+str(port)
    env=dict(os.environ,PORT=str(port),HOSTNAME='127.0.0.1',CNC_LOGIN_USERNAME='fixture',CNC_LOGIN_PASSWORD='fixture-password',CNC_SESSION_SECRET='fixture-session-secret-for-testing-only',INTERNET_MAP_API_URL='http://127.0.0.1:'+str(api.server_port),INTERNET_MAP_API_KEY='fixture-backup-key')
    process=subprocess.Popen([str(node),str(ui/'server.js')],cwd=ui,env=env,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    opener=urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPCookieProcessor(CookieJar()))
    def request(path, data=None, origin=None):
        headers={'Origin':origin or base,'Content-Type':'application/json'}
        req=urllib.request.Request(base+path,data=json.dumps(data).encode() if data is not None else None,headers=headers)
        try:
            with opener.open(req,timeout=10) as response:return response.status,response.read()
        except urllib.error.HTTPError as error:return error.code,error.read()
    try:
        deadline=time.monotonic()+45
        while True:
            try:
                status,_=request('/api/controller/backup')
                if status==401:break
            except OSError:pass
            if process.poll() is not None or time.monotonic()>deadline:raise RuntimeError('Dashboard fixture did not start')
            time.sleep(0.2)
        assert request('/api/auth/login',dict(username='fixture',password='fixture-password'))[0]==200
        status,body=request('/api/controller/backup')
        data=json.loads(body)
        assert status==200 and data['latest']['uploaded'] and 'key_path' not in data
        assert calls==[], 'Reading status must not create backups'
        assert request('/api/controller/backup',{},origin='https://untrusted.invalid')[0]==403
        assert calls==[], 'Cross-origin requests must not create backups'
        status,body=request('/api/controller/backup',{})
        assert status==200 and json.loads(body)['success']
        assert calls==['/api/v1/database/bundle/create']
        status,body=request('/dashboard/server')
        assert status==200 and b'Backup sekarang' in body
        return dict(authenticated_status=True,unauthenticated_blocked=True,cross_origin_blocked=True,manual_click_forwarded_to_fake_api=True,button_rendered=True,real_backups_started=0)
    finally:
        process.terminate()
        try:process.wait(timeout=10)
        except subprocess.TimeoutExpired:process.kill();process.wait()
        api.shutdown();api.server_close()


if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--ui',type=Path,required=True)
    parser.add_argument('--node',type=Path,required=True)
    args=parser.parse_args()
    print(json.dumps(verify(args.ui,args.node)))
