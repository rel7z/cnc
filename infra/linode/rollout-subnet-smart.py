#!/usr/bin/env python3
"""Replace only the named unrestricted subnet-finder binary; never launch a scan."""
import concurrent.futures
import hashlib
import importlib.util
import json
from pathlib import Path
import shutil
import subprocess
import tarfile

ROOT=Path(__file__).resolve().parents[2]
spec=importlib.util.spec_from_file_location('common',ROOT/'infra/linode/rollout-memory-burst.py')
common=importlib.util.module_from_spec(spec);spec.loader.exec_module(common)
BINARY=Path('/tmp/cnc-subnet-finder-resource-smart')
HOSTS=['cnc-linode-server']+common.WORKERS


def main():
    digest=hashlib.sha256(BINARY.read_bytes()).hexdigest()
    release='/opt/cnc/queue-releases/subnet-smart-'+digest[:16]
    files=['main.go','pipeline.go','dedup.go','pipeline_test.go','dedup_test.go','go.mod','go.sum','README.md']
    archive=Path('/tmp/cnc-subnet-smart-source.tar.gz')
    with tarfile.open(archive,'w:gz') as tar:
        for name in files:
            path=ROOT/'cnc-backend/subnet-finder'/name
            tar.add(path,arcname='cnc-backend/subnet-finder/'+name,recursive=False)
        tar.add(ROOT/'infra/linode/rollout-subnet-smart.py',arcname='infra/linode/rollout-subnet-smart.py',recursive=False)
    common.ssh('cnc-linode-server','import pathlib\npathlib.Path('+repr(release)+').mkdir(parents=True,exist_ok=True)\n')
    common.upload('cnc-linode-server',archive,release+'/source.tar.gz')
    common.ssh('cnc-linode-server','import pathlib,tarfile\nr=pathlib.Path('+repr(release)+')\nwith tarfile.open(r/"source.tar.gz") as t:t.extractall(r/"source",filter="data")\n')
    def stage(host):common.upload(host,BINARY,'/tmp/cnc-subnet-finder-resource-smart')
    with concurrent.futures.ThreadPoolExecutor(max_workers=7) as pool:list(pool.map(stage,HOSTS))
    proof=[]
    for host in HOSTS:
        code='RELEASE='+repr(release)+'\nEXPECTED='+repr(digest)+'\n'+'''
import pathlib,shutil,hashlib,json
target=pathlib.Path('/opt/cnc/tools/subnet-finder')
assert target.is_file() and not target.is_symlink() and not target.parent.is_symlink(),'Expected named tool binary is missing or symlinked'
new=pathlib.Path('/tmp/cnc-subnet-finder-resource-smart');assert hashlib.sha256(new.read_bytes()).hexdigest()==EXPECTED
backup=pathlib.Path(RELEASE)/'rollback';backup.mkdir(parents=True,exist_ok=True)
old=backup/'subnet-finder'
if not old.exists():shutil.copy2(target,old)
temp=target.with_name('subnet-finder.resource-smart-new');shutil.copy2(new,temp);temp.chmod(0o755);temp.replace(target)
assert hashlib.sha256(target.read_bytes()).hexdigest()==EXPECTED
print(json.dumps({'installed':True,'sha256':EXPECTED,'rollback':str(old)}))
'''
        result=json.loads(common.ssh(host,code));result['host']=host;proof.append(result);print(json.dumps(result),flush=True)
    # Keep the repository's explicit Linux artifact in sync with the release.
    local=ROOT/'cnc-api/tools/subnet-finder-linux'
    assert not local.is_symlink() and not local.parent.is_symlink(),'Do not follow local tool symlinks'
    backup=Path('/tmp/cnc-subnet-smart-local-rollback-'+digest[:16]);backup.mkdir(exist_ok=True)
    if local.is_file() and not (backup/local.name).exists():shutil.copy2(local,backup/local.name)
    shutil.copy2(BINARY,local);local.chmod(0o755)
    Path('/tmp/cnc-subnet-smart-release.json').write_text(json.dumps({'release':release,'sha256':digest,'hosts':proof,'service_restarts':False,'private_tools_accessed':False},indent=2)+'\n')
    print(json.dumps({'release':release,'hosts_updated':len(proof),'service_restarts':False,'scan_started':False}),flush=True)


if __name__=='__main__':main()
