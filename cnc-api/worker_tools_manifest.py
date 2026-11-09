"""Verify installed generic worker tools before advertising the worker as ready."""
import hashlib,json,os
from pathlib import Path,PurePosixPath

def verify(root):
    manifest=json.loads((root/'.cnc-manifest.json').read_text())
    if manifest.get('version')!=1 or not {'cnc-enum','dork2'}.issubset(manifest.get('required_executables',[])):
        raise RuntimeError('Worker tools package is incomplete')
    recorded=set()
    for item in manifest['files']:
        relative=PurePosixPath(item['path'])
        if relative.is_absolute() or '..' in relative.parts:
            raise RuntimeError('Invalid worker tools manifest path')
        path=root/relative
        if path.is_symlink() or not path.is_file() or hashlib.sha256(path.read_bytes()).hexdigest()!=item['sha256']:
            raise RuntimeError('Worker tools integrity check failed: '+str(relative))
        recorded.add(str(relative))
    if not {'dork2-src/index.js', 'dork2-src/stream-run.js'}.issubset(recorded):
        raise RuntimeError('Worker tools package is missing dork2 dependencies')
    for name in manifest['required_executables']:
        if name not in recorded or not os.access(root/name,os.X_OK):
            raise RuntimeError('Worker tool is missing or not executable: '+name)
    return len(recorded)

if __name__=='__main__':
    print('Verified generic worker tool files:',verify(Path(os.environ.get('CNC_TOOLS_ROOT','/opt/cnc/tools'))))
