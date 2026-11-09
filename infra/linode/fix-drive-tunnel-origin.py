#!/usr/bin/env python3
"""Preserve the browser-facing localhost port through nginx for Drive OAuth."""
import json
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[2]
SSH_CONFIG = ROOT / ".linode/ssh_config"
HOST = "cnc-linode-server"

REMOTE = r'''
import json,os,shutil,subprocess
from pathlib import Path
link=Path('/etc/nginx/sites-enabled/cnc')
target=link.resolve(strict=True)
if not str(target).startswith('/etc/nginx/') or not target.is_file():raise RuntimeError('unexpected nginx site path')
original=target.read_bytes()
old=b'proxy_set_header Host $host;'
new=b'proxy_set_header Host $http_host;'
if original.count(old)!=1:
 if original.count(new)==1:print(json.dumps({'changed':False,'target':str(target),'reason':'already configured'}));raise SystemExit
 raise RuntimeError('expected exactly one Host proxy directive')
rollback=Path('/opt/cnc/queue-releases/drive-oauth-bridge-1f6004ea1cfab44c/rollback/nginx-cnc-before-http-host')
rollback.parent.mkdir(parents=True,exist_ok=True)
if not rollback.exists():rollback.write_bytes(original);rollback.chmod(0o600)
stage=target.with_name(target.name+'.drive-origin-new');stage.write_bytes(original.replace(old,new));stage.chmod(target.stat().st_mode&0o777);stage.replace(target)
try:
 subprocess.run(['nginx','-t'],check=True,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True)
 subprocess.run(['systemctl','reload','nginx.service'],check=True)
except Exception:
 restore=target.with_name(target.name+'.drive-origin-rollback');restore.write_bytes(original);restore.chmod(target.stat().st_mode&0o777);restore.replace(target)
 subprocess.run(['systemctl','reload','nginx.service'],check=False)
 raise
print(json.dumps({'changed':True,'target':str(target),'directive':'proxy_set_header Host $http_host;','nginx_reloaded':True,'rollback':str(rollback)}))
'''


def main():
    output = subprocess.check_output(
        ["ssh", "-F", str(SSH_CONFIG), "-o", "BatchMode=yes", HOST, "python3", "-"],
        input=REMOTE.encode(),
    ).decode()
    print(json.dumps(json.loads(output), indent=2))


if __name__ == "__main__":
    main()
