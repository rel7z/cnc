#!/usr/bin/env python3
"""Restrict the passwordless Drive OAuth start route to the SSH tunnel."""
import json
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[2]
SSH_CONFIG = ROOT / ".linode/ssh_config"
HOST = "cnc-linode-server"

REMOTE = r'''
import json,subprocess
from pathlib import Path
target=Path('/etc/nginx/sites-enabled/cnc').resolve(strict=True)
if not str(target).startswith('/etc/nginx/') or not target.is_file():raise RuntimeError('unexpected nginx site path')
original=target.read_bytes()
marker=b'    location / {\n'
block=b"""    location = /drive-local-start {
        allow 127.0.0.1;
        allow ::1;
        deny all;
        proxy_pass http://127.0.0.1:3000;
        proxy_set_header Host $http_host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto http;
        proxy_set_header Authorization "";
        proxy_buffering off;
    }
"""
if block in original:
 print(json.dumps({'changed':False,'reason':'already configured','target':str(target)}));raise SystemExit
if original.count(marker)!=1:raise RuntimeError('could not locate nginx catch-all route')
rollback=Path('/opt/cnc/queue-releases/drive-oauth-bridge-dd15657770bf9c22/rollback/nginx-cnc-before-local-start')
rollback.parent.mkdir(parents=True,exist_ok=True)
if not rollback.exists():rollback.write_bytes(original);rollback.chmod(0o600)
stage=target.with_name(target.name+'.drive-local-start-new');stage.write_bytes(original.replace(marker,block+marker));stage.chmod(target.stat().st_mode&0o777);stage.replace(target)
try:
 subprocess.run(['nginx','-t'],check=True,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True)
 subprocess.run(['systemctl','reload','nginx.service'],check=True)
except Exception:
 restore=target.with_name(target.name+'.drive-local-start-rollback');restore.write_bytes(original);restore.chmod(target.stat().st_mode&0o777);restore.replace(target)
 subprocess.run(['systemctl','reload','nginx.service'],check=False)
 raise
print(json.dumps({'changed':True,'target':str(target),'route':'/drive-local-start','allowed_sources':['127.0.0.1','::1'],'nginx_reloaded':True,'rollback':str(rollback)}))
'''


def main():
    output = subprocess.check_output(
        ["ssh", "-F", str(SSH_CONFIG), "-o", "BatchMode=yes", HOST, "python3", "-"],
        input=REMOTE.encode(),
    ).decode()
    print(json.dumps(json.loads(output), indent=2))


if __name__ == "__main__":
    main()
