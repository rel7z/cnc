import importlib.util
import io
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import unittest
from unittest import mock
import urllib.error
import urllib.request

MODULE = Path(__file__).with_name('dashboard-doctor.py')
spec = importlib.util.spec_from_file_location('doctor', MODULE)
doctor = importlib.util.module_from_spec(spec); spec.loader.exec_module(doctor)


class DoctorTests(unittest.TestCase):
    def test_realtime_diagnosis_bounds_snapshot_and_accepts_valid_frame(self):
        frames = [
            (b'event: snapshot\ndata: {"workers":[],"jobs":[],"tasks":[]}\n\n', 'connected'),
            (b'event: snapshot\ndata: '+b'x'*(600*1024), 'snapshot_too_large'),
            (b'event: other\n', 'invalid_snapshot'),
        ]
        for frame, expected in frames:
            client = mock.Mock()
            client.open.return_value = io.BytesIO(frame)
            with mock.patch.object(doctor, 'session_cookie', return_value='cnc_session=fixture; HttpOnly'), mock.patch.object(doctor, 'opener', return_value=client):
                self.assertEqual(doctor.realtime_check(Path('/unused'))['status'], expected)

    def test_auth_classification_does_not_treat_network_as_expiry(self):
        self.assertEqual(doctor.classify_auth(0, ''), 'valid')
        self.assertEqual(doctor.classify_auth(1, 'Token has expired; aws login'), 'login_required')
        self.assertEqual(doctor.classify_auth(1, 'Could not connect to endpoint URL'), 'aws_unreachable_or_denied')
        self.assertEqual(doctor.classify_auth(1, 'AccessDeniedException'), 'aws_unreachable_or_denied')
        self.assertEqual(doctor.classify_auth(1, 'InvalidSignatureException: Signature expired'), 'clock_skew_or_stale_request')

    def test_healthy_tunnel_is_left_alone(self):
        with mock.patch.object(doctor, 'probe', return_value=True), mock.patch.object(doctor, 'aws_command') as aws, mock.patch.object(doctor, 'stop_tunnel_listener') as stop:
            self.assertEqual(doctor.repair(Path('/unused'), True)['status'], 'healthy')
            aws.assert_not_called(); stop.assert_not_called()

    def test_linode_repair_uses_manager_without_aws_or_sync(self):
        cfg = dict(local_port=8106, _path='/fixture/manager.json')
        with mock.patch.object(doctor, 'URL', 'http://localhost:8081'), mock.patch.object(doctor, 'manager_settings', return_value=cfg), mock.patch.object(doctor, 'probe', side_effect=[False, True]), mock.patch.object(doctor.subprocess, 'run', return_value=subprocess.CompletedProcess([], 0)) as run, mock.patch.object(doctor, 'aws_command') as aws:
            self.assertEqual(doctor.repair(Path('/fixture'), True)['status'], 'reconnected')
            self.assertIn('--no-sync', run.call_args.args[0])
            self.assertIn('--once', run.call_args.args[0])
            self.assertEqual(doctor.URL, 'http://localhost:8106')
            aws.assert_not_called()

    def test_failed_aws_network_does_not_login_or_kill_listener(self):
        with mock.patch.object(doctor, 'probe', return_value=False), mock.patch.object(doctor, 'aws_command', return_value=(['aws'], 'instance', 'region', {})), mock.patch.object(doctor, 'auth_check', return_value='aws_unreachable_or_denied'), mock.patch.object(doctor, 'start_login') as login, mock.patch.object(doctor, 'stop_tunnel_listener') as stop:
            self.assertEqual(doctor.repair(Path('/unused'), True)['status'], 'aws_unreachable_or_denied')
            login.assert_not_called(); stop.assert_not_called()

    def test_login_cooldown(self):
        with tempfile.TemporaryDirectory() as tmp, mock.patch.object(doctor.subprocess, 'Popen') as popen:
            cache = Path(tmp)/'login.json'
            self.assertTrue(doctor.start_login(['aws'], {}, cache))
            self.assertFalse(doctor.start_login(['aws'], {}, cache))
            self.assertEqual(popen.call_count, 1)
            self.assertEqual(cache.stat().st_mode & 0o777, 0o600)
            if sys.platform == 'darwin':
                self.assertIn('--incognito %s', popen.call_args.kwargs['env']['BROWSER'])

    def test_unrelated_listener_not_killed(self):
        results = [subprocess.CompletedProcess([], 0, stdout='123\n'), subprocess.CompletedProcess([], 0, stdout=str(doctor.os.getuid())+' /usr/bin/python3\n')]
        with mock.patch.object(doctor.subprocess, 'run', side_effect=results), mock.patch.object(doctor.os, 'kill') as kill:
            with self.assertRaisesRegex(RuntimeError, 'another program'):
                doctor.stop_tunnel_listener()
            kill.assert_not_called()

    def test_single_use_browser_cookie_handoff(self):
        child = subprocess.Popen([sys.executable, str(MODULE), '--handoff'], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        try:
            child.stdin.write('cnc_session=fixture-cookie; HttpOnly; SameSite=Lax; Path=/; Max-Age=60\n'); child.stdin.close()
            link = child.stdout.readline().strip()
            self.assertTrue(link.startswith('http://localhost:'))
            self.assertNotIn('fixture-cookie', link)
            client = doctor.opener()
            with self.assertRaises(urllib.error.HTTPError) as invalid:
                client.open(link+'wrong', timeout=3)
            self.assertEqual(invalid.exception.code, 404)
            invalid.exception.close()
            with self.assertRaises(urllib.error.HTTPError) as signed_in:
                client.open(link, timeout=3)
            response = signed_in.exception
            self.assertEqual(response.code, 303)
            self.assertEqual(response.headers['Location'], doctor.URL+'/dashboard')
            self.assertIn('HttpOnly', response.headers['Set-Cookie'])
            self.assertIn('fixture-cookie', response.headers['Set-Cookie'])
            self.assertEqual(response.headers['Cache-Control'], 'no-store')
            response.close()
            child.wait(timeout=3)
            with self.assertRaises(urllib.error.URLError):
                client.open(link, timeout=2)
        finally:
            if child.poll() is None:
                child.kill(); child.wait()
            child.stdout.close(); child.stderr.close()


if __name__ == '__main__':
    unittest.main()
