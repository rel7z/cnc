#!/usr/bin/env python3
"""Apply the owner's saved root-login policy to the verified existing workers.

No workloads are executed or restarted. Existing root/SSH settings are retained
in private rollback storage. A password-only verification binary is required.
"""
import argparse,json,shlex,subprocess,sys,time
from pathlib import Path
sys.path.insert(0,str(Path(__file__).resolve().parents[1]))
from worker_login_policy import default_worker_password,password_setup
ROOT=Path(__file__).resolve().parents[2]

def main():
 parser=argparse.ArgumentParser(description=__doc__)
 parser.add_argument('--inventory',type=Path,required=True,help='JSON list of worker id/address pairs')
 parser.add_argument('--verifier',type=Path,required=True)
 parser.add_argument('--release',required=True)
 args=parser.parse_args()
 if not args.release.replace('-','').isalnum():raise RuntimeError('Invalid release')
 password=default_worker_password(ROOT)
 if not password:raise RuntimeError('Configure the private worker password policy first')
 workers=json.loads(args.inventory.read_text())
 expected=set(json.loads((ROOT/'.linode/active-deployment.json').read_text())['workers'])
 if {w['id'] for w in workers}!=expected or len(workers)!=len(expected):raise RuntimeError('Inventory must match all active deployment workers exactly')
 known=ROOT/'.linode/cross-cloud-known_hosts'
 receipts=[]
 for worker in workers:
  name,address=worker['id'],worker['address']
  if not name.replace('-','').isalnum():raise RuntimeError('Invalid worker ID')
  backup='/opt/cnc/rollouts/'+args.release+'/login-rollback'
  script='''set -euo pipefail
umask 077
actual=$(python3 -c 'import json; print(json.load(open("/opt/cnc/config.json"))["worker_id"])')
test "$actual" = NAME
before=$(systemctl show cnc-worker -p MainPID --value)
systemctl is-active --quiet cnc-worker
if ! test -e BACKUP/root-shadow; then
install -d -m 700 BACKUP
getent shadow root > BACKUP/root-shadow
cp -p /etc/ssh/sshd_config BACKUP/sshd_config
if test -f /etc/ssh/sshd_config.d/00-cnc-worker-login.conf; then cp -p /etc/ssh/sshd_config.d/00-cnc-worker-login.conf BACKUP/login.conf; fi
fi
rollback() {
 cut -d: -f1,2 BACKUP/root-shadow | chpasswd -e
 cp -p BACKUP/sshd_config /etc/ssh/sshd_config
 if test -f BACKUP/login.conf; then cp -p BACKUP/login.conf /etc/ssh/sshd_config.d/00-cnc-worker-login.conf; else rm -f /etc/ssh/sshd_config.d/00-cnc-worker-login.conf; fi
 /usr/sbin/sshd -t && systemctl reload ssh
}
trap rollback ERR
SETUP
/usr/sbin/sshd -T -C user=root,host=localhost,addr=127.0.0.1 | grep -x 'permitrootlogin yes' > /dev/null
/usr/sbin/sshd -T -C user=root,host=localhost,addr=127.0.0.1 | grep -x 'passwordauthentication yes' > /dev/null
test "$before" = "$(systemctl show cnc-worker -p MainPID --value)"
systemctl is-active --quiet cnc-worker
trap - ERR
'''.replace('NAME',shlex.quote(name)).replace('BACKUP',shlex.quote(backup)).replace('SETUP',password_setup(password))
  command=['ssh','-i',str(ROOT/'.linode/id_ed25519'),'-o','BatchMode=yes','-o','StrictHostKeyChecking=yes','-o','UserKnownHostsFile='+str(known),'root@'+address,'bash -se']
  subprocess.run(command,input=script,text=True,check=True,capture_output=True,timeout=60)
  result=subprocess.run([str(args.verifier),str(ROOT/'.linode/worker-root-password'),str(known),name+'='+address],check=True,capture_output=True,text=True,timeout=30)
  receipt=json.loads(result.stdout);receipt['rollback']=backup;receipts.append(receipt)
  print(json.dumps(receipt),flush=True)
  checkpoint=ROOT/'.linode/worker-login-verification.json'
  checkpoint.write_text(json.dumps({'release':args.release,'workers':receipts},indent=2)+'\n');checkpoint.chmod(0o600)
 (ROOT/'.linode/worker-login-verification.json').write_text(json.dumps({'release':args.release,'workers':receipts},indent=2)+'\n')
 (ROOT/'.linode/worker-login-verification.json').chmod(0o600)

if __name__=='__main__':main()
