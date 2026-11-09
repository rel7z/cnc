#!/bin/bash
set -eu
SOURCE_DIR=/opt/cnc/source
mkdir -p /opt/cnc/tools
python3 - <<'PY'
import subprocess
from pathlib import Path
source=Path('/opt/cnc/source')
modules={'cms-scan':'cms-scan','cnc-enum':'enum','subnet-finder':'subnet-finder','reverseip-domain':'reverseip/src1','reverseip-thc':'reverseip/src2','wp-plugins-detector':'enum/wp-plugins-detector'}
for name,module in modules.items():
 subprocess.run(['/opt/cnc/go/bin/go','build','-ldflags=-s -w','-o','/opt/cnc/tools/'+name,'.'],cwd=source/'cnc-backend'/module,check=True)
PY
python3 - <<'PY'
import urllib.request,hashlib,tarfile
from pathlib import Path
root=Path('/opt/cnc/node-runtime')
if not list(root.glob('node-*-linux-x64/bin/node')):
 base='https://nodejs.org/dist/latest-v22.x/'
 with urllib.request.urlopen(base+'SHASUMS256.txt',timeout=60) as response: sums=response.read().decode()
 checksum,filename=next(line.split() for line in sums.splitlines() if line.endswith('-linux-x64.tar.xz'))
 with urllib.request.urlopen(base+filename,timeout=120) as response: data=response.read()
 if hashlib.sha256(data).hexdigest()!=checksum: raise RuntimeError('Node checksum mismatch')
 archive=Path('/opt/cnc/node.tar.xz');archive.write_bytes(data)
 with tarfile.open(archive) as tar: tar.extractall(root,filter='data')
node=next(root.glob('node-*-linux-x64'))
Path('/opt/cnc/node-path').write_text(str(node))
PY
export PATH="$(cat /opt/cnc/node-path)/bin:$PATH"
mkdir -p /opt/cnc/tools/dork2-src
cp "$SOURCE_DIR/cnc-backend/google-parser/dork2/"{index.js,stream-run.js,package.json,package-lock.json} /opt/cnc/tools/dork2-src/
cd /opt/cnc/tools/dork2-src
npm ci --no-audit --no-fund
cat > /opt/cnc/tools/dork2 <<'WRAPPER'
#!/bin/sh
exec "$(cat /opt/cnc/node-path)/bin/node" /opt/cnc/tools/dork2-src/index.js "$@"
WRAPPER
chmod 755 /opt/cnc/tools/dork2
cd /opt/cnc
tar -czf worker-tools.tar.gz tools node-runtime node-path
