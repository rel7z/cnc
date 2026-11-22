import importlib.util
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import tempfile
import subprocess
import sys
import threading
import unittest
from unittest.mock import patch
import urllib.error
import urllib.request

spec = importlib.util.spec_from_file_location('drive', Path(__file__).with_name('drive_oauth.py'))
drive = importlib.util.module_from_spec(spec); spec.loader.exec_module(drive)


class DriveTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(); self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.config = self.root / 'manager.json'
        self.access = self.root / 'access.json'; self.access.write_text(json.dumps(dict(username='fixture', password='fixture')))
        self.profile = self.root / 'profile.json'; self.profile.write_text(json.dumps(dict(client_id='fixture-client', client_secret='fixture-secret')))
        self.state = dict(config={'redirect_url': 'http://localhost:8123/api/config/gdrive/callback', 'client_id': 'fixture-client', 'client_secret': 'fixture-secret', 'enabled': False}, writes=0, used=False, headers={})
        self.first = self.controller(self.state)
        self.connection(self.first)

    def server(self, cls, handler):
        server = cls(('127.0.0.1', 0), handler)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        self.addCleanup(server.server_close); self.addCleanup(server.shutdown)
        return server

    def controller(self, state):
        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args): pass
            def send(self, status, payload, headers=None):
                data = json.dumps(payload).encode(); self.send_response(status)
                for key, value in (headers or {}).items(): self.send_header(key, value)
                self.send_header('Content-Length', str(len(data))); self.end_headers(); self.wfile.write(data)
            def do_GET(self):
                if self.path == '/api/config/gdrive/auth-status':
                    self.send(200, {'authorized': state.get('authorized', False)});return
                if self.path == '/api/config/gdrive':
                    cfg = dict(state['config']); secret = bool(cfg.pop('client_secret', ''));cfg['client_secret'] = ''
                    self.send(200, {'gdrive': cfg, 'client_secret_set': secret});return
                state['headers'] = dict(self.headers)
                if self.path.startswith('/drive-connect?'):
                    self.send(307, {}, {'Location': 'https://accounts.google.com/o/oauth2/v2/auth?state=fixture', 'Set-Cookie': 'cnc_drive_state=fixture; HttpOnly; Path=/api/config/gdrive; SameSite=Lax'});return
                if self.path.startswith('/api/config/gdrive/callback'):
                    valid = self.headers.get('Cookie') == 'cnc_drive_state=fixture' and not state['used']
                    state['used'] = True;self.send(200 if valid else 400, {'valid': valid});return
                self.send(404, {})
            def do_POST(self):
                data = json.loads(self.rfile.read(int(self.headers.get('Content-Length', '0'))))
                if self.path == '/api/auth/login':
                    self.send(200, {}, {'Set-Cookie': 'cnc_session=fixture; HttpOnly; Path=/'});return
                if self.headers.get('Cookie') != 'cnc_session=fixture': self.send(401, {});return
                if self.path == '/api/config/gdrive':
                    state['config'] = {**state['config'], **{k:v for k,v in data.items() if k != 'client_secret' or v}};state['writes'] += 1;self.send(200, {});return
                self.send(404, {})
        return self.server(ThreadingHTTPServer, Handler)

    def connection(self, server):
        self.config.write_text(json.dumps(dict(local_port=server.server_port, credentials_file=str(self.access))))

    def request(self, server, path, **headers):
        request = urllib.request.Request('http://127.0.0.1:' + str(server.server_port) + path, headers={'Host': 'localhost:8080', **headers})
        try: return drive.opener().open(request, timeout=3)
        except urllib.error.HTTPError as error: return error

    def test_callback_survives_port_switch_and_preserves_state_validation(self):
        proxy = self.server(drive.CallbackServer, drive.handler(self.config))
        with self.request(proxy, '/drive-connect?ticket=' + 'a'*32) as response:
            self.assertEqual(response.status, 307)
            self.assertIn('HttpOnly', response.headers['Set-Cookie'])
        self.assertEqual(self.state['headers']['X-Forwarded-Host'], 'localhost:8080')
        second_state = dict(config={}, used=False, headers={})
        self.connection(self.controller(second_state))
        for expected in (200, 400):
            with self.request(proxy, '/api/config/gdrive/callback?state=fixture&code=dummy', Cookie='cnc_drive_state=fixture; cnc_session=do-not-forward; unrelated=private') as response:
                self.assertEqual(response.status, expected)
        self.assertEqual(second_state['headers']['Cookie'], 'cnc_drive_state=fixture')
        self.assertFalse(self.state['used'])

    def test_callback_host_routes_and_cors_are_scoped(self):
        proxy = self.server(drive.CallbackServer, drive.handler(self.config))
        with self.request(proxy, '/_cnc-drive/health', Origin='http://localhost:' + str(self.first.server_port)) as response:
            self.assertEqual(json.load(response)['callback_url'], drive.CALLBACK_URL)
            self.assertIn('Access-Control-Allow-Origin', response.headers)
        with self.request(proxy, '/_cnc-drive/health', Origin='https://untrusted.example') as response:
            self.assertNotIn('Access-Control-Allow-Origin', response.headers)
        for path in ['/api/server/exec', '/api/config', '/drive-connect?authorization=bad']:
            with self.request(proxy, path) as response: self.assertIn(response.status, (400, 404))
        with self.request(proxy, '/_cnc-drive/health', Host='untrusted.example:8080') as response:
            self.assertEqual(response.status, 403)

    def test_saved_account_hint_follows_controller_changes_without_changing_state(self):
        (self.root/'drive-oauth-profile.json').write_text(json.dumps({'preferred_account':'owner@example.test'}))
        proxy=self.server(drive.CallbackServer,drive.handler(self.config))
        with self.request(proxy,'/drive-connect?ticket='+'a'*32) as response:
            query=urllib.parse.parse_qs(urllib.parse.urlsplit(response.headers['Location']).query)
            self.assertEqual(query['login_hint'],['owner@example.test'])
            self.assertEqual(query['state'],['fixture'])
        self.connection(self.controller(dict(config={},used=False,headers={})))
        with self.request(proxy,'/drive-connect?ticket='+'b'*32) as response:
            self.assertIn('login_hint=owner%40example.test',response.headers['Location'])

    def test_profile_applies_once_and_restores_new_controller_without_enabling(self):
        self.assertTrue(drive.sync_controller(self.config, self.profile)['updated'])
        self.assertEqual(self.state['config']['redirect_url'], drive.CALLBACK_URL)
        self.assertFalse(self.state['config']['enabled'])
        self.assertFalse(drive.sync_controller(self.config, self.profile)['updated'])
        self.assertEqual(self.state['writes'], 1)
        fresh = dict(config={'client_id': '', 'client_secret': '', 'enabled': False}, writes=0)
        self.connection(self.controller(fresh))
        drive.sync_controller(self.config, self.profile)
        self.assertEqual(fresh['config']['client_id'], 'fixture-client')
        self.assertEqual(fresh['config']['client_secret'], 'fixture-secret')
        self.assertEqual(fresh['config']['redirect_url'], drive.CALLBACK_URL)
        self.assertFalse(fresh['config']['enabled'])

    def test_fresh_controller_restores_session_before_applying_and_enables(self):
        profile=json.loads(self.profile.read_text())
        profile.update(enabled=True, oauth_token={'refresh_token':'fixture-refresh'})
        self.profile.write_text(json.dumps(profile))
        fresh=dict(config={'enabled':False},writes=0)
        self.connection(self.controller(fresh))
        def restore(*args):
            self.assertEqual(fresh['writes'],0)
            self.assertEqual(args[2],profile['oauth_token'])
            return {'restored':True}
        with patch.object(drive,'controller_session',side_effect=restore) as transfer:
            drive.sync_controller(self.config,self.profile)
        self.assertEqual(transfer.call_count,1)
        self.assertTrue(fresh['config']['enabled'])

    def test_connected_session_is_saved_privately_but_disconnect_is_not_undone(self):
        self.state['authorized']=True
        with patch.object(drive,'controller_session',return_value={'token':{'refresh_token':'fixture-refresh'}}):
            drive.sync_controller(self.config,self.profile)
        profile=json.loads(self.profile.read_text())
        self.assertEqual(profile['oauth_token']['refresh_token'],'fixture-refresh')
        self.assertEqual(self.profile.stat().st_mode & 0o777,0o600)
        self.state['authorized']=False
        with patch.object(drive,'controller_session') as transfer:
            drive.sync_controller(self.config,self.profile)
        transfer.assert_not_called()

    def test_mismatched_client_never_transfers_session(self):
        self.state['config']['client_id']='another-client'
        with patch.object(drive,'controller_session') as transfer:
            with self.assertRaisesRegex(RuntimeError,'different Google client'):
                drive.sync_controller(self.config,self.profile)
        transfer.assert_not_called()

    def test_database_recovery_requires_saved_key_enabled_drive_and_authorization(self):
        key=self.profile.with_name('internet-map-recovery.key')
        key.write_bytes(b'k'*32)
        self.state['authorized']=True
        self.state['config']['enabled']=True
        with patch.object(drive,'controller_session',return_value={}), patch.object(drive,'sync_database_recovery',return_value={'status':'recovering'}) as restore:
            result=drive.sync_controller(self.config,self.profile)
            restore.assert_called_once_with(self.config,key)
            self.assertEqual(result['database_recovery']['status'],'recovering')
        self.state['config']['enabled']=False
        with patch.object(drive,'controller_session',return_value={}), patch.object(drive,'sync_database_recovery') as restore:
            drive.sync_controller(self.config,self.profile)
            restore.assert_not_called()
        self.state['config']['enabled']=True
        self.state['authorized']=False
        with patch.object(drive,'sync_database_recovery') as restore:
            drive.sync_controller(self.config,self.profile)
            restore.assert_not_called()

    def test_session_transfer_atomic_private_and_safe_to_retry(self):
        config=self.root/'controller.json'
        config.write_text(json.dumps({'data_dir':str(self.root/'data'),'gdrive':{}}))
        script=drive.SESSION_SCRIPT.replace('/opt/cnc/config.json',str(config)).replace('/opt/cnc-controller/config.json',str(self.root/'missing.json'))
        token={'refresh_token':'fixture-refresh','access_token':'fixture-access'}
        payload={'action':'restore','client_id':'fixture-client','token':token}
        for _ in range(2):
            result=subprocess.run([sys.executable,'-c',script],input=json.dumps(payload),text=True,capture_output=True)
            self.assertEqual(result.returncode,0,result.stderr)
            self.assertNotIn('fixture-refresh',result.stdout)
        saved=next((self.root/'data/gdrive').glob('*.json'))
        self.assertEqual(saved.stat().st_mode & 0o777,0o600)
        self.assertEqual(json.loads(saved.read_text()),token)
        payload['token']={'refresh_token':'different'}
        result=subprocess.run([sys.executable,'-c',script],input=json.dumps(payload),text=True,capture_output=True)
        self.assertNotEqual(result.returncode,0)
        self.assertEqual(json.loads(saved.read_text()),token)


if __name__ == '__main__': unittest.main()
