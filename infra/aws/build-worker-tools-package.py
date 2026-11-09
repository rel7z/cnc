"""Controller-side canonical generic worker package; never traverses processor/tools."""
import hashlib,io,json,os,pathlib,tarfile,time
P=pathlib.Path
root=P('/opt/cnc/tools')
roots=('cnc-enum','dork2','dork2-src','subnet-finder','reverseip-thc','reverseip-domain','cms-scan','wp-plugins-detector')
files=[]
for name in roots:
 p=root/name
 if p.is_symlink() or not p.exists():raise RuntimeError('Required generic tool is absent or a symlink: '+name)
 if p.is_file():files.append(p);continue
 for directory,children,names in os.walk(p,followlinks=False):
  children[:]=[n for n in children if n not in ('.git','.cache','__pycache__') and not (P(directory)/n).is_symlink()]
  for n in names:
   f=P(directory)/n
   if f.is_symlink() or n.startswith('.env') or n in ('credentials.json','token.json'):continue
   if f.is_file():files.append(f)
manifest={'version':1,'required_executables':[n for n in roots if (root/n).is_file()],'files':[]}
archive=P('/opt/cnc/worker-tools.tar.gz.new')
with tarfile.open(archive,'w:gz') as package:
 for f in sorted(files):
  relative=f.relative_to(root).as_posix();data=f.read_bytes();mode=f.stat().st_mode&0o777
  manifest['files'].append({'path':relative,'sha256':hashlib.sha256(data).hexdigest(),'mode':mode})
  package.add(f,arcname='tools/'+relative,recursive=False)
 data=json.dumps(manifest,sort_keys=True).encode();entry=tarfile.TarInfo('tools/.cnc-manifest.json');entry.size=len(data);entry.mode=0o600;package.addfile(entry,io.BytesIO(data))
archive.chmod(0o600)
target=P('/opt/cnc/worker-tools.tar.gz')
if target.exists():os.link(target,P('/opt/cnc/worker-tools.tar.gz.rollback-'+str(time.time_ns())))
os.replace(archive,target)
print(json.dumps({'package_bytes':target.stat().st_size,'sha256':hashlib.sha256(target.read_bytes()).hexdigest(),'files':len(files),'required_executables':manifest['required_executables'],'private_processor_tools_included':False}))
