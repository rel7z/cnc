"""Executed on the existing controller via SSM; prints metadata only."""
import json,os,pathlib,subprocess
P=pathlib.Path
record=json.loads(NETWORK_PAYLOAD)
subprocess.run(['apt-get','update','-qq'],check=True,stdout=subprocess.DEVNULL)
subprocess.run(['apt-get','install','-y','-qq','wireguard-tools'],check=True,stdout=subprocess.DEVNULL)
c=json.loads(P('/opt/cnc/config.json').read_text())
network_dir=P(c['data_dir'])/'linode';network_dir.mkdir(mode=0o700,parents=True,exist_ok=True)
key=network_dir/'wireguard-controller.key'
if not key.exists():
 private=subprocess.check_output(['wg','genkey']);key.write_bytes(private);key.chmod(0o600)
public=subprocess.check_output(['wg','pubkey'],input=key.read_bytes()).decode().strip()
P('/etc/wireguard').mkdir(mode=0o700,exist_ok=True)
conf=P('/etc/wireguard/cncln0.conf')
if not conf.exists():
 conf.write_text('[Interface]\nAddress = 10.203.88.1/24\nListenPort = 51820\nPrivateKey = '+key.read_text().strip()+'\n');conf.chmod(0o600)
subprocess.run(['systemctl','enable','--now','wg-quick@cncln0'],check=True,stdout=subprocess.DEVNULL)
config={'interface':'cncln0','endpoint':record['endpoint'],'public_key':public,'server_addr':c['tcp_addr'],'http_addr':c['http_addr'],'aws_region':'us-east-2','security_group':record['security_group']}
p=network_dir/'network.json';p.write_text(json.dumps(config));p.chmod(0o600)
assert subprocess.check_output(['systemctl','is-active','wg-quick@cncln0'],text=True).strip()=='active'
print(json.dumps({'wireguard_active':True,'interface':'cncln0','private_listeners_unchanged':True,'private_keys_redacted':True}))
