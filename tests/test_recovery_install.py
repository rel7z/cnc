"""Exercise fresh recovery installation without contacting services or hosts."""
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock, patch


class RecoveryInstallTests(unittest.TestCase):
    def exercise(self, fail=False):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            app, cnc, units = root/'map', root/'cnc', root/'units'
            old, state = cnc/'wordpress-generator', app/'data/discovery'
            for directory in (old, state, units):
                directory.mkdir(parents=True)
            (old/'paused').touch()
            (old/'checkpoint').write_text('legacy preserved')
            (state/'paused').touch()
            (state/'checkpoint').write_text('restored preserved')
            (app/'data/restore-receipt.json').write_text('{}')
            (app/'service.env').write_text('INTERNET_MAP_API_KEY='+'k'*32+'\n')
            (cnc/'config.json').write_text(json.dumps(dict(http_addr='10.20.0.2:8080')))
            source = (Path(__file__).parents[1]/'infra/install-discovery.py').read_text()
            source = source.replace('/opt/internet-map',str(app)).replace('/opt/cnc',str(cnc)).replace('/etc/systemd/system',str(units))
            scope = {'__name__':'recovery_install_test'}
            exec(compile(source, 'isolated-install-discovery.py', 'exec'), scope)
            opener = Mock()
            opener.open.return_value = io.BytesIO(b'[]')
            calls = []
            def run(*args):
                calls.append(args)
                if fail and args[:2] == ('systemctl','is-active'):
                    raise RuntimeError('activation fixture failed')
                return Mock(returncode=0)
            scope['run'] = run
            with patch('urllib.request.build_opener',return_value=opener), patch('subprocess.run',return_value=Mock(returncode=0)):
                if fail:
                    with self.assertRaisesRegex(RuntimeError,'activation fixture'):
                        scope['install']()
                else:
                    scope['install']()
            self.assertEqual((state/'checkpoint').read_text(),'restored preserved')
            self.assertTrue((state/'paused').exists())
            if fail:
                self.assertFalse(old.is_symlink())
                self.assertEqual((old/'checkpoint').read_text(),'legacy preserved')
            else:
                self.assertIn(('systemctl','disable','--now','internet-map-backup.timer'),calls)
                self.assertFalse(any(call[:2] == ('systemctl','enable') and 'internet-map-backup.timer' in call for call in calls))
                self.assertTrue(old.is_symlink())
                self.assertEqual(old.resolve(),state.resolve())
                retired = list(cnc.glob('wordpress-generator-before-restore-*'))
                self.assertEqual((retired[0]/'checkpoint').read_text(),'legacy preserved')
                self.assertEqual((state/'generator.env').read_text(),'GO_API_URL=http://10.20.0.2:8080\n')

    def test_restored_tree_installs_and_keeps_legacy_for_rollback(self):
        self.exercise()

    def test_failed_activation_keeps_restored_and_legacy_state(self):
        self.exercise(fail=True)
