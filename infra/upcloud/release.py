#!/usr/bin/env python3
"""Create a source-only Git release and deploy the integrated UpCloud cluster."""
import base64
import json
import os
from pathlib import Path
import re
import shlex
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parents[2]
STATE = ROOT / '.upcloud'
REF = 'refs/heads/codex/upcloud-deployment'


def run(args, **kwargs):
    return subprocess.run(args, check=True, **kwargs)


def git(*args, **kwargs):
    return run(['git', '-C', str(ROOT), *args], **kwargs)


def snapshot():
    index = STATE / 'release-index'
    if index.exists(): index.unlink()
    env = dict(os.environ, GIT_INDEX_FILE=str(index))
    git('read-tree', '--empty', env=env)
    paths = ['.gitignore']
    extensions = {'.go', '.mod', '.sum', '.ts', '.tsx', '.js', '.mjs', '.css', '.json', '.svg', '.png', '.ico', '.md', '.sh', '.py'}
    for directory in ['cnc-api', 'cnc-backend', 'cnc-ui', 'infra/upcloud']:
        for path in (ROOT / directory).rglob('*'):
            relative = path.relative_to(ROOT)
            if not path.is_file() or any(part in {'node_modules', '.next', '__pycache__', '.git'} for part in relative.parts): continue
            if path.name.startswith('.env') or path.name.endswith('_config.json') or path.name in {'server_config.json', 'worker_config.json', 'cnc_config.json', 'credentials.json', 'token.json'}: continue
            if path.suffix in extensions or path.name == 'Makefile': paths.append(str(relative))
    git('add', '-f', '--', *paths, env=env)
    tree = git('write-tree', env=env, capture_output=True, text=True).stdout.strip()
    previous = subprocess.run(['git', '-C', str(ROOT), 'rev-parse', '--verify', REF], capture_output=True, text=True)
    parents = ['-p', previous.stdout.strip()] if previous.returncode == 0 else []
    revision = git('commit-tree', tree, *parents, input='Deploy CNC on UpCloud without credential brute forcing\n', text=True, capture_output=True).stdout.strip()
    git('update-ref', REF, revision)
    index.unlink()
    return revision


