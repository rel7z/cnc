#!/usr/bin/env python3
"""Deploy clean enum exports and exactly one part per worker to existing AWS nodes."""
import hashlib
import argparse
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile

from provision import aws, ROOT, STATE_FILE, PROFILE, REGION

spec = importlib.util.spec_from_file_location('enum_release', ROOT / 'infra/aws/deploy-enum-performance.py')
shared = importlib.util.module_from_spec(spec)
spec.loader.exec_module(shared)
invoke = shared.invoke

SPLIT_TEST = r'''package cnc
import("bufio";"crypto/sha256";"encoding/hex";"encoding/json";"io";"os";"testing")
func TestReleaseThreeWorkerInput(t *testing.T){
 input:=os.Getenv("CNC_RELEASE_VERIFY_INPUT")
 parts,err:=splitEnumInput(input,t.TempDir(),3);if err!=nil||len(parts)!=3 {t.Fatalf("parts=%d error=%v",len(parts),err)}
 original,err:=os.Open(input);if err!=nil {t.Fatal(err)};defer original.Close()
 want:=sha256.New();scan:=bufio.NewScanner(original);scan.Buffer(make([]byte,65536),1048576)
 total:=0;for scan.Scan(){if len(scan.Bytes())>0{want.Write(scan.Bytes());want.Write([]byte{'\n'});total++}};if scan.Err()!=nil {t.Fatal(scan.Err())}
 got:=sha256.New();counts:=[]int{};sizes:=[]int64{}
 for _,p:=range parts{f,e:=os.Open(p);if e!=nil {t.Fatal(e)};info,_:=f.Stat();sizes=append(sizes,info.Size());s:=bufio.NewScanner(io.TeeReader(f,got));s.Buffer(make([]byte,65536),1048576);n:=0;for s.Scan(){n++};if s.Err()!=nil{t.Fatal(s.Err())};f.Close();counts=append(counts,n)}
 if hex.EncodeToString(want.Sum(nil))!=hex.EncodeToString(got.Sum(nil)){t.Fatal("input conservation failed")}
 min,max:=counts[0],counts[0];for _,n:=range counts{if n<min{min=n};if n>max{max=n}};if max-min>1{t.Fatal("unbalanced parts")}
 report,_:=json.Marshal(map[string]any{"parts":len(parts),"lines":counts,"bytes":sizes,"total_lines":total,"input_preserved":true})
 if e:=os.WriteFile(os.Getenv("CNC_RELEASE_VERIFY_REPORT"),report,0600);e!=nil{t.Fatal(e)}
}
'''

