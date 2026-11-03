"""Isolated manager tests: no cloud changes and no private tool access."""
import importlib.util
import contextlib
import errno
import hashlib
import io
import json
from pathlib import Path
import subprocess
import tempfile
import sys
import unittest
from unittest.mock import patch, MagicMock

spec = importlib.util.spec_from_file_location('manager', Path(__file__).with_name('cnc_manager.py'))
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)
actual_port_busy = m.port_busy
actual_can_bind_port = m.can_bind_port
actual_dashboard_health = m.dashboard_health


class ManagerTests(unittest.TestCase):
    def test_digitalocean_worker_login_uses_its_own_alias_and_key(self):
        folder = self.root / '.digitalocean'
        folder.mkdir()
        config = folder / 'ssh_config'
        config.write_text('Host cnc-do-worker-1\n HostName 192.0.2.15\n')
        with patch.object(m, 'executable', return_value='/usr/bin/ssh'):
            command = m.worker_ssh_command({'repo': str(self.root), 'provider': 'digitalocean', 'ssh_config': str(config)}, {'id': 'cnc-do-worker-1'})
        self.assertIn(str(config), command)
        self.assertEqual(command[-2], 'cnc-do-worker-1')
        self.assertNotIn('.linode', ' '.join(command))

    def test_verified_digitalocean_deployment_is_discovered_before_old_linode(self):
        folder = self.root / '.digitalocean'
        folder.mkdir()
        connection = {'provider': 'digitalocean', 'repo': str(self.root), 'ssh_config': str(folder / 'ssh_config'), 'ssh_alias': 'cnc-do-controller', 'local_port': 8113, 'remote_port': 3000}
        (folder / 'active-deployment.json').write_text(json.dumps({'verified': True, 'connection': connection}))
        self.assertEqual(m.discover(self.root)['provider'], 'digitalocean')

    def test_worker_login_uses_saved_alias_without_opening_tools(self):
        directory = self.root / '.linode'
        directory.mkdir()
        (directory / 'cross-cloud-ssh_config').write_text('Host cnc-linode-worker-5\n HostName 192.0.2.5\n')
        with patch.object(m, 'executable', return_value='/usr/bin/ssh'):
            command = m.worker_ssh_command({'repo': str(self.root)}, {'id': 'cnc-linode-worker-5'}, '/root/processor/demo')
        self.assertIn('cnc-linode-worker-5', command)
        self.assertEqual(command[-1], 'cd /root/processor/demo && exec bash -l')
        self.assertIn('StrictHostKeyChecking=yes', command)

    def test_worker_login_uses_advertised_host_and_quotes_folder(self):
        with patch.object(m, 'executable', return_value='/usr/bin/ssh'):
            command = m.worker_ssh_command({'repo': str(self.root)}, {'id': 'demo', 'metadata': {'ssh_host': '192.0.2.8', 'ssh_user': 'root', 'ssh_port': '2222'}}, '/root/processor/my folder')
            self.assertIn('root@192.0.2.8', command)
            self.assertIn('2222', command)
            self.assertEqual(command[-1], "cd '/root/processor/my folder' && exec bash -l")
            with self.assertRaises(m.ManagerError):
                m.worker_ssh_command({'repo': str(self.root)}, {'id': '-oProxyCommand=bad', 'address': '192.0.2.8'})

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name).resolve()
        self.config = self.root / 'connection.json'
        self.aws = dict(provider='aws', instance_id='i-test', region='us-east-2', aws_profile='test', local_port=8081, remote_port=3000)
        for name, value in [('port_busy', False), ('can_bind_port', True), ('dashboard_health', False)]:
            mock = patch.object(m, name, return_value=value)
            mock.start()
            self.addCleanup(mock.stop)

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

    def test_verified_linode_migration_precedes_stale_aws(self):
        m.private_json(self.root / '.aws-cnc/state.json', {'nodes': {'cnc-aws-server': {'id': 'i-stale'}}})
        connection = dict(provider='linode', ssh_config='/fixture/ssh', ssh_alias='controller', local_port=8087, remote_port=3000)
        path = self.root / '.linode/active-deployment.json'
        m.private_json(path, {'verified': True, 'connection': connection})
        self.assertEqual(m.discover(self.root), dict(connection, repo=str(self.root)))
        m.private_json(path, {'verified': False, 'connection': connection})
        with self.assertRaises(m.ManagerError):
            m.discover(self.root)

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
             patch.object(m, 'discover', return_value=dict(self.aws)), patch.object(m, 'connect') as connect, \
             patch.object(m, 'sync_tools') as sync:
            m.deploy(self.config, no_browser=True)
            self.assertEqual(setup.call_count, 2)
            self.assertIn('--apply', setup.call_args_list[0].args[0])
            self.assertIn('deploy', setup.call_args_list[1].args[0])
            connect.assert_called_once_with(self.aws, self.config, no_browser=True)
            sync.assert_called_once_with(self.aws, self.config)

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
             patch.object(m, 'connect'), patch.object(m, 'sync_tools') as sync:
            m.deploy(self.config)
            self.assertEqual(runner.call_count, 2)
            self.assertTrue(runner.call_args_list[1].args[0][1].endswith('release.py'))
            sync.assert_called_once()
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
             patch.object(m, 'diagnose'), \
             patch.object(m.subprocess, 'Popen', side_effect=[child, OSError('end fixture')]) as start:
            with self.assertRaises(OSError): m.supervise(self.aws, self.config)
            self.assertEqual(start.call_count, 2)
            stop.assert_called_once_with(child)

    def test_conflicting_port_moves_and_persists_without_stopping_apps(self):
        m.private_json(self.config, self.aws)
        with patch.object(m, 'port_busy', return_value=True), \
             patch.object(m, 'can_bind_port', side_effect=lambda p: p == 8083), \
             patch.object(m.subprocess, 'Popen') as spawn:
            self.assertTrue(m.ensure_local_port(self.aws, self.config))
        self.assertEqual(self.aws['local_port'], 8083)
        self.assertEqual(m.read_json(self.config)['local_port'], 8083)
        self.assertEqual(m.read_json(self.root/'config-before-port-change.json')['local_port'], 8081)
        spawn.assert_not_called()

    def test_healthy_port_is_preserved(self):
        with patch.object(m, 'dashboard_health', return_value=True), patch.object(m, 'can_bind_port') as bind:
            self.assertFalse(m.ensure_local_port(self.aws, self.config))
        bind.assert_not_called()
        self.assertFalse(self.config.exists())

    def test_adopts_background_port_migration_after_sync(self):
        m.private_json(self.config, dict(self.aws, local_port=8084))
        with patch.object(m, 'private_json') as write:
            self.assertFalse(m.ensure_local_port(self.aws, self.config))
        self.assertEqual(self.aws['local_port'], 8084)
        write.assert_not_called()

    def test_no_free_port_retains_original_configuration(self):
        m.private_json(self.config, self.aws)
        before = self.config.read_bytes()
        with patch.object(m, 'port_busy', return_value=True), patch.object(m, 'can_bind_port', return_value=False):
            with self.assertRaisesRegex(m.ManagerError, 'No free localhost port'):
                m.ensure_local_port(self.aws, self.config)
        self.assertEqual(before, self.config.read_bytes())

    def test_ipv6_only_listener_is_detected(self):
        with patch.object(m.socket, 'create_connection', side_effect=[OSError('no IPv4 listener'), MagicMock()]) as connect:
            self.assertTrue(actual_port_busy(8081))
        self.assertEqual(connect.call_args.args[0], ('::1', 8081))

    def test_ipv6_bind_conflict_rejects_candidate(self):
        ipv4, ipv6 = MagicMock(), MagicMock()
        ipv6.bind.side_effect = OSError(errno.EADDRINUSE, 'in use')
        handles = []
        for inner in [ipv4, ipv6]:
            handle = MagicMock(); handle.__enter__.return_value = inner; handles.append(handle)
        with patch.object(m.socket, 'socket', side_effect=handles):
            self.assertFalse(actual_can_bind_port(8081))

    def test_other_apps_login_is_not_a_healthy_cnc_dashboard(self):
        for body, expected in [(b'<input name="username"><input name="password">', False),
                               (b'CNC Console <input name="password">', True)]:
            response = MagicMock(); response.status = 200; response.read.return_value = body
            opened = MagicMock(); opened.__enter__.return_value = response
            with patch.object(m.OPENER, 'open', return_value=opened):
                self.assertEqual(actual_dashboard_health(8081), expected)

    def test_auto_diagnosis_redacts_authentication_error(self):
        output = io.StringIO()
        with patch.object(m, 'tunnel_command', return_value=(['aws'], {})), \
             patch.object(m.subprocess, 'run', return_value=subprocess.CompletedProcess([], 1, '', 'Session expired private_fake_token')), \
             contextlib.redirect_stdout(output):
            report = m.diagnose(self.aws, self.config)
        self.assertIn('AWS authentication session has expired.', report['issues'])
        self.assertIn('aws login --profile test', output.getvalue())
        self.assertNotIn('private_fake_token', output.getvalue())
        self.assertNotIn('private_fake_token', (self.root/'last-diagnosis.json').read_text())

    def test_connection_failure_runs_diagnosis_without_sync(self):
        with patch.object(m, 'tunnel_command'), patch.object(m, 'manager_matches', return_value=True), \
             patch.object(m, 'launchctl'), patch.object(m, 'wait_ready', return_value=False), \
             patch.object(m, 'diagnose', return_value={'issues':['fixture cause']}) as diagnose, \
             patch.object(m, 'sync_tools') as sync:
            with self.assertRaisesRegex(m.ManagerError, 'fixture cause'):
                m.connect(self.aws, self.config, no_browser=True, once=True)
        diagnose.assert_called_once_with(self.aws, self.config)
        sync.assert_not_called()

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
        source = self.root / 'processor/tools'
        source.mkdir(parents=True)
        # Synthetic inert bytes only; the actual private folder is never read.
        (source / 'scanner.py').write_bytes(b'opaque synthetic fixture\x00\xff')
        (source / 'config.json').write_bytes(b'opaque config, deliberately not JSON')
        archive = self.root / 'test.tar.gz'
        return source, archive, m.sync_archive(self.root, archive)

    def remote_fixture(self, archive, digest):
        code = m.REMOTE_SYNC.replace("Path('/opt/cnc-processor/tools')", repr_path(self.root/'deployment/tools'))
        code = code.replace("Path('/opt/cnc-processor/sync-rollback')", repr_path(self.root/'rollback'))
        return subprocess.run([sys.executable,'-c',code,str(archive),digest],capture_output=True,text=True)

    def test_opaque_sync_hashes_and_rollback_without_deleting_extras(self):
        source, archive, manifest = self.fixture_archive()
        target = self.root / 'deployment/tools';target.mkdir(parents=True)
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

    def test_named_sync_never_traverses_unapproved_siblings(self):
        source = self.root / 'processor/tools'
        for folder in ['first', 'second', 'unapproved']:
            (source / folder).mkdir(parents=True)
            (source / folder / 'opaque.dat').write_bytes(b'synthetic fixture')
        # A symlink in an unapproved sibling would fail a whole-tree sync.
        (source / 'unapproved/alias').symlink_to(self.root / 'missing')
        archive = self.root / 'named.tar.gz'
        manifest = m.sync_archive(self.root, archive, folders=['first','second'])
        self.assertEqual(set(manifest), {'first/opaque.dat','second/opaque.dat'})
        for invalid in [[], ['../unapproved'], ['first/opaque.dat'], ['missing']]:
            with self.assertRaises(m.ManagerError):
                m.sync_archive(self.root, archive, folders=invalid)

    def test_sync_rejects_corruption_before_changing_target(self):
        _, archive, _ = self.fixture_archive()
        result=self.remote_fixture(archive,'0'*64)
        self.assertNotEqual(result.returncode,0)
        self.assertFalse((self.root/'deployment/tools').exists())

    def test_sync_rejects_destination_symlinks(self):
        _, archive, _ = self.fixture_archive()
        target=self.root/'deployment/tools';target.mkdir(parents=True)
        other=self.root/'other';other.write_bytes(b'untouched')
        (target/'scanner.py').symlink_to(other)
        result=self.remote_fixture(archive,hashlib.sha256(archive.read_bytes()).hexdigest())
        self.assertNotEqual(result.returncode,0)
        self.assertEqual(other.read_bytes(),b'untouched')

    def test_aws_sync_resumes_pending_command_without_new_upload(self):
        manifest={'opaque.py':{'bytes':1,'sha256':'fixture'}}
        receipt={'folder':'tools','files':manifest,'verified':True,'activated':False,'rollback':'/private-backup'}
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
        receipt={'folder':'tools','files':manifest,'verified':True,'activated':False,'rollback':'/private-backup'}
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
        receipt={'folder':'tools','files':manifest,'verified':True,'activated':False,'rollback':'/private-backup'}
        def run(command,**kwargs):
            return subprocess.CompletedProcess(command,0,json.dumps(receipt) if command[0].endswith('ssh') else '')
        with patch.object(m,'repo_path',return_value=self.root),patch.object(m,'executable',side_effect=lambda name,*args:'/bin/'+name), \
             patch.object(m.subprocess,'run',side_effect=run) as runner:
            m.sync_tools(cfg,self.config)
        self.assertEqual([c.args[0][0] for c in runner.call_args_list],['/bin/scp','/bin/ssh'])
        self.assertEqual(m.read_json(self.root/'last-sync.json'),receipt)

    def test_full_tree_discovers_both_tools_and_new_nested_folder(self):
        source, archive, _ = self.fixture_archive()
        expected={'bf/rr':b'opaque bf fixture', 'bf/pass.txt':b'opaque supporting file',
                  'divi/scanner.py':b'opaque divi fixture', 'future/deep/extra.bin':b'new tool fixture'}
        for name, data in expected.items():
            p=source/name;p.parent.mkdir(parents=True,exist_ok=True);p.write_bytes(data)
        (source/'bf/rr').chmod(0o700)
        manifest=m.sync_archive(self.root,archive)
        self.assertTrue(set(expected)<=set(manifest))
        result=self.remote_fixture(archive,hashlib.sha256(archive.read_bytes()).hexdigest())
        self.assertEqual(result.returncode,0,result.stderr)
        self.assertEqual(json.loads(result.stdout)['folder'],'tools')
        for name, data in expected.items():self.assertEqual((self.root/'deployment/tools'/name).read_bytes(),data)
        self.assertEqual((self.root/'deployment/tools/bf/rr').stat().st_mode & 0o777,0o700)

    def test_dashboard_open_never_depends_on_tool_sync(self):
        with patch.object(m,'sync_tools',side_effect=AssertionError('Opening the tunnel must not access tools')) as sync,patch.object(m,'connect') as connect:
            m.open_dashboard(self.aws,self.config,no_browser=True)
        sync.assert_not_called()
        connect.assert_called_once_with(self.aws,self.config,no_browser=True,once=False)

    def test_dashboard_connection_failure_does_not_start_tool_sync(self):
        with patch.object(m,'sync_tools') as sync,patch.object(m,'connect',side_effect=m.ManagerError('fixture SSH failure')):
            with self.assertRaisesRegex(m.ManagerError,'fixture SSH failure'):
                m.open_dashboard(self.aws,self.config)
        sync.assert_not_called()

    def test_diagnostic_once_does_not_start_sync(self):
        with patch.object(m,'sync_tools') as sync,patch.object(m,'connect'):
            m.open_dashboard(self.aws,self.config,once=True)
            m.open_dashboard(self.aws,self.config,no_sync=True)
        sync.assert_not_called()

    def test_old_divi_pending_command_finishes_before_full_tree_sync(self):
        manifest={'opaque.py':{'bytes':1,'sha256':'fixture'}}
        receipt={'folder':'divi','files':manifest,'verified':True,'activated':False,'rollback':'/private-backup'}
        m.private_json(self.root/'pending-sync.json',dict(command_id='legacy',instance_id='i-test',region='us-east-2',manifest=manifest))
        response={'Status':'Success','StandardOutputContent':json.dumps(receipt)}
        function=m.sync_tools
        with patch.object(m,'repo_path',return_value=self.root),patch.object(m,'executable',return_value='aws'), \
             patch.object(m.subprocess,'run',return_value=subprocess.CompletedProcess([],0,json.dumps(response))), \
             patch.object(m,'sync_tools') as full_sync:
            function(self.aws,self.config)
        full_sync.assert_called_once_with(self.aws,self.config)
        self.assertFalse((self.root/'pending-sync.json').exists())
        self.assertEqual(m.read_json(self.root/'last-divi-sync.json'),receipt)


def repr_path(path):
    return 'Path(' + repr(str(path)) + ')'


if __name__ == '__main__':
    unittest.main()