def main():
    STATE.mkdir(mode=0o700, exist_ok=True)
    server_only = '--server-only' in sys.argv[1:]
    public_dashboard = '--public-dashboard' in sys.argv[1:]
    provision_args = [arg for arg in sys.argv[1:] if arg not in ('--server-only', '--public-dashboard')]
    run([sys.executable, str(ROOT / 'infra/upcloud/provision.py'), '--apply', '--provision-only', *provision_args], cwd=ROOT)
    nodes = json.loads((STATE / 'state.json').read_text())['servers']
    server_name = next(name for name in nodes if name.endswith('-server'))
    workers = {} if server_only else {name: node for name, node in nodes.items() if '-worker-' in name}
    revision = snapshot()
    ssh = ['ssh', '-F', str(STATE / 'ssh_config'), server_name]
    run(ssh + ['mkdir -p /opt/cnc/git; test -d /opt/cnc/git/repo.git || git init --bare -q /opt/cnc/git/repo.git'])
    env = dict(os.environ, GIT_SSH_COMMAND=shlex.join(['ssh', '-F', str(STATE / 'ssh_config')]))
    git('push', server_name + ':/opt/cnc/git/repo.git', REF + ':refs/heads/deployment', env=env)
    print('Deploying Git revision ' + revision, flush=True)
    agent = run(['ssh-agent', '-s'], capture_output=True, text=True).stdout
    sock = re.search(r'SSH_AUTH_SOCK=([^;]+)', agent).group(1)
    pid = re.search(r'SSH_AGENT_PID=([^;]+)', agent).group(1)
    agent_env = dict(os.environ, SSH_AUTH_SOCK=sock, SSH_AGENT_PID=pid)
    try:
        run(['ssh-add', str(STATE / 'id_ed25519')], env=agent_env)
        server_ip = nodes[server_name]['private_ip']
        configs = {server_name: {'http_addr': server_ip + ':8080', 'tcp_addr': server_ip + ':9090', 'data_dir': '/opt/cnc/data', 'gdrive': {'enabled': False}, 'telegram': {'enabled': False}}}
        configs.update({name: {'server_addr': server_ip + ':9090', 'worker_id': name, 'max_tasks': 2, 'data_dir': '/opt/cnc/data'} for name in workers})
        encoded = base64.b64encode(json.dumps({'revision': revision, 'nodes': {name: node for name, node in nodes.items() if name == server_name or name in workers}, 'configs': configs, 'server': server_name}).encode()).decode()
        remote = "set -eu\nexport DEPLOY_DATA=" + shlex.quote(encoded) + "\n" + '''
mkdir -p /opt/cnc/source
git --git-dir=/opt/cnc/git/repo.git --work-tree=/opt/cnc/source checkout -f deployment
python3 - <<'INSTALL_GO'
import urllib.request,json,hashlib,tarfile
from pathlib import Path
if not Path('/opt/cnc/go/bin/go').exists():
 with urllib.request.urlopen('https://go.dev/dl/?mode=json',timeout=60) as response: releases=json.load(response)
 release=next(r for r in releases if r['stable'])
 artifact=next(f for f in release['files'] if f['os']=='linux' and f['arch']=='amd64' and f['kind']=='archive')
 with urllib.request.urlopen('https://go.dev/dl/'+artifact['filename'],timeout=120) as response: data=response.read()
 if hashlib.sha256(data).hexdigest()!=artifact['sha256']: raise RuntimeError('Go checksum mismatch')
 archive=Path('/opt/cnc/go.tar.gz'); archive.write_bytes(data)
 with tarfile.open(archive) as tar: tar.extractall('/opt/cnc',filter='data')
INSTALL_GO
cd /opt/cnc/source/cnc-api
/opt/cnc/go/bin/go test ./...
CGO_ENABLED=0 /opt/cnc/go/bin/go build -ldflags='-s -w' -o /opt/cnc/cnc-server.new ./cmd/server
CGO_ENABLED=0 /opt/cnc/go/bin/go build -ldflags='-s -w' -o /opt/cnc/cnc-worker.new ./cmd/worker
bash /opt/cnc/source/infra/upcloud/install-tools.sh
python3 - <<'DEPLOY'
import base64,json,os,subprocess
from pathlib import Path
data=json.loads(base64.b64decode(os.environ['DEPLOY_DATA']))
def install(name,kind,remote=False):
 config=json.dumps(data['configs'][name])
 service=f'[Unit]\\nDescription=CNC {kind}\\nAfter=network-online.target\\nWants=network-online.target\\n[Service]\\nWorkingDirectory=/opt/cnc\\nExecStart=/opt/cnc/cnc-{kind} -config=/opt/cnc/config.json\\nRestart=on-failure\\nRestartSec=5\\n[Install]\\nWantedBy=multi-user.target\\n'
 script=f"set -eu\\nmv /opt/cnc/cnc-{kind}.new /opt/cnc/cnc-{kind}\\nchmod 700 /opt/cnc/cnc-{kind}\\ncat > /opt/cnc/config.json <<'CONFIG'\\n{config}\\nCONFIG\\ncat > /etc/systemd/system/cnc-{kind}.service <<'UNIT'\\n{service}UNIT\\nprintf '%s\\n' '{data['revision']}' > /opt/cnc/RELEASE\\nsystemctl daemon-reload\\nsystemctl enable cnc-{kind}\\nsystemctl restart cnc-{kind}\\nsystemctl is-active cnc-{kind}\\n"
 if remote:
  dest='root@'+data['nodes'][name]['private_ip']
  opts=['-o','BatchMode=yes','-o','StrictHostKeyChecking=accept-new','-o','ConnectTimeout=10']
  subprocess.run(['ssh',*opts,dest,'mkdir -p /opt/cnc'],check=True)
  subprocess.run(['scp',*opts,'/opt/cnc/cnc-worker.new',dest+':/opt/cnc/cnc-worker.new'],check=True)
  subprocess.run(['scp',*opts,'/opt/cnc/worker-tools.tar.gz',dest+':/opt/cnc/worker-tools.tar.gz'],check=True)
  subprocess.run(['ssh',*opts,dest,'tar -xzf /opt/cnc/worker-tools.tar.gz -C /opt/cnc'],check=True)
  subprocess.run(['ssh',*opts,dest,'bash -s'],input=script,text=True,check=True)
 else: subprocess.run(['bash','-s'],input=script,text=True,check=True)
install(data['server'],'server')
for name in data['nodes']:
 if name!=data['server']: install(name,'worker',True)
DEPLOY
cd /opt/cnc/source
PRIVATE_IP=$(python3 -c 'import os,json,base64;d=json.loads(base64.b64decode(os.environ["DEPLOY_DATA"]));print(d["nodes"][d["server"]]["private_ip"])')
export PRIVATE_IP
'''
        run(['ssh', '-A', '-F', str(STATE / 'ssh_config'), server_name, 'bash', '-s'], input=remote, text=True, env=agent_env)
        # Build the web frontend from the exact Git checkout already deployed.
        ui_script=(ROOT / 'infra/upcloud/deploy-ui.sh').read_text()
        body=ui_script.split("<<'REMOTE'\n",1)[1].rsplit('\nREMOTE',1)[0]
        body=body.replace('mkdir -p /opt/cnc/ui\ncd /opt/cnc/ui\ntar -xzf /opt/cnc/ui-source.tar.gz','cd /opt/cnc/source/cnc-ui')
        body=body.replace('/opt/cnc/ui/.next/standalone','/opt/cnc/source/cnc-ui/.next/standalone')
        run(ssh + ['bash','-s'],input='export PRIVATE_IP='+shlex.quote(server_ip)+'\n'+body,text=True)
        if public_dashboard:
            run([sys.executable, str(ROOT / 'infra/upcloud/public-access.py')], cwd=ROOT)
        deadline=time.monotonic()+90
        while True:
            result=run(ssh+['curl -fsS http://'+server_ip+':8080/api/workers'],capture_output=True,text=True)
            online=[w for w in json.loads(result.stdout) if w['id'] in workers and w['status'] in ('online','busy')]
            if len(online)==len(workers): break
            if time.monotonic()>deadline: raise RuntimeError('Not all workers registered')
            time.sleep(3)
        for name in [server_name, *workers]:
            actual=run(['ssh','-F',str(STATE/'ssh_config'),name,'cat /opt/cnc/RELEASE'],capture_output=True,text=True).stdout.strip()
            if actual!=revision: raise RuntimeError('Revision mismatch on '+name)
        state=json.loads((STATE/'state.json').read_text()); state.update(revision=revision,verified_workers=len(online),dashboard_verified=True,workers_deployed=not server_only)
        (STATE/'state.json').write_text(json.dumps(state,indent=2)+'\n'); (STATE/'state.json').chmod(0o600)
        print(json.dumps({'server_ip':nodes[server_name]['public_ip'],'revision':revision,'workers_online':len(online),'dashboard':'verified'}),flush=True)
    finally:
        subprocess.run(['ssh-agent','-k'],env=agent_env,stdout=subprocess.DEVNULL)

if __name__=='__main__': main()