BUILD = r'''
import hashlib,io,json,os,shutil,subprocess,tarfile,urllib.request
from pathlib import Path
root=Path('/opt/cnc');source=root/'source';release=root/'enum-clean-releases'/REVISION
release.mkdir(parents=True,exist_ok=False);release.chmod(0o700)
rollback=release/'rollback';rollback.mkdir(mode=0o700)
opener=urllib.request.build_opener(urllib.request.ProxyHandler({}))
archive=release/'source.tar.gz'
with opener.open(DOWNLOAD,timeout=90) as response:archive.write_bytes(response.read())
archive.chmod(0o600)
if hashlib.sha256(archive.read_bytes()).hexdigest()!=SHA:raise RuntimeError('Release digest mismatch')
approved=release/'approved';approved.mkdir(mode=0o700)
with tarfile.open(archive) as package:
 members=package.getmembers()
 if sorted(m.name for m in members)!=sorted(FILES) or not all(m.isfile() for m in members):raise RuntimeError('Unexpected release members')
 package.extractall(approved,filter='data')
config=json.loads((root/'config.json').read_text());base='http://'+config['http_addr']
def api(path):return json.load(opener.open(base+path,timeout=30))
jobs=api('/api/jobs');jobs=jobs.get('jobs',[]) if isinstance(jobs,dict) else jobs
active=[j for j in jobs if j['status'] in ('running','pending')]
if IDLE_ONLY:
 if active:raise RuntimeError('Idle-only deployment refuses to touch an active job')
 old=api('/api/jobs/enum_d17b70696b1d829c284ac092')
 if old['status'] not in ('cancelled','completed','completed_with_errors','failed'):raise RuntimeError('Reference job is not terminal')
else:
 if len(active)!=1 or active[0].get('enum') is None:raise RuntimeError('Expected one active enum scan; inspect before replacing')
 old=active[0]
old_id=old['id'];settings=old['enum']
if sum(w['status'] in ('online','busy') for w in api('/api/workers'))!=3:raise RuntimeError('Expected three online workers')
state_dir=Path(config['data_dir'])/'enum-jobs'/old_id
state=json.loads((state_dir/'state.json').read_text());artifact=json.loads((state_dir/state['artifact_filename']).read_text())
request={'tool_id':'enum','input_file':old['input_file'],'mode':'spread','timeout_seconds':old['timeout_seconds'],
 'version_rules':artifact['rules'],'options':{'threads':settings['threads'],'auto_concurrency':settings['automatic'],'timeout':settings['request_timeout_ms']//1000}}
for name in settings.get('wordlists',[]):
 if name not in ('wp_plugins.txt','joomla_exts.txt'):raise RuntimeError('Invalid wordlist name')
 request[name.removesuffix('.txt')]=(state_dir/name).read_text()
for name,value in [('restart-request.json',json.dumps(request)),('old-job.txt',old_id),('actor.txt',settings['actor'])]:
 (release/name).write_text(value);(release/name).chmod(0o600)
selected=[n for n in FILES if n.startswith('cnc-backend/') or n in ('cnc-api/enum.go','cnc-api/enum_state.go','cnc-api/enum_split_test.go','cnc-api/enum_integration_test.go','cnc-api/enum_publication_test.go','cnc-api/gdrive_enum_test.go','cnc-api/enum_text.go','cnc-api/enum_text_test.go')]
changed=selected+['cnc-api/server.go','cnc-api/gdrive.go'];absent=[]
for name in changed:
 path=source/name;backup=rollback/'source'/name
 if path.exists():
  if path.is_symlink() or not path.is_file():raise RuntimeError('Unsafe source destination')
  backup.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(path,backup)
 else:absent.append(name)
(rollback/'absent.json').write_text(json.dumps(absent))
shutil.copy2(root/'cnc-server',rollback/'cnc-server')
shutil.copy2(root/'tools/cnc-enum',rollback/'cnc-enum')
shutil.copy2(root/'worker-tools.tar.gz',rollback/'worker-tools.tar.gz')
env=dict(os.environ,GOPATH=str(root/'go-work'),GOMODCACHE=str(root/'go-work/pkg/mod'),GOCACHE=str(root/'go-cache'),GOMAXPROCS='2')
go=str(root/'go/bin/go');temporary_test=source/'cnc-api/release_enum_split_test.go'
try:
 for name in selected:
  destination=source/name;destination.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(approved/name,destination)
 patches=[('cnc-api/server.go','splitEnumInput(job.InputFile, chunkDir)','splitEnumInput(job.InputFile, chunkDir, job.Workers)'),
 ('cnc-api/gdrive.go','line := strings.TrimRight(scanner.Text(), "\\r")','line := cleanEnumTextLine(strings.TrimRight(scanner.Text(), "\\r"))')]
 for name,before,after in patches:
  path=source/name;text=path.read_text()
  if text.count(before)==1:path.write_text(text.replace(before,after,1))
  elif text.count(after)!=1:raise RuntimeError('Unexpected source for targeted patch: '+name)
 gdrive=source/'cnc-api/gdrive.go';text=gdrive.read_text()
 before='for _, entry := range entries {\n'
 start=text.index('func (u *GDriveUploader) scanAndUpload()')
 position=text.index(before,start)+len(before)
 if not text[position:].startswith('\t\tif isImmutableEnumResult(entry.Name())'):text=text[:position]+'\t\tif isImmutableEnumResult(entry.Name()) { continue }\n'+text[position:]
 before='for _, name := range names {\n'
 start=text.index('func (u *GDriveUploader) syncDeletions()')
 position=text.index(before,start)+len(before)
 if not text[position:].startswith('\t\tif isImmutableEnumResult(name)'):text=text[:position]+'\t\tif isImmutableEnumResult(name) { continue }\n'+text[position:]
 gdrive.write_text(text)
 if temporary_test.exists():raise RuntimeError('Temporary verification filename already exists')
 temporary_test.write_text(SPLIT_TEST)
 env.update(CNC_RELEASE_VERIFY_INPUT=old['input_file'],CNC_RELEASE_VERIFY_REPORT=str(release/'split-verification.json'))
 with (release/'build.log').open('w') as log:
  subprocess.run([go,'test','./...'],cwd=source/'cnc-backend/enum',env=env,stdout=log,stderr=subprocess.STDOUT,check=True,timeout=180)
  subprocess.run([go,'test','.','-run','TestEnum|TestGDrive|TestDrive|TestReleaseThreeWorkerInput','-count=1'],cwd=source/'cnc-api',env=env,stdout=log,stderr=subprocess.STDOUT,check=True,timeout=240)
  subprocess.run([go,'build','-p','2','-trimpath','-ldflags=-s -w','-o',str(release/'cnc-server'),'./cmd/server'],cwd=source/'cnc-api',env=env,stdout=log,stderr=subprocess.STDOUT,check=True,timeout=240)
  subprocess.run([go,'build','-trimpath','-ldflags=-s -w','-o',str(release/'cnc-enum'),'.'],cwd=source/'cnc-backend/enum',env=env,stdout=log,stderr=subprocess.STDOUT,check=True,timeout=180)
 (release/'build.log').chmod(0o600)
 fixture=subprocess.run(['python3',str(approved/'infra/aws/verify-enum-url-output.py'),str(release/'cnc-enum')],capture_output=True,text=True,check=True,timeout=40)
 fixture=json.loads(fixture.stdout)
 benchmark=subprocess.run(['python3',str(approved/'infra/aws/benchmark-enum.py'),str(rollback/'cnc-enum'),str(release/'cnc-enum')],capture_output=True,text=True,check=True,timeout=120)
 benchmark=json.loads(benchmark.stdout)
 if any(measured['speedup']<0.7 for measured in benchmark.values()):raise RuntimeError('Controlled throughput regression detected')
 (release/'benchmark.json').write_text(json.dumps(benchmark,indent=2)+'\n')
 hashes={name:hashlib.sha256((release/name).read_bytes()).hexdigest() for name in ('cnc-enum','cnc-server')}
 bundle=root/'worker-tools.tar.gz';stage=release/'worker-tools.tar.gz';found=0
 with tarfile.open(bundle) as old_tar,tarfile.open(stage,'w:gz') as new_tar:
  for member in old_tar:
   if member.name.lstrip('./') in ('tools/cnc-enum','cnc-enum'):
    if not member.isfile():raise RuntimeError('Unsafe archive member')
    content=(release/'cnc-enum').read_bytes();member.name='tools/cnc-enum';member.size=len(content);member.mode=0o700;new_tar.addfile(member,io.BytesIO(content));found+=1
   else:new_tar.addfile(member,old_tar.extractfile(member) if member.isfile() else None)
 if found!=1:raise RuntimeError('Missing or duplicate enum archive entry')
 with tarfile.open(stage) as package:
  member=next(m for m in package if m.name.lstrip('./')=='tools/cnc-enum')
  if hashlib.sha256(package.extractfile(member).read()).hexdigest()!=hashes['cnc-enum']:raise RuntimeError('Worker archive hash mismatch')
 # Candidate is verified before replacing the binary used by later launches.
 target=root/'tools/cnc-enum';new=target.with_name('cnc-enum.new');shutil.copy2(release/'cnc-enum',new);new.chmod(0o700);new.replace(target)
 stage.replace(bundle)
 print(json.dumps({'revision':REVISION,'old_job':old_id,'hashes':hashes,'fixture':fixture,'benchmark':benchmark,'real_input_split':json.loads((release/'split-verification.json').read_text()),'tests_passed':True,'rollback':str(rollback)}))
except Exception:
 for name in changed:
  backup=rollback/'source'/name;destination=source/name
  if backup.exists():shutil.copy2(backup,destination)
  elif name in absent:destination.unlink(missing_ok=True)
 raise
finally:
 temporary_test.unlink(missing_ok=True)
'''

