#!/usr/bin/env python3
"""Stage only Linode integration files over a verified deployed source baseline."""
import base64,gzip,json,subprocess
from pathlib import Path
ROOT=Path(__file__).resolve().parents[2]
FILES=('cnc-api/linode.go','cnc-api/dork_capacity.go','cnc-api/dork_capacity_test.go','cnc-api/gdrive_force_sync.go','cnc-api/gdrive_force_sync_test.go','cnc-api/aws.go','cnc-api/worker_tools_package.go','cnc-api/worker_tools_package.py','cnc-api/worker_tools_manifest.py','cnc-api/linode_connection.go','cnc-api/linode_connection_test.go','cnc-api/linode_network.go','cnc-api/linode_network_test.go','cnc-api/linode_test.go','cnc-ui/components/dashboard/LinodeIntegration.tsx')
data={name:(ROOT/name).read_text() for name in FILES}
name='cnc-ui/components/dashboard/LogViewerModal.tsx'
data[name]=(ROOT/name).read_text()
for name in ('task_failure.go','task_failure_test.go','worker_tool_test.go'):
 data['cnc-api/'+name]=(ROOT/'cnc-api'/name).read_text()
worker=(ROOT/'cnc-api/worker.go').read_text()
start=worker.index('\texitCode := 0',worker.index('func (w *WorkerAgent) executeShellTask('))
end=worker.index('\n\tresult := &TaskResult{',start)
data['_worker_diagnostic_tail']=worker[start:end]
payload=base64.b64encode(gzip.compress(json.dumps(data).encode())).decode()
remote=(ROOT/'infra/aws/deploy-linode-integration-remote.py').read_text().replace('SOURCE_PAYLOAD',repr(payload))
script=Path('/private/tmp/cnc-deploy-linode-integration.sh');script.write_text("python3 - <<'ROLLOUT'\n"+remote+"\nROLLOUT\n")
subprocess.run(['python3','/private/tmp/cnc-aws-ssm.py',str(script)],check=True)
