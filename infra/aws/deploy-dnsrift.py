#!/usr/bin/env python3
"""Deploy a narrow DNSRift integration over the verified live source baseline."""
import base64,gzip,hashlib,json,os,subprocess,sys,tempfile
from pathlib import Path
ROOT=Path(__file__).resolve().parents[2]
names=['cnc-backend/reverseip/src3/'+x for x in ('go.mod','main.go','main_test.go')]+['cnc-api/reverseip_dnsrift_test.go','cnc-api/worker_tools_package.py','cnc-ui/components/dashboard/ToolSelectDropdown.tsx']
files={name:(ROOT/name).read_text() for name in names}
tools=(ROOT/'cnc-api/tools.go').read_text();start=tools.index('\t"reverseip-dnsrift": {');end=tools.index('\n\t"reverseip-thc": {',start)
payload={'files':files,'registry_block':tools[start:end],'activate':'--activate' in sys.argv}
snapshot_names=names+['cnc-api/tools.go','cnc-api/server_tool_test.go','cnc-api/Makefile','cnc-api/cli.go','infra/aws/deploy-dnsrift.py','infra/aws/deploy-dnsrift-remote.py','infra/aws/install-dnsrift-workers.py']
with tempfile.TemporaryDirectory(prefix='cnc-dnsrift-git-') as directory:
 env=dict(os.environ,GIT_INDEX_FILE=str(Path(directory)/'index'))
 def git(*args):return subprocess.check_output(['git',*args],cwd=ROOT,env=env,text=True).strip()
 git('read-tree','--empty');git('add','--',*snapshot_names)
 revision=git('commit-tree',git('write-tree'),'-m','Deploy third load-balanced reverse IP provider DNSRift and grouped launcher')
 git('update-ref','refs/heads/codex/dnsrift-integration-audit',revision)
payload['git_revision']=revision
encoded=base64.b64encode(gzip.compress(json.dumps(payload).encode())).decode()
script="python3 - <<'DNSRIFT'\n"+(ROOT/'infra/aws/deploy-dnsrift-remote.py').read_text().replace('SOURCE_PAYLOAD',repr(encoded))+"\nDNSRIFT\n"
p=Path('/private/tmp/cnc-deploy-dnsrift.sh');p.write_text(script)
subprocess.run(['python3','/private/tmp/cnc-aws-ssm.py',str(p)],check=True)
