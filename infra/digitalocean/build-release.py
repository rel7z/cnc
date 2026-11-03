#!/usr/bin/env python3
"""Build CNC cores and unrestricted generic tools, excluding processor/tools."""
import hashlib,json,os,shutil,subprocess,tarfile
from pathlib import Path
ROOT=Path(__file__).resolve().parents[2];OUT=ROOT/'.digitalocean/release'
env=dict(os.environ,GOCACHE='/private/tmp/cnc-do-go-cache',GOTOOLCHAIN='local',GOPROXY='off',CGO_ENABLED='0',GOOS='linux',GOARCH='amd64')
modules=[('bin/cnc-server','cnc-api','./cmd/server'),('bin/cnc-worker','cnc-api','./cmd/worker'),('bin/cnc-processor','processor','./cmd/cnc-processor')]+[('tools/'+name,'cnc-backend/'+module,'.') for name,module in {'cms-scan':'cms-scan','cnc-enum':'enum','subnet-finder':'subnet-finder','reverseip-domain':'reverseip/src1','reverseip-thc':'reverseip/src2','reverseip-dnsrift':'reverseip/src3','wp-plugins-detector':'enum/wp-plugins-detector'}.items()]
for target,module,entry in modules:
 path=OUT/target;path.parent.mkdir(parents=True,exist_ok=True)
 subprocess.run(['go','build','-ldflags=-s -w','-o',str(path),entry],cwd=ROOT/module,env=env,check=True)
 print('Built '+target,flush=True)
with tarfile.open(OUT/'ui-source.tar.gz','w:gz') as tar:
 root=ROOT/'cnc-ui'
 for directory in ['app','components','lib','public']:
  for p in sorted((root/directory).rglob('*')):
   if p.is_symlink():raise RuntimeError('UI symlink refused')
   if p.is_file():tar.add(p,arcname='ui/'+str(p.relative_to(root)),recursive=False)
 for name in ['package.json','package-lock.json','tsconfig.json','next-env.d.ts','next.config.ts','postcss.config.mjs','proxy.ts']:
  if (root/name).exists():tar.add(root/name,arcname='ui/'+name,recursive=False)
source=ROOT/'cnc-backend/google-parser/dork2';target=OUT/'tools/dork2-src';target.mkdir(parents=True,exist_ok=True)
for name in ['index.js','stream-run.js','package.json','package-lock.json']:shutil.copy2(source/name,target/name)
for name in ['worker_tools_package.py','worker_tools_manifest.py']:shutil.copy2(ROOT/'cnc-api'/name,OUT/name)
with tarfile.open(OUT/'artifacts.tar.gz','w:gz') as tar:
 for folder in ['bin','tools']:
  for p in sorted((OUT/folder).rglob('*')):
   if p.is_symlink():raise RuntimeError('Artifact symlink refused')
   if p.is_file():tar.add(p,arcname=str(p.relative_to(OUT)),recursive=False)
 for name in ['ui-source.tar.gz','worker_tools_package.py','worker_tools_manifest.py']:tar.add(OUT/name,arcname=name,recursive=False)
files=['artifacts.tar.gz','ui-source.tar.gz']+[target for target,_,_ in modules]
manifest={'release':'cnc-digitalocean-20261101','private_processor_tools_included':False,'sha256':{name:hashlib.sha256((OUT/name).read_bytes()).hexdigest() for name in files}}
(OUT/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n');print('Release artifacts prepared.',flush=True)
