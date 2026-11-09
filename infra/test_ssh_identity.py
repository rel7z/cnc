"""Test shared identities and provisioning policy with temporary keys only."""
import importlib.util
import io
import contextlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0,str(Path(__file__).parent))
import ssh_identity as identity
from ssh_policy import POLICY_NAME,key_only_setup,policy_text


def module(name,path):
    spec=importlib.util.spec_from_file_location(name,Path(__file__).parent/path)
    obj=importlib.util.module_from_spec(spec);spec.loader.exec_module(obj);return obj


class IdentityTests(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory();self.addCleanup(self.temp.cleanup)
        self.root=Path(self.temp.name);self.shared=self.root/'Mac Support/ssh'
        self.env=patch.dict(os.environ,{'CNC_SSH_IDENTITY_DIR':str(self.shared)})
        self.env.start();self.addCleanup(self.env.stop)

    def key(self,path):
        path.parent.mkdir(parents=True,exist_ok=True)
        subprocess.run(['ssh-keygen','-q','-t','ed25519','-N','','-f',str(path)],check=True,capture_output=True)
        return path

    def test_same_mac_key_reused_across_all_providers_and_legacy_retained(self):
        original=self.key(self.root/'.digitalocean/id_ed25519')
        old_linode=self.key(self.root/'.linode/id_ed25519');old_bytes=old_linode.read_bytes()
        paths=[identity.deployment_key(self.root,self.root/provider) for provider in ['.digitalocean','.linode','.aws-cnc','.upcloud']]
        self.assertEqual(len(set(paths)),1)
        self.assertEqual(paths[0].read_bytes(),original.read_bytes())
        self.assertEqual(old_linode.read_bytes(),old_bytes)
        self.assertEqual(paths[0].stat().st_mode&0o777,0o600)
        self.assertEqual(self.shared.stat().st_mode&0o777,0o700)
        self.assertEqual((self.root/'.aws-cnc/id_ed25519').resolve(),paths[0].resolve())
        lines=identity.identity_lines(paths[0],old_linode)
        self.assertIn(str(paths[0]),lines);self.assertIn(str(old_linode),lines)

    def test_missing_identity_generated_once_and_pair_mismatch_rejected(self):
        key=identity.shared_key(self.root);first=key.read_bytes()
        self.assertEqual(identity.shared_key(self.root).read_bytes(),first)
        key.with_suffix('.pub').write_text('ssh-ed25519 invalid\n')
        with self.assertRaisesRegex(RuntimeError,'mismatch'):identity.shared_key(self.root)
        self.assertEqual(key.read_bytes(),first)

    def test_missing_private_key_does_not_silently_rotate_identity(self):
        key=identity.deployment_key(self.root,self.root/'.digitalocean')
        key.unlink()
        with self.assertRaisesRegex(RuntimeError,'missing'):identity.shared_key(self.root)
        self.assertFalse(key.exists())

    def test_policy_is_key_only_and_precedes_legacy_password_dropins(self):
        self.assertEqual(sorted([POLICY_NAME,'00-cnc-worker-login.conf','50-cloud-init.conf'])[0],POLICY_NAME)
        for user in ['root','ubuntu']:
            text=policy_text(user);self.assertIn('PasswordAuthentication no',text)
            self.assertIn('AuthenticationMethods publickey',text);self.assertIn('AllowUsers '+user,text)
            self.assertNotIn('PasswordAuthentication yes',text)
            subprocess.run(['bash','-n'],input=key_only_setup(user),text=True,check=True,capture_output=True)
        self.assertIn('PermitRootLogin no',policy_text('ubuntu'))
        with self.assertRaises(ValueError):policy_text('root; whoami')

    def test_aws_key_registration_and_bootstrap_only_use_public_material(self):
        obj=module('aws_ssh_fixture','aws/provision.py');key=self.key(self.root/'fixture-key')
        calls=[]
        def aws(*args,**kwargs):
            calls.append(args)
            if args[:2]==('ec2','describe-key-pairs'):return {'KeyPairs':[]}
            if args[:2]==('ec2','run-instances'):return {'Instances':[{'InstanceId':'i-fixture'}]}
            return {}
        with patch.object(obj,'deployment_key',return_value=key),patch.object(obj,'state_save'),patch.object(obj,'aws',side_effect=aws):
            state={'key_name':'legacy-fixture'};name,path=obj.ensure_key(state)
            obj.launch('fixture','sg-fixture',{'subnet_id':'subnet-fixture'},name,'ami-fixture','/dev/sda1','fixture',20)
        registration=next(c for c in calls if 'import-key-pair' in c)
        self.assertIn('fileb://'+str(key.with_suffix('.pub')),registration)
        launch=next(c for c in calls if 'run-instances' in c)
        script=launch[launch.index('--user-data')+1]
        self.assertIn('AllowUsers ubuntu',script);self.assertIn('PasswordAuthentication no',script)
        self.assertNotIn('PRIVATE KEY',script);self.assertTrue(name.startswith('cnc-mac-'))
        self.assertIn('legacy-fixture',state['legacy_key_names'])

    def test_digitalocean_new_nodes_have_shared_key_and_key_only_cloud_init(self):
        obj=module('do_ssh_fixture','digitalocean/provision.py');key=self.key(self.root/'fixture-key')
        nodes={};payloads=[]
        def api(path,payload=None,method=None):
            if path=='account':return {'account':{'droplet_limit':10}}
            if path=='droplets?per_page=200':return {'droplets':[]}
            if path=='sizes?per_page=200':return {'sizes':[{'slug':'s-4vcpu-8gb','memory':8192,'available':True,'regions':['sgp1'],'price_monthly':48}]}
            if path=='account/keys?per_page=200':return {'ssh_keys':[{'id':1,'public_key':key.with_suffix('.pub').read_text()}]}
            if path=='droplets':
                payloads.append(payload);number=len(payloads);nodes[str(number)]={'id':number,'status':'active','networks':{'v4':[{'type':'public','ip_address':'192.0.2.'+str(number)},{'type':'private','ip_address':'10.0.0.'+str(number)}]},'memory':8192,'vcpus':4}
                return {'droplet':nodes[str(number)]}
            if path.startswith('droplets/'):return {'droplet':nodes[path.split('/')[-1]]}
            raise AssertionError(path)
        with patch.object(obj,'ROOT',self.root),patch.object(obj,'STATE',self.root/'.digitalocean'),patch.object(obj,'deployment_key',return_value=key),patch.object(obj,'api',side_effect=api),patch.object(sys,'argv',['fixture','--roles','controller','worker-1','worker-2','--apply']),contextlib.redirect_stdout(io.StringIO()):obj.main()
        self.assertEqual(len(payloads),3)
        for payload in payloads:
            cloud=json.loads(payload['user_data'].split('\n',1)[1])
            self.assertFalse(cloud['ssh_pwauth']);self.assertEqual(cloud['ssh_authorized_keys'],[key.with_suffix('.pub').read_text().strip()])
            self.assertIn('PasswordAuthentication no',cloud['runcmd'][0][-1])
        config=self.root/'.digitalocean/ssh_config'
        resolved=subprocess.check_output(['ssh','-G','-F',str(config),'cnc-do-controller'],text=True,stderr=subprocess.DEVNULL)
        self.assertIn('proxyjump cnc-do-worker-1',resolved)
        self.assertIn('hostname 10.0.0.1',resolved)
        self.assertIn('identityfile '+str(key),resolved)

    def test_upcloud_controller_and_worker_bootstrap_key_only(self):
        obj=module('upcloud_ssh_fixture','upcloud/provision.py');key=self.key(self.root/'fixture-key');nodes={};payloads=[]
        plan={'name':'fixture','current_offering':'yes','memory_amount':2048,'core_number':1,'storage_size':20,'storage_tier':'standard'}
        def api(path,payload=None):
            if path=='plan':return {'plans':{'plan':[plan]}}
            if path=='account':return {'account':{'resource_limits':{'memory':10000,'cores':10}}}
            if path=='storage/template':return {'storages':{'storage':[{'title':'Ubuntu Server 24.04','uuid':'template'}]}}
            if path=='server' and payload is None:return {'servers':{'server':[]}}
            if path=='server':
                payloads.append(payload['server']);number=str(len(payloads))
                nodes[number]={'uuid':number,'state':'started','ip_addresses':{'ip_address':[{'access':'public','family':'IPv4','address':'192.0.2.'+number},{'access':'utility','family':'IPv4','address':'10.0.0.'+number}]}}
                return {'server':nodes[number]}
            if path.startswith('server/'):return {'server':nodes[path.split('/')[1]]}
            raise AssertionError(path)
        with patch.object(obj,'ROOT',self.root),patch.object(obj,'deployment_key',return_value=key),patch.object(obj,'api',side_effect=api),patch.object(sys,'argv',['fixture','--plan','fixture','--workers','1','--apply','--provision-only']),contextlib.redirect_stdout(io.StringIO()):obj.main()
        self.assertEqual(len(payloads),2)
        for payload in payloads:
            self.assertEqual(payload['login_user']['create_password'],'no')
            self.assertEqual(payload['login_user']['ssh_keys']['ssh_key'],[key.with_suffix('.pub').read_text().strip()])
            self.assertIn('PasswordAuthentication no',payload['user_data'])

    def test_linode_controller_and_worker_bootstrap_key_only(self):
        import base64
        obj=module('linode_ssh_fixture','linode/provision.py');key=self.key(self.root/'fixture-key');payloads=[]
        def api(path,payload=None):
            if path.startswith('linode/types'):return {'data':[{'id':'fixture','price':{'monthly':1},'memory':2048}]}
            if path.startswith('linode/instances?'):return {'data':[]}
            if path=='linode/instances':
                payloads.append(payload)
                return {'id':len(payloads),'ipv4':['192.0.2.'+str(len(payloads))],'type':'fixture','region':'fixture'}
            raise AssertionError(path)
        with patch.object(obj,'ROOT',self.root),patch.object(obj,'STATE',self.root/'.linode'),patch.object(obj,'deployment_key',return_value=key),patch.object(obj,'default_worker_password',return_value=None),patch.object(obj,'api',side_effect=api),patch.object(sys,'argv',['fixture','--workers','1','--server-type','fixture','--worker-type','fixture','--apply']),contextlib.redirect_stdout(io.StringIO()):obj.main()
        self.assertEqual(len(payloads),2)
        for payload in payloads:
            script=base64.b64decode(payload['metadata']['user_data']).decode()
            self.assertIn('AuthenticationMethods publickey',script)
            self.assertIn('PasswordAuthentication no',script)
            self.assertEqual(payload['authorized_keys'],[key.with_suffix('.pub').read_text().strip()])


if __name__=='__main__':unittest.main()