WORKER = r'''
import hashlib,json,os,shutil,subprocess,tarfile,urllib.request
from pathlib import Path
root=Path('/opt/cnc');release=root/'enum-clean-releases'/REVISION
release.mkdir(parents=True,exist_ok=False);release.chmod(0o700)
rollback=release/'rollback';rollback.mkdir(mode=0o700)
opener=urllib.request.build_opener(urllib.request.ProxyHandler({}))
archive=release/'worker-tools.tar.gz'
with opener.open(BASE+'/download/cnc-worker-tools',timeout=120) as response:
 with archive.open('wb') as output:shutil.copyfileobj(response,output)
archive.chmod(0o600)
with tarfile.open(archive) as package:
 member=next(m for m in package if m.name.lstrip('./')=='tools/cnc-enum')
 candidate=package.extractfile(member).read()
if hashlib.sha256(candidate).hexdigest()!=EXPECTED_SHA:raise RuntimeError('Worker binary hash mismatch')
with opener.open(VERIFY_DOWNLOAD,timeout=30) as response:verifier=response.read()
if hashlib.sha256(verifier).hexdigest()!=VERIFY_SHA:raise RuntimeError('Verifier hash mismatch')
check=release/'verify.py';check.write_bytes(verifier);check.chmod(0o600)
binary=release/'cnc-enum';binary.write_bytes(candidate);binary.chmod(0o700)
fixture=subprocess.run(['python3',str(check),str(binary)],capture_output=True,text=True,check=True,timeout=45)
target=root/'tools/cnc-enum';shutil.copy2(target,rollback/'cnc-enum')
stage=target.with_name('cnc-enum.new');shutil.copy2(binary,stage);stage.chmod(0o700);stage.replace(target)
print(json.dumps({'sha256':EXPECTED_SHA,'fixture':json.loads(fixture.stdout),'agent_restarted':False,'rollback':str(rollback)}))
'''

