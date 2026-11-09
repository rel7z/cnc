#!/usr/bin/env python3
"""Deploy the verified generic dork2 queue/streaming patch; no search is run."""
import base64,gzip,json,subprocess
from pathlib import Path
ROOT=Path(__file__).resolve().parents[2];stage=Path('/private/tmp/cnc-dork2-fix')
payload=json.loads((stage/'patch.json').read_text());payload.update(index=(stage/'index.js').read_text(),module=(stage/'cnc-provider-queue.mjs').read_text(),builder=(ROOT/'cnc-api/worker_tools_package.py').read_text())
encoded=base64.b64encode(gzip.compress(json.dumps(payload).encode())).decode()
script=r'''python3 - <<'PATCH'
import base64,gzip,json,pathlib,hashlib,os,subprocess,shutil
P=pathlib.Path;p=json.loads(gzip.decompress(base64.b64decode('PAYLOAD')))
root=P('/opt/cnc/tools/dork2-src');index=root/'index.js';module=root/'cnc-provider-queue.mjs'
assert hashlib.sha256(index.read_bytes()).hexdigest() in (p['original_sha256'],p['patched_sha256']),'Generic tool changed; refusing overwrite'
release=P('/opt/cnc/dork-fix-releases')/p['patched_sha256'][:16];release.mkdir(mode=0o700,parents=True,exist_ok=True)
if not (release/'index.original.js').exists():shutil.copy2(index,release/'index.original.js')
(release/'package.json').write_text('{"type":"module"}')
for name,key in (('index.js','index'),('cnc-provider-queue.mjs','module')):(release/name).write_text(p[key])
node=P('/opt/cnc/node-path').read_text().strip()+'/bin/node'
for name in ('index.js','cnc-provider-queue.mjs'):subprocess.run([node,'--check',str(release/name)],check=True,capture_output=True)
for name in ('cnc-provider-queue.mjs','index.js'):
 new=root/(name+'.new');shutil.copy2(release/name,new);new.chmod(0o644);os.replace(new,root/name)
check=subprocess.run(['/opt/cnc/tools/dork2','--help'],capture_output=True,text=True,timeout=15);assert check.returncode==0 and 'Usage: dork2' in check.stderr
exec(compile(p['builder'],'worker-package-build','exec'),{'__name__':'__main__'})
print(json.dumps({'generic_runtime_patched':True,'sha256':p['patched_sha256'],'offline_help_passed':True}))
PATCH
'''.replace('PAYLOAD',encoded)
p=Path('/private/tmp/cnc-deploy-dork2-runtime.sh');p.write_text(script)
subprocess.run(['python3','/private/tmp/cnc-aws-ssm.py',str(p)],check=True)
