"""Install the pinned official DuckDB wheel and signed HTTPS extension, without system pip."""
import argparse
import hashlib
import importlib
import json
import os
import platform
import shutil
import sys
import tempfile
import time
import urllib.request
import zipfile
from pathlib import Path,PurePosixPath

VERSION='2.0.1'

def install(root):
    root=Path(root);root.mkdir(parents=True,exist_ok=True,mode=0o700)
    lib=root/'index-lib';extensions=root/'index-extensions';extensions.mkdir(exist_ok=True,mode=0o700)
    if platform.system()!='Linux' or platform.machine() not in ('x86_64','amd64'):raise RuntimeError('Expected the existing Linux amd64 controller')
    if not (lib/'duckdb').exists():
        with urllib.request.urlopen(f'https://pypi.org/pypi/duckdb/{VERSION}/json',timeout=30) as response:meta=json.load(response)
        abi='cp'+str(sys.version_info.major)+str(sys.version_info.minor)
        choices=[item for item in meta['urls'] if (abi+'-'+abi in item['filename'] or abi+'-abi3' in item['filename']) and 'manylinux' in item['filename'] and 'x86_64' in item['filename']]
        if not choices:raise RuntimeError('No compatible official DuckDB wheel')
        wheel=choices[0]
        if not wheel['url'].startswith('https://files.pythonhosted.org/'):raise RuntimeError('Unexpected wheel origin')
        with urllib.request.urlopen(wheel['url'],timeout=60) as response:data=response.read((150<<20)+1)
        if len(data)>150<<20 or hashlib.sha256(data).hexdigest()!=wheel['digests']['sha256']:raise RuntimeError('Wheel checksum mismatch')
        with tempfile.TemporaryDirectory(prefix='index-install-',dir=root) as temp:
            temp=Path(temp);archive=temp/'duckdb.whl';archive.write_bytes(data);unpacked=temp/'lib';unpacked.mkdir()
            with zipfile.ZipFile(archive) as bundle:
                for entry in bundle.infolist():
                    path=PurePosixPath(entry.filename)
                    if path.is_absolute() or '..' in path.parts or entry.external_attr>>16 & 0o170000==0o120000:raise RuntimeError('Unsafe wheel member')
                bundle.extractall(unpacked)
            if lib.exists():lib.rename(root/('index-lib.before-'+str(time.time_ns())))
            shutil.move(unpacked,lib)
    sys.path.insert(0,str(lib));duckdb=importlib.import_module('duckdb')
    scratch=root/'index-scratch';scratch.mkdir(exist_ok=True,mode=0o700)
    with duckdb.connect(config={'extension_directory':str(extensions),'temp_directory':str(scratch),'memory_limit':'256MB','threads':'2'}) as connection:
        connection.execute("INSTALL httpfs FROM 'https://extensions.duckdb.org'")
        connection.execute('LOAD httpfs')
    (root/'index-runtime.json').write_text(json.dumps({'duckdb':duckdb.__version__,'wheel_origin':'pypi.org','httpfs':'signed official extension'}))
    return {'duckdb':duckdb.__version__,'httpfs':'ready','system_python_packages_changed':False}

if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--root',type=Path,required=True)
    print(json.dumps(install(parser.parse_args().root)))
