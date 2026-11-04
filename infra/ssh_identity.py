"""One Mac-local deployment identity shared by provider provisioners.

Private key bytes stay on the provisioning Mac. Legacy provider keys are kept.
"""
import contextlib
import fcntl
import hashlib
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile


def identity_directory():
    override = os.environ.get('CNC_SSH_IDENTITY_DIR')
    if override:
        return Path(override).expanduser()
    return Path.home() / ('Library/Application Support/CNC Manager/ssh' if sys.platform == 'darwin' else '.config/cnc-manager/ssh')


def public_key(path):
    result = subprocess.run(['ssh-keygen', '-y', '-P', '', '-f', str(path)], stdin=subprocess.DEVNULL, capture_output=True, text=True)
    if result.returncode:
        raise RuntimeError('Cannot load deployment SSH key; preserve the existing key and check permissions')
    return result.stdout.strip()


def shared_key(root, directory=None):
    root = Path(root)
    directory = (Path(directory) if directory else identity_directory()).expanduser().absolute()
    if directory.is_symlink():
        raise RuntimeError('SSH identity directory must not be a symlink')
    directory.mkdir(parents=True, mode=0o700, exist_ok=True); directory.chmod(0o700)
    directory = directory.resolve()
    path = directory / 'id_ed25519'
    fd = os.open(directory / 'identity.lock', os.O_CREAT | os.O_RDWR, 0o600)
    try:
        fcntl.flock(fd, fcntl.LOCK_EX)
        if path.is_symlink():
            raise RuntimeError('Shared private key must not be a symlink')
        if not path.exists():
            candidates = [root / provider / 'id_ed25519' for provider in ('.digitalocean', '.linode', '.aws-cnc', '.upcloud')]
            source = next((p for p in candidates if p.is_file() and not p.is_symlink()), None)
            if source:
                # Validate before the atomic local copy; never print the key.
                public_key(source)
                temp_fd, temp_name = tempfile.mkstemp(prefix='.identity-', dir=directory)
                try:
                    with os.fdopen(temp_fd, 'wb') as out, source.open('rb') as src:
                        shutil.copyfileobj(src, out); out.flush(); os.fsync(out.fileno())
                    os.replace(temp_name, path)
                finally:
                    with contextlib.suppress(FileNotFoundError): os.unlink(temp_name)
            else:
                if path.with_suffix('.pub').exists() or any(p.is_symlink() or p.with_suffix('.pub').exists() for p in candidates):
                    raise RuntimeError('Deployment private key is missing; restore its backup rather than generating a replacement')
                subprocess.run(['ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-f', str(path)], check=True, capture_output=True)
        path.chmod(0o600)
        pub = path.with_suffix('.pub')
        if pub.is_symlink():
            raise RuntimeError('Shared public key must not be a symlink')
        actual = public_key(path)
        if pub.exists() and pub.read_text().split()[:2] != actual.split()[:2]:
            raise RuntimeError('Shared keypair mismatch; refusing to replace it')
        if not pub.exists(): pub.write_text(actual + '\n')
        pub.chmod(0o600)
        return path
    finally:
        os.close(fd)


def deployment_key(root, state_dir):
    """Use the common key for new nodes; retain old keys/config compatibility."""
    key = shared_key(root)
    state_dir = Path(state_dir); state_dir.mkdir(parents=True, mode=0o700, exist_ok=True)
    legacy = state_dir / 'id_ed25519'
    for target, source in [(legacy, key), (legacy.with_suffix('.pub'), key.with_suffix('.pub'))]:
        if not target.exists() and not target.is_symlink(): target.symlink_to(source)
    return key


def identity_lines(key, legacy):
    paths = [Path(key)]
    if Path(legacy).is_file() and Path(legacy).resolve() != Path(key).resolve(): paths.append(Path(legacy))
    return ''.join(' IdentityFile "' + str(p).replace('"', '\\"') + '"\n' for p in paths)


def key_name(path):
    return 'cnc-mac-' + hashlib.sha256(' '.join(public_key(path).split()[:2]).encode()).hexdigest()[:16]
