#!/usr/bin/env python3
"""Install a supplied Apify token in persistent worker systemd configuration.

Never places the token in command arguments, job payloads, Git or stdout.
Usage: python3 infra/linode/configure-apify.py --token-file /absolute/private/file
"""
import argparse
import json
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[2]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--token-file', required=True, type=Path)
    args = parser.parse_args()
    token = args.token_file.read_text().strip()
    if token.startswith('APIFY_TOKEN='):
        token = token.split('=', 1)[1].strip().strip('\"\'')
    if not token or any(c.isspace() or c in '\"\'\\' for c in token):
        raise SystemExit('Token file must contain a single token or APIFY_TOKEN=value')
    state = json.loads((ROOT / '.linode/state.json').read_text())
    workers = sorted(name for name in state['servers'] if '-worker-' in name)
    if not workers:
        raise SystemExit('No configured workers')
    # Remote script consumes the token only from SSH stdin and checks presence,
    # without revealing its value. The env file survives subsequent deployments.
    remote = r'''import json,os,subprocess,sys,time
from pathlib import Path
token=sys.stdin.read().strip()
p=Path('/opt/cnc/apify.env')
fd=os.open(p,os.O_WRONLY|os.O_CREAT|os.O_TRUNC,0o600)
os.fchmod(fd,0o600)
with os.fdopen(fd,'w') as f:f.write('APIFY_TOKEN='+token+'\n')
d=Path('/etc/systemd/system/cnc-worker.service.d');d.mkdir(parents=True,exist_ok=True)
(d/'apify.conf').write_text('[Service]\nEnvironmentFile=/opt/cnc/apify.env\n')
subprocess.run(['systemctl','daemon-reload'],check=True)
subprocess.run(['systemctl','restart','cnc-worker'],check=True)
subprocess.run(['systemctl','is-active','--quiet','cnc-worker'],check=True)
for attempt in range(10):
    pid=subprocess.check_output(['systemctl','show','cnc-worker','--property=MainPID','--value'],text=True).strip()
    env=Path('/proc/'+pid+'/environ').read_bytes().split(b'\0')
    if any(v==('APIFY_TOKEN='+token).encode() for v in env):break
    time.sleep(0.2)
else:raise RuntimeError('Worker token environment not verified')
print(json.dumps({'service':'active','apify_token':'configured','permissions':'0600'}))
'''
    import shlex
    for name in workers:
        result = subprocess.run(['ssh', '-F', str(ROOT / '.linode/ssh_config'), name,
                                 'python3 -c ' + shlex.quote(remote)], input=token,
                                text=True, capture_output=True)
        if result.returncode:
            # Don't emit remote stderr: credentials must stay out of tool logs.
            raise SystemExit('Worker configuration failed: ' + name)
        print(name + ': ' + result.stdout.strip(), flush=True)
    print('Token configured on ' + str(len(workers)) + ' workers; no queries executed.')


if __name__ == '__main__':
    main()
