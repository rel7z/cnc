"""Isolated manager tests: no cloud changes and no private tool access."""
import importlib.util
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
import sys
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('manager', Path(__file__).with_name('cnc_manager.py'))
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)


class ManagerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name).resolve()
        self.config = self.root / 'connection.json'
        self.aws = dict(provider='aws', instance_id='i-test', region='us-east-2', aws_profile='test', local_port=8081, remote_port=3000)

    def tearDown(self):
        self.temp.cleanup()

    def test_private_config_and_invalid_port(self):
        m.private_json(self.config, self.aws)
        self.assertEqual(self.config.stat().st_mode & 0o777, 0o600)
        self.assertEqual(m.load_config(self.config), self.aws)
        for bad in (0, 80, 65536):
            with self.assertRaises(m.ManagerError): m.port(bad)
        alias = self.root / 'alias'
        alias.symlink_to(self.config)
        with self.assertRaises(m.ManagerError): m.private_json(alias, {})

    def test_discovery(self):
        m.private_json(self.root / '.aws-cnc/state.json', {'nodes': {'cnc-aws-server': {'id': 'i-found'}}})
        self.assertEqual(m.discover(self.root)['instance_id'], 'i-found')

    def test_aws_tunnel_parameters(self):
        with patch.object(m, 'executable', side_effect=lambda name, *args: '/bin/' + name):
            command, env = m.tunnel_command(self.aws)
        params = json.loads(command[command.index('--parameters') + 1])
        self.assertEqual(params, {'host': ['127.0.0.1'], 'portNumber': ['3000'], 'localPortNumber': ['8081']})
        self.assertEqual(command[-1], 'test')
        self.assertTrue(env['PATH'].startswith('/bin'))

    def test_ssh_loopback_and_option_injection(self):
        ssh = self.root / 'ssh_config'; ssh.write_text('')
        cfg = dict(provider='linode', ssh_config=str(ssh), ssh_alias='server', local_port=8081, remote_port=3000)
        with patch.object(m, 'executable', return_value='/usr/bin/ssh'):
            command, _ = m.tunnel_command(cfg)
            self.assertIn('127.0.0.1:8081:127.0.0.1:3000', command)
            cfg['ssh_alias'] = '-oProxyCommand=bad'
            with self.assertRaises(m.ManagerError): m.tunnel_command(cfg)

    def plan(self):
        return subprocess.CompletedProcess([], 0, json.dumps(dict(workers_launchable_now=3, controller_hourly_usd=.1,
            worker_hourly_usd=.05, account='test', instance_type='controller', worker_type='worker')))

    def test_aws_cancellation_has_no_writes(self):
        with patch.object(m, 'repo_path', return_value=self.root), patch.object(m, 'ask', side_effect=['aws', '3', 'test', 'no']), \
             patch.object(m, 'executable', return_value='/bin/mock'), patch.object(m.subprocess, 'run', return_value=self.plan()), \
             patch.object(m, 'run_setup') as setup, patch.object(m, 'new_login') as login:
            m.deploy(self.config)
            setup.assert_not_called(); login.assert_not_called()
        self.assertFalse(self.config.exists())

    def test_aws_confirmed_workflow(self):
        with patch.object(m, 'repo_path', return_value=self.root), patch.object(m, 'ask', side_effect=['aws', '3', 'test', 'DEPLOY']), \
             patch.object(m, 'executable', return_value='/bin/mock'), patch.object(m.subprocess, 'run', return_value=self.plan()), \
             patch.object(m, 'run_setup') as setup, patch.object(m, 'new_login'), \
             patch.object(m, 'discover', return_value=dict(self.aws)), patch.object(m, 'connect') as connect:
            m.deploy(self.config, no_browser=True)
            self.assertEqual(setup.call_count, 2)
            self.assertIn('--apply', setup.call_args_list[0].args[0])
            self.assertIn('deploy', setup.call_args_list[1].args[0])
            connect.assert_called_once_with(self.aws, self.config, no_browser=True)

    def test_existing_deployment_refused(self):
        m.private_json(self.root / '.aws-cnc/state.json', {'revision': 'existing'})
        with patch.object(m, 'repo_path', return_value=self.root), patch.object(m, 'ask', return_value='aws'):
            with self.assertRaises(m.ManagerError): m.deploy(self.config)

    def linode_response(self, token, path):
        if path.startswith('linode/instances'): return {'data': []}
        if path.startswith('regions'): return {'data': [{'id': 'sg-sin-2'}]}
        return {'data': [{'id': x, 'price': {'monthly': 10}} for x in ('g6-standard-4', 'g6-standard-2')]}

    def test_linode_cancellation_has_no_writes(self):
        with patch.object(m, 'repo_path', return_value=self.root), \
             patch.object(m, 'ask', side_effect=['linode', '1', 'sg-sin-2', 'g6-standard-4', 'g6-standard-2', 'no']), \
             patch.object(m, 'executable', return_value='/bin/mock'), patch.object(m.getpass, 'getpass', return_value='test-token-must-not-persist'), \
             patch.object(m, 'linode_api', side_effect=self.linode_response), patch.object(m, 'run_setup') as setup:
            m.deploy(self.config)
            setup.assert_not_called()
        self.assertFalse(self.config.exists())

    def test_linode_confirmed_workflow(self):
        def setup(command, *args):
            if 'provision.py' in command[1]:
                m.private_json(self.root / '.linode/state.json', {'servers': {'cnc-linode-server': {'id': 1}}})
        with patch.object(m, 'repo_path', return_value=self.root), \
             patch.object(m, 'ask', side_effect=['linode', '1', 'sg-sin-2', 'g6-standard-4', 'g6-standard-2', 'DEPLOY']), \
             patch.object(m, 'executable', return_value='/bin/mock'), patch.object(m.getpass, 'getpass', return_value='test-token-must-not-persist'), \
             patch.object(m, 'linode_api', side_effect=self.linode_response), patch.object(m, 'run_setup', side_effect=setup) as runner, \
             patch.object(m, 'new_login'), patch.object(m.subprocess, 'run', return_value=subprocess.CompletedProcess([], 0)), \
             patch.object(m, 'connect'):
            m.deploy(self.config)
            self.assertEqual(runner.call_count, 2)
            self.assertTrue(runner.call_args_list[1].args[0][1].endswith('release.py'))
        self.assertNotIn('test-token-must-not-persist', self.config.read_text())

    def test_healthy_connect_reuses_tunnel(self):
        with patch.object(m, 'tunnel_command'), patch.object(m, 'dashboard_health', return_value=True), \
             patch.object(m, 'supervise') as start, patch.object(m, 'launchctl') as ctl:
            m.connect(self.aws, self.config, no_browser=True, once=True)
            start.assert_not_called(); ctl.assert_not_called()

    def test_managed_recovery(self):
        with patch.object(m, 'tunnel_command'), patch.object(m, 'dashboard_health', return_value=False), \
             patch.object(m, 'manager_matches', return_value=True), patch.object(m, 'wait_ready', return_value=True), \
             patch.object(m, 'launchctl') as ctl:
            m.connect(self.aws, self.config, no_browser=True, once=True)
            self.assertEqual(ctl.call_args.args[:2], ('kickstart', '-k'))

    def test_stale_child_is_stopped_before_reconnect(self):
        from unittest.mock import Mock
        child = Mock(); child.poll.return_value = None
        with patch.object(m, 'tunnel_command', return_value=(['tunnel'], {})), \
             patch.object(m, 'port_busy', return_value=False), patch.object(m, 'dashboard_health', return_value=False), \
             patch.object(m.time, 'sleep'), patch.object(m, 'stop_child') as stop, \
             patch.object(m.subprocess, 'Popen', side_effect=[child, OSError('end fixture')]) as start:
            with self.assertRaises(OSError): m.supervise(self.aws, self.config)
            self.assertEqual(start.call_count, 2)
            stop.assert_called_once_with(child)

    def test_install_failure_restores_legacy_agent(self):
        support = self.root / 'support'
        agent = self.root / 'Library/LaunchAgents' / (m.LEGACY + '.plist')
        agent.parent.mkdir(parents=True); agent.write_bytes(b'legacy fixture')
        with patch.object(m, 'SUPPORT', support), patch.object(m.Path, 'home', return_value=self.root), \
             patch.object(m.sys, 'platform', 'darwin'), patch.object(m, 'tunnel_command'), \
             patch.object(m, 'legacy_matches', return_value=True), patch.object(m, 'loaded', return_value=False), \
             patch.object(m, 'launchctl'), patch.object(m, 'wait_ready', return_value=False):
            with self.assertRaises(m.ManagerError): m.install(self.aws, self.config)
        self.assertEqual(agent.read_bytes(), b'legacy fixture')
        self.assertFalse((agent.parent / (m.LABEL + '.plist')).exists())

    def fixture_archive(self):
        source = self.root / 'processor/tools/divi'
        source.mkdir(parents=True)
        # Synthetic inert bytes only; the actual private folder is never read.
        (source / 'scanner.py').write_bytes(b'opaque synthetic fixture\x00\xff')
        (source / 'config.json').write_bytes(b'opaque config, deliberately not JSON')
        archive = self.root / 'test.tar.gz'
        return source, archive, m.sync_archive(self.root, archive)

    def remote_fixture(self, archive, digest):
        code = m.REMOTE_SYNC.replace("Path('/opt/cnc-processor/tools/divi')", repr_path(self.root/'deployment/divi'))
        code = code.replace("Path('/opt/cnc-processor/sync-rollback')", repr_path(self.root/'rollback'))
        return subprocess.run([sys.executable,'-c',code,str(archive),digest],capture_output=True,text=True)

    def test_opaque_sync_hashes_and_rollback_without_deleting_extras(self):
        source, archive, manifest = self.fixture_archive()
        target = self.root / 'deployment/divi';target.mkdir(parents=True)
        (target/'scanner.py').write_bytes(b'old copy')
        (target/'unrelated.txt').write_bytes(b'retain')
        result = self.remote_fixture(archive,hashlib.sha256(archive.read_bytes()).hexdigest())
        self.assertEqual(result.returncode,0,result.stderr)
        receipt=json.loads(result.stdout)
        self.assertEqual(receipt['files'],manifest)
        self.assertFalse(receipt['activated'])
        self.assertEqual((Path(receipt['rollback'])/'scanner.py').read_bytes(),b'old copy')
        self.assertEqual((target/'scanner.py').read_bytes(),(source/'scanner.py').read_bytes())
        self.assertEqual((target/'unrelated.txt').read_bytes(),b'retain')

    def test_sync_rejects_source_symlinks(self):
        source, archive, _ = self.fixture_archive()
        (source/'alias.py').symlink_to(source/'scanner.py')
        with self.assertRaises(m.ManagerError):m.sync_archive(self.root,archive)

    def test_sync_rejects_corruption_before_changing_target(self):
        _, archive, _ = self.fixture_archive()
        result=self.remote_fixture(archive,'0'*64)
        self.assertNotEqual(result.returncode,0)
        self.assertFalse((self.root/'deployment/divi').exists())

    def test_sync_rejects_destination_symlinks(self):
        _, archive, _ = self.fixture_archive()
        target=self.root/'deployment/divi';target.mkdir(parents=True)
        other=self.root/'other';other.write_bytes(b'untouched')
        (target/'scanner.py').symlink_to(other)
        result=self.remote_fixture(archive,hashlib.sha256(archive.read_bytes()).hexdigest())
        self.assertNotEqual(result.returncode,0)
        self.assertEqual(other.read_bytes(),b'untouched')

    def test_aws_sync_resumes_pending_command_without_new_upload(self):
        manifest={'opaque.py':{'bytes':1,'sha256':'fixture'}}
        receipt={'folder':'divi','files':manifest,'verified':True,'activated':False,'rollback':'/private-backup'}
        m.private_json(self.root/'pending-sync.json',dict(command_id='existing',instance_id='i-test',region='us-east-2',manifest=manifest))
        response={'Status':'Success','StandardOutputContent':json.dumps(receipt)}
        with patch.object(m,'repo_path',return_value=self.root), patch.object(m,'executable',return_value='aws'), \
             patch.object(m.subprocess,'run',return_value=subprocess.CompletedProcess([],0,json.dumps(response))) as run, \
             patch.object(m,'sync_archive') as archive:
            m.sync_tools(self.aws,self.config)
        archive.assert_not_called()
        self.assertEqual(run.call_count,1)
        self.assertIn('existing',run.call_args.args[0])
        self.assertFalse((self.root/'pending-sync.json').exists())
        self.assertEqual(m.read_json(self.root/'last-sync.json'),receipt)

    def test_expired_aws_session_does_not_copy_tools(self):
        with patch.object(m,'repo_path',return_value=self.root), patch.object(m,'executable',return_value='aws'), \
             patch.object(m.subprocess,'run',return_value=subprocess.CompletedProcess([],1,'','Your session has expired')), \
             patch.object(m,'sync_archive') as archive:
            with self.assertRaisesRegex(m.ManagerError,'session expired'):m.sync_tools(self.aws,self.config)
        archive.assert_not_called()

    def test_cross_account_sync_is_refused(self):
        m.private_json(self.root/'.aws-cnc/state.json',{'account':'expected'})
        with patch.object(m,'repo_path',return_value=self.root), patch.object(m,'executable',return_value='aws'), \
             patch.object(m.subprocess,'run',return_value=subprocess.CompletedProcess([],0,json.dumps({'Account':'other'}))), \
             patch.object(m,'sync_archive') as archive:
            with self.assertRaisesRegex(m.ManagerError,'account'):m.sync_tools(self.aws,self.config)
        archive.assert_not_called()

    def test_complete_aws_sync_transport(self):
        _, _, manifest=self.fixture_archive()
        m.private_json(self.root/'.aws-cnc/state.json',{'account':'expected','release_bucket':'private-fixture'})
        receipt={'folder':'divi','files':manifest,'verified':True,'activated':False,'rollback':'/private-backup'}
        def run(command,**kwargs):
            operation=command[1:3]
            responses={('sts','get-caller-identity'):{'Account':'expected'},
                ('s3api','get-public-access-block'):{'PublicAccessBlockConfiguration':{k:True for k in ('BlockPublicAcls','IgnorePublicAcls','BlockPublicPolicy','RestrictPublicBuckets')}},
                ('s3api','put-object'):{},('ssm','send-command'):{'Command':{'CommandId':'new-command'}},
                ('ssm','get-command-invocation'):{'Status':'Success','StandardOutputContent':json.dumps(receipt)}}
            if operation==['s3','presign']:return subprocess.CompletedProcess(command,0,'https://fixture.invalid/private-download')
            return subprocess.CompletedProcess(command,0,json.dumps(responses[tuple(operation)]))
        with patch.object(m,'repo_path',return_value=self.root), patch.object(m,'executable',return_value='aws'), \
             patch.object(m.subprocess,'run',side_effect=run) as runner:
            m.sync_tools(self.aws,self.config)
        calls=[c.args[0] for c in runner.call_args_list]
        upload=next(c for c in calls if c[1:3]==['s3api','put-object'])
        self.assertIn('AES256',upload)
        send=next(c for c in calls if c[1:3]==['ssm','send-command'])
        commands=json.loads(send[send.index('--parameters')+1])['commands'][0]
        self.assertIn('https://fixture.invalid/private-download',commands)
        self.assertNotIn('opaque synthetic fixture',commands)
        self.assertEqual(m.read_json(self.root/'last-sync.json'),receipt)

    def test_complete_linode_sync_transport(self):
        _, _, manifest=self.fixture_archive()
        ssh=self.root/'ssh_config';ssh.write_text('')
        cfg=dict(provider='linode',ssh_config=str(ssh),ssh_alias='server',local_port=8081,remote_port=3000)
        receipt={'folder':'divi','files':manifest,'verified':True,'activated':False,'rollback':'/private-backup'}
        def run(command,**kwargs):
            return subprocess.CompletedProcess(command,0,json.dumps(receipt) if command[0].endswith('ssh') else '')
        with patch.object(m,'repo_path',return_value=self.root),patch.object(m,'executable',side_effect=lambda name,*args:'/bin/'+name), \
             patch.object(m.subprocess,'run',side_effect=run) as runner:
            m.sync_tools(cfg,self.config)
        self.assertEqual([c.args[0][0] for c in runner.call_args_list],['/bin/scp','/bin/ssh'])
        self.assertEqual(m.read_json(self.root/'last-sync.json'),receipt)


def repr_path(path):
    return 'Path(' + repr(str(path)) + ')'


if __name__ == '__main__':
    unittest.main()