ACTIVATE = r'''
import hashlib,hmac,json,os,re,shutil,subprocess,time,urllib.request
from pathlib import Path
root=Path('/opt/cnc');release=root/'enum-clean-releases'/REVISION;rollback=release/'rollback'
config=json.loads((root/'config.json').read_text());base='http://'+config['http_addr']
opener=urllib.request.build_opener(urllib.request.ProxyHandler({}))
def api(path,data=None):
 request=urllib.request.Request(base+path,data=data,headers={'Content-Type':'application/json'})
 return json.load(opener.open(request,timeout=30))
old_id=(release/'old-job.txt').read_text()
if IDLE_ONLY:
 jobs=api('/api/jobs');jobs=jobs.get('jobs',[]) if isinstance(jobs,dict) else jobs
 if any(j['status'] in ('running','pending') for j in jobs):raise RuntimeError('A job became active; idle-only activation refused')
else:
 if api('/api/jobs/'+old_id)['status']!='running':raise RuntimeError('Scan state changed; inspect before activation')
 api('/api/jobs/'+old_id+'/cancel',b'{}')
 for attempt in range(40):
  workers=api('/api/workers')
  active=sum((w.get('enum_requests') or {}).get(old_id,{}).get('active',0) for w in workers)
  if active==0:break
  time.sleep(1)
 else:raise RuntimeError('Old scan requests did not stop')
subprocess.run(['systemctl','stop','cnc-server'],check=True)
normalized=[]
archived_jsonl=0
try:
 # Only strip the known old enum suffixes; unrelated results remain unchanged.
 watch=Path(os.path.expanduser((config.get('gdrive') or {}).get('watch_dir','/root/merged')))
 for path in watch.glob('enum-versions-*.jsonl'):
  if path.is_symlink() or not path.is_file() or not re.fullmatch(r'enum-versions-[a-z0-9][a-z0-9_-]{0,63}-[a-z0-9][a-z0-9_-]{0,63}\.jsonl',path.name):continue
  destination=Path(config['data_dir'])/'enum-legacy-exports'/path.name
  destination.parent.mkdir(parents=True,exist_ok=True);destination.parent.chmod(0o700)
  if destination.exists():
   if hashlib.sha256(destination.read_bytes()).digest()!=hashlib.sha256(path.read_bytes()).digest():raise RuntimeError('Legacy artifact archive collision')
  else:shutil.copy2(path,destination);destination.chmod(0o600)
  path.unlink();archived_jsonl+=1
 metadata=re.compile(r'^(https?://\S+)  component=(?:wordpress:plugin:|joomla:)\S+\s+version=\S+\s+confidence=\S+$')
 plugin=re.compile(r'^(https?://\S+/wp-content/plugins/\S+)  plugin=\S+\s+version=\S+$')
 for path in watch.glob('*.txt'):
  if path.is_symlink() or not path.is_file():continue
  stage=path.with_name(path.name+'.enum-clean.tmp');changed=0
  try:
   with path.open(encoding='utf-8') as source,stage.open('w',encoding='utf-8') as output:
    for line in source:
     raw=line.rstrip('\r\n');match=metadata.fullmatch(raw) or plugin.fullmatch(raw)
     if match:
      raw=match.group(1);changed+=1
     output.write(raw+'\n')
   if changed:
    backup=rollback/'merged'/path.name;backup.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(path,backup);backup.chmod(0o600)
    stage.chmod(path.stat().st_mode & 0o777);stage.replace(path);normalized.append({'file':path.name,'lines_cleaned':changed})
  except UnicodeDecodeError:pass
  finally:stage.unlink(missing_ok=True)
 target=root/'cnc-server';stage=target.with_name('cnc-server.new');shutil.copy2(release/'cnc-server',stage);stage.chmod(0o700);stage.replace(target)
 subprocess.run(['systemctl','start','cnc-server'],check=True)
 for attempt in range(90):
  try:
   workers=api('/api/workers')
   if sum(w['status'] in ('online','busy') for w in workers)==3:break
  except Exception:pass
  time.sleep(1)
 else:raise RuntimeError('Workers did not reconnect')
except Exception:
 stage=root/'cnc-server.rollback';shutil.copy2(rollback/'cnc-server',stage);stage.replace(root/'cnc-server')
 subprocess.run(['systemctl','start','cnc-server'],check=False)
 raise
if IDLE_ONLY:
 drive=api('/api/config/gdrive/auth-status')
 if drive.get('authorized'):api('/api/config/gdrive/sync',b'{}')
 report={'idle_only':True,'jobs_cancelled':False,'jobs_restarted':False,'jobs_launched':False,'reference_job':old_id,
  'reference_job_status':api('/api/jobs/'+old_id)['status'],'future_parts_for_three_workers':3,
  'normalized_files':normalized,'workers_online':3,'drive_authorized':drive.get('authorized'),
  'legacy_jsonl_archived_internally':archived_jsonl,'jsonl_export_disabled':True,
  'controller_restarted':True,'worker_agents_restarted':False}
 (release/'verification.json').write_text(json.dumps(report,indent=2)+'\n')
 print(json.dumps(report));raise SystemExit(0)
body=(release/'restart-request.json').read_bytes();actor=(release/'actor.txt').read_text()
secret=dict(line.split('=',1) for line in (root/'dashboard-audit.env').read_text().splitlines() if '=' in line)['CNC_DASHBOARD_AUDIT_SECRET']
stamp=str(int(time.time()*1000));signature=hmac.new(secret.encode(),(actor+'\n'+stamp+'\nPOST\n/api/tools/launch\n'+hashlib.sha256(body).hexdigest()).encode(),hashlib.sha256).hexdigest()
request=urllib.request.Request(base+'/api/tools/launch',body,{'Content-Type':'application/json','X-CNC-Actor':actor,'X-CNC-Audit-Time':stamp,'X-CNC-Audit-Signature':signature})
with opener.open(request,timeout=600) as response:created=json.load(response)
new_id=created['job_id'];(release/'replacement-job.txt').write_text(new_id+'\n')
for attempt in range(180):
 tasks=api('/api/jobs/'+new_id+'/tasks')
 if len(tasks)==3 and len({t.get('assigned_to') for t in tasks if t.get('assigned_to')})==3:break
 if len(tasks)>3:raise RuntimeError('Unexpected extra production parts')
 time.sleep(1)
else:raise RuntimeError('Three parts were not dispatched to distinct workers')
counts=[];sizes=[]
for task in tasks:
 path=Path(task['payload']['chunk_path']);sizes.append(path.stat().st_size)
 with path.open('rb') as file:counts.append(sum(1 for _ in file))
if max(counts)-min(counts)>1:raise RuntimeError('Production parts are not balanced')
drive=api('/api/config/gdrive/auth-status')
if drive.get('authorized'):api('/api/config/gdrive/sync',b'{}')
report={'old_job':old_id,'old_job_status':api('/api/jobs/'+old_id)['status'],'new_job':new_id,'parts':len(tasks),'lines':counts,'bytes':sizes,
 'assigned_workers':[t['assigned_to'] for t in tasks],'same_input_and_settings':True,'existing_results_retained':True,
 'normalized_files':normalized,'workers_online':3,'drive_authorized':drive.get('authorized'),'controller_restarted':True,'worker_agents_restarted':False}
(release/'verification.json').write_text(json.dumps(report,indent=2)+'\n')
print(json.dumps(report))
'''


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--idle-only',action='store_true',help='Require no active jobs; never cancel, launch, or restart a scan')
    parser.add_argument('--scanner-only',action='store_true',help='Update scanner binaries/archive without restarting controller or worker services')
    args=parser.parse_args()
    if args.scanner_only and not args.idle_only:parser.error('--scanner-only requires --idle-only')
    state = json.loads(STATE_FILE.read_text())
    files = []
    for folder in ('cnc-backend/enum', 'cnc-backend/enum/detection', 'cnc-backend/enum/version'):
        files += [str(p.relative_to(ROOT)) for p in sorted((ROOT / folder).iterdir()) if p.is_file() and (p.suffix == '.go' or p.name in ('go.mod', 'go.sum'))]
    files += ['cnc-api/' + n for n in ('server.go', 'gdrive.go', 'enum.go', 'enum_state.go', 'enum_split_test.go', 'enum_integration_test.go', 'enum_publication_test.go', 'gdrive_enum_test.go', 'enum_text.go', 'enum_text_test.go')]
    files += ['infra/aws/deploy-enum-three-workers.py', 'infra/aws/verify-enum-url-output.py', 'infra/aws/benchmark-enum.py', 'cnc-backend/enum/OPTIMIZATION.md']
    report = {'workers': {}}
    evidence = ROOT / 'docs/verification/enum-three-workers.json'
    def save():evidence.write_text(json.dumps(report, indent=2) + '\n')
    with tempfile.TemporaryDirectory(prefix='enum-three-release-') as folder:
        temporary = Path(folder)
        env = dict(os.environ, GIT_INDEX_FILE=str(temporary / 'index'))
        def git(*args):return subprocess.check_output(['git', *args], cwd=ROOT, env=env, text=True).strip()
        git('read-tree', '--empty');git('add', '--', *files)
        revision = git('commit-tree', git('write-tree'), '-m', 'Deploy URL-only enum exports and one balanced part per worker')
        git('update-ref', 'refs/heads/codex/enum-three-workers', revision)
        report['revision'] = revision
        archive = temporary / 'source.tar.gz'
        with tarfile.open(archive, 'w:gz') as package:
            for name in files:
                if (ROOT / name).is_symlink():raise RuntimeError('Symlink release input')
                package.add(ROOT / name, arcname=name, recursive=False)
        key = 'releases/enum-three-workers-' + revision + '.tar.gz'
        aws('s3api', 'put-object', '--bucket', state['release_bucket'], '--key', key, '--body', str(archive), '--server-side-encryption', 'AES256')
        def presign(object_key):return subprocess.check_output(['aws','s3','presign','s3://'+state['release_bucket']+'/'+object_key,'--expires-in','3600','--profile',PROFILE,'--region',REGION],text=True).strip()
        values = dict(REVISION=revision, FILES=files, DOWNLOAD=presign(key), SHA=hashlib.sha256(archive.read_bytes()).hexdigest(), SPLIT_TEST=SPLIT_TEST, IDLE_ONLY=args.idle_only)
        def code(template, **extra):return '\n'.join(k+'='+repr(v) for k,v in dict(values,**extra).items())+'\n'+template
        controller = state['nodes']['cnc-aws-server']
        report['build'] = invoke(controller['id'],code(BUILD),'Build clean URL and three-part release');save()
        verifier = ROOT / 'infra/aws/verify-enum-url-output.py'
        verify_key = 'releases/enum-three-workers-' + revision + '-verify.py'
        aws('s3api','put-object','--bucket',state['release_bucket'],'--key',verify_key,'--body',str(verifier),'--server-side-encryption','AES256')
        for name,node in state['nodes'].items():
            if name=='cnc-aws-server':continue
            report['workers'][name] = invoke(node['id'],code(WORKER,BASE='http://'+controller['private_ip']+':8080',EXPECTED_SHA=report['build']['hashes']['cnc-enum'],VERIFY_DOWNLOAD=presign(verify_key),VERIFY_SHA=hashlib.sha256(verifier.read_bytes()).hexdigest()),'Verify and install clean enum '+name)
            save()
        if args.scanner_only:
            report['production']={'scanner_only':True,'services_restarted':False,'jobs_cancelled':False,'jobs_launched':False}
        else:
            report['production'] = invoke(controller['id'],code(ACTIVATE),'Activate three-worker production scan',timeout=1200)
        save()


if __name__ == '__main__':main()
