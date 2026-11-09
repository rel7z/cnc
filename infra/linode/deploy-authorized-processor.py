#!/usr/bin/env python3
"""Deploy an explicit approved file list through a source-only Git bundle."""
from pathlib import Path
import os
import shlex
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[2]
BRANCH = "codex/private-processor-routes"
FILES = [
    "processor/config.json",
    "processor/tools/3-tools/common.py",
    "processor/tools/3-tools/task_a.py",
    "processor/tools/3-tools/task_b.py",
    "infra/linode/deploy-authorized-processor.py",
]

def ssh(host, command, data=None, capture=False):
    return subprocess.run(["ssh", "-F", str(ROOT/".linode/ssh_config"), host, command],
                          input=data, check=True, stdout=subprocess.PIPE if capture else None)

REMOTE = r"""
import json, shutil, subprocess, urllib.request
from pathlib import Path
env = dict(line.split('=',1) for line in Path('/opt/cnc/processor.env').read_text().splitlines()
           if '=' in line and not line.startswith('#'))
def api(path, body=None):
    req=urllib.request.Request('http://127.0.0.1:9191/v1/'+path,
        data=json.dumps(body).encode() if body is not None else None,
        method='PUT' if body is not None else 'GET',
        headers={'Authorization':'Bearer '+env['CNC_PROCESSOR_TOKEN'].strip('"'),
                 'Content-Type':'application/json'})
    return json.load(urllib.request.urlopen(req, timeout=10))
enabled = api('health')['enabled']
active = [r for r in api('runs') or [] if r['Status'] in ('running','queued','dispatching')]
if active:
    raise RuntimeError('Active processor runs; finish them before deployment')
release = Path('/opt/cnc-processor/releases/REVISION')
release.mkdir(parents=True, exist_ok=True)
subprocess.run(['git','--git-dir=/opt/cnc-processor/git/repo.git','--work-tree='+str(release),
                'checkout','-f','REVISION','--','.'], check=True, stdout=subprocess.DEVNULL)
app = Path('/opt/cnc-processor')
backup = app/'config.before-current-authorized-release.json'
shutil.copy2(app/'config.json',backup)
previous_config=json.loads(backup.read_text())
previous_programs={p for programs in previous_config.get('routes',{}).values() for p in programs}
approved_programs={'tools/3-tools/wordpress_authorized.py','tools/3-tools/ftp_checker.py',
                   'task-a','task-b'}
code_backup={relative:(app/relative).read_bytes() if (app/relative).exists() else None
             for relative in ('tools/3-tools/common.py','tools/3-tools/task_a.py','tools/3-tools/task_b.py')}
api('processing-state',{'enabled':False})
try:
    for relative in ('tools/3-tools/common.py',
                     'tools/3-tools/task_a.py','tools/3-tools/task_b.py'):
        dst=app/relative
        dst.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(release/'processor'/relative,dst)
        dst.chmod(0o600)
    shutil.copy2(release/'processor/config.json',app/'config.json')
    (app/'config.json').chmod(0o600)
    subprocess.run(['systemctl','restart','cnc-processor'],check=True)
    import time
    for _ in range(20):
        try:
            api('health')
            break
        except OSError:
            time.sleep(.5)
    else:
        raise RuntimeError('Processor failed to become healthy')
    api('processing-state',{'enabled':enabled})
    (app/'AUTHORIZED_RELEASE').write_text('REVISION\n')
    print(json.dumps({'release':'REVISION','health':api('health')}))
except Exception:
    if previous_programs <= approved_programs:
        for relative,data in code_backup.items():
            dst=app/relative
            if data is None:
                dst.unlink(missing_ok=True)
            else:
                dst.write_bytes(data)
                dst.chmod(0o600)
        shutil.copy2(backup,app/'config.json')
        subprocess.run(['systemctl','restart','cnc-processor'],check=True)
        import time
        for _ in range(20):
            try:
                api('processing-state',{'enabled':enabled})
                break
            except OSError:
                time.sleep(.5)
    else:
        subprocess.run(['systemctl','stop','cnc-processor'],check=True)
        print('No approved rollback programs; processor stopped for review')
    raise
"""

def main():
    # Separate empty index: never stage or inspect unrelated/restricted files.
    with tempfile.TemporaryDirectory(prefix="cnc-authorized-release-") as temp:
        env = os.environ.copy()
        env["GIT_INDEX_FILE"] = str(Path(temp)/"index")
        def git(*args):
            return subprocess.check_output(["git", *args], cwd=ROOT, env=env).decode().strip()
        git("read-tree", "--empty")
        git("add", "--", *FILES)
        tree = git("write-tree")
        # Root commit keeps unrelated tool history outside this approved bundle.
        revision = git("commit-tree", tree, "-m",
                       "Configure private processor input aliases")
        git("update-ref", "refs/heads/"+BRANCH, revision)
        bundle = Path(temp)/"processor.bundle"
        git("bundle", "create", str(bundle), "refs/heads/"+BRANCH)
        ssh("cnc-linode-server", "mkdir -p /opt/cnc-processor/git; "
            "test -d /opt/cnc-processor/git/repo.git || git init --bare -q /opt/cnc-processor/git/repo.git")
        ssh("cnc-linode-server", "umask 077; cat > /opt/cnc-processor/git/authorized.bundle", bundle.read_bytes())
        ssh("cnc-linode-server", "git --git-dir=/opt/cnc-processor/git/repo.git fetch -q "
            "/opt/cnc-processor/git/authorized.bundle refs/heads/"+BRANCH+":refs/heads/"+BRANCH)
        ssh("cnc-linode-server", "python3 -", REMOTE.replace("REVISION",revision).encode())
        print("Source-only Git release:", revision)

if __name__ == "__main__":
    main()
