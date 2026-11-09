#!/usr/bin/env python3
"""Deploy default no-timeout jobs using permitted production CNC/UI sources.

Never walks, reads, archives or checks out processor/tools. The old broad
release script must not be used for this task because it includes that subtree.
"""
import io,json,os,shlex,subprocess,tarfile,tempfile
from pathlib import Path

ROOT=Path(__file__).resolve().parents[2]
STATE=ROOT/'.linode'
REF='refs/heads/codex/sync-tool-fixes'

def run(args,**kw): return subprocess.run(args,check=True,**kw)
def git(*args,**kw): return run(['git','-C',str(ROOT),*args],**kw)

def main():
    nodes=json.loads((STATE/'state.json').read_text())['servers']
    server=next(name for name in nodes if name.endswith('-server'))
    ssh=['ssh','-F',str(STATE/'ssh_config'),server]
    base=run(ssh+['cat /opt/cnc/RELEASE'],capture_output=True,text=True).stdout.strip()
    # Read only these permitted Git paths; the live cluster source overlay follows.
    archive=git('archive',base,'cnc-api','cnc-ui',capture_output=True).stdout
    # The running binary can be newer than RELEASE. Preserve deployed API sources
    # and its permitted cluster package, never traversing processor/tools.
    snapshot_script = """import io,sys,tarfile
from pathlib import Path
root=Path('/opt/cnc/no-timeout-source')
paths=list((root/'cnc-api').glob('*.go'))+[root/'cnc-api/go.mod',root/'cnc-api/go.sum']
paths+=list((root/'cnc-api/cmd').rglob('*.go'))
paths+=[root/'processor/go.mod',root/'processor/go.sum']
paths+=list((root/'processor/cluster').glob('*.go'))
with tarfile.open(fileobj=sys.stdout.buffer,mode='w|') as archive:
    for path in paths:
        if path.is_file(): archive.add(path,arcname=str(path.relative_to(root)),recursive=False)
"""
    live_archive=run(ssh+['python3 -c '+shlex.quote(snapshot_script)],capture_output=True).stdout
    with tempfile.TemporaryDirectory(prefix='cnc-upload-release-') as directory:
        stage=Path(directory)
        with tarfile.open(fileobj=io.BytesIO(archive)) as tar: tar.extractall(stage,filter='data')
        with tarfile.open(fileobj=io.BytesIO(live_archive)) as tar: tar.extractall(stage,filter='data')
        for name in ['cnc-api/tools.go','cnc-api/server_tool_test.go','cnc-ui/components/dashboard/Header.tsx','cnc-backend/cms-scan/cms.go','cnc-backend/cms-scan/cms_test.go','cnc-backend/cms-scan/go.mod']:
            target=stage/name;target.parent.mkdir(parents=True,exist_ok=True);target.write_bytes((ROOT/name).read_bytes())
        server_go=stage/'cnc-api/server.go'
        content=server_go.read_text()
        if '/api/fs/upload' not in content:
            content=content.replace('mux.HandleFunc("/api/fs/list", s.handleFSListAPI)','mux.HandleFunc("/api/fs/list", s.handleFSListAPI)\n\tmux.HandleFunc("/api/fs/upload", s.handleFSUploadAPI)')
        server_go.write_text(content)
        # Independent source-only Git tree keeps unrelated local work untouched.
        env=dict(os.environ,GIT_INDEX_FILE=str(stage/'index'))
        git('read-tree','--empty',env=env)
        git('--work-tree='+str(stage),'add','-f','--','cnc-api','cnc-ui','processor/go.mod','processor/go.sum','processor/cluster','cnc-backend/cms-scan',env=env)
        tree=git('write-tree',env=env,capture_output=True,text=True).stdout.strip()
        revision=git('commit-tree',tree,input='Sync CMS crash fix, Reverse IP worker mode and logout text on production '+base+'\n',text=True,capture_output=True).stdout.strip()
        git('update-ref',REF,revision)
    env=dict(os.environ,GIT_SSH_COMMAND=shlex.join(['ssh','-F',str(STATE/'ssh_config')]))
    git('push',server+':/opt/cnc/git/repo.git','+'+REF+':refs/heads/sync-tool-fixes',env=env)
    remote='''set -eu
mkdir -p /opt/cnc/sync-tool-source
git --git-dir=/opt/cnc/git/repo.git archive sync-tool-fixes cnc-api cnc-ui processor/go.mod processor/go.sum processor/cluster cnc-backend/cms-scan | tar -x -C /opt/cnc/sync-tool-source
cd /opt/cnc/sync-tool-source/cnc-api
/opt/cnc/go/bin/go test ./...
CGO_ENABLED=0 /opt/cnc/go/bin/go build -ldflags='-s -w' -o /opt/cnc/cnc-server.timeout-new ./cmd/server
cd /opt/cnc/sync-tool-source/cnc-backend/cms-scan
/opt/cnc/go/bin/go test ./...
CGO_ENABLED=0 /opt/cnc/go/bin/go build -ldflags='-s -w' -o /opt/cnc/tools/cms-scan.new .
mv /opt/cnc/tools/cms-scan.new /opt/cnc/tools/cms-scan
cd /opt/cnc/sync-tool-source/cnc-ui
export PATH="$(cat /opt/cnc/node-path)/bin:$PATH"
npm ci --no-audit --no-fund
GO_API_URL="http://PRIVATE_IP:8080" npm run build -- --webpack
cp -r public .next/standalone/ 2>/dev/null || true
cp -r .next/static .next/standalone/.next/
test -f /opt/cnc/cnc-server.before-no-timeout || cp /opt/cnc/cnc-server /opt/cnc/cnc-server.before-no-timeout
mv /opt/cnc/cnc-server.timeout-new /opt/cnc/cnc-server
chmod 700 /opt/cnc/cnc-server
python3 - <<'UPDATE_UI'
from pathlib import Path
p=Path('/etc/systemd/system/cnc-ui.service')
s=p.read_text().replace('/opt/cnc/source/cnc-ui/.next/standalone','/opt/cnc/sync-tool-source/cnc-ui/.next/standalone').replace('/opt/cnc/file-manager-source/cnc-ui/.next/standalone','/opt/cnc/sync-tool-source/cnc-ui/.next/standalone').replace('/opt/cnc/no-timeout-source/cnc-ui/.next/standalone','/opt/cnc/sync-tool-source/cnc-ui/.next/standalone')
p.write_text(s)
UPDATE_UI
systemctl daemon-reload
systemctl restart cnc-server cnc-ui
systemctl is-active cnc-server cnc-ui
printf '%s\\n' 'REVISION' > /opt/cnc/RELEASE
'''.replace('PRIVATE_IP',nodes[server]['private_ip']).replace('REVISION',revision)
    print('Deploying tool fixes Git release '+revision,flush=True)
    run(ssh+['bash','-s'],input=remote,text=True)
    state=json.loads((STATE/'state.json').read_text());state.update(revision=revision,tool_fixes_revision=revision)
    (STATE/'state.json').write_text(json.dumps(state,indent=2)+'\n');(STATE/'state.json').chmod(0o600)
    print(json.dumps({'revision':revision,'server':nodes[server]['public_ip'],'processor_tools_accessed':False}),flush=True)

if __name__=='__main__': main()
