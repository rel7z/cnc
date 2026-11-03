"""Install the official Node LTS runtime with vendor checksum verification."""
import hashlib,tarfile,urllib.request
from pathlib import Path

def install_node():
 root=Path('/opt/cnc/node-runtime');root.mkdir(parents=True,exist_ok=True)
 current=sorted(root.glob('node-v22.*-linux-x64/bin/node'))
 if not current:
  op=urllib.request.build_opener(urllib.request.ProxyHandler({}));base='https://nodejs.org/dist/latest-v22.x/'
  with op.open(base+'SHASUMS256.txt',timeout=45) as response:sums=response.read().decode()
  digest,name=next(line.split() for line in sums.splitlines() if line.endswith('-linux-x64.tar.xz'))
  with op.open(base+name,timeout=120) as response:data=response.read()
  if hashlib.sha256(data).hexdigest()!=digest:raise RuntimeError('Node checksum mismatch')
  archive=root/'node.tar.xz';archive.write_bytes(data)
  with tarfile.open(archive) as tar:tar.extractall(root,filter='data')
  current=sorted(root.glob('node-v22.*-linux-x64/bin/node'))
 if not current:raise RuntimeError('Node runtime missing')
 node=current[-1].parent.parent
 Path('/opt/cnc/node-path').write_text(str(node)+'\n')
 return node
