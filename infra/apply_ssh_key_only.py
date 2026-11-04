#!/usr/bin/env python3
"""Remote prepare/apply/confirm helper; never reads a private SSH key.

The caller prepends POLICY = policy_text(...). Existing sessions are retained.
"""
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import time

phase, run, fingerprint = sys.argv[1:4]
if not run.startswith('mac-key-') or not run.replace('-', '').isalnum():
    raise RuntimeError('Invalid rollout ID')
root = Path('/var/lib/cnc-ssh-policy') / run
target = Path('/etc/ssh/sshd_config.d/00-00-cnc-key-only.conf')
main = Path('/etc/ssh/sshd_config')
unit = 'cnc-ssh-rollback-' + run


def invoke(*args):
    return subprocess.check_output(args, text=True, stderr=subprocess.PIPE).strip()


def assert_key():
    path = Path('/root/.ssh/authorized_keys')
    if path.is_symlink():
        raise RuntimeError('Authorized keys must not be a symlink')
    lines = invoke('ssh-keygen', '-lf', str(path)).splitlines()
    if len(lines) != 1 or lines[0].split()[1] != fingerprint:
        raise RuntimeError('Expected exactly the approved Mac public key; no keys changed')


def effective(config=None):
    args = ['/usr/sbin/sshd', '-T']
    if config:
        args += ['-f', str(config)]
    args += ['-C', 'user=root,host=cnc-admin,addr=198.51.100.10']
    return dict(line.split(' ', 1) for line in invoke(*args).splitlines())


def validate(values):
    expected = {'passwordauthentication': 'no', 'kbdinteractiveauthentication': 'no',
                'pubkeyauthentication': 'yes', 'authenticationmethods': 'publickey',
                'authorizedkeyscommand': 'none', 'trustedusercakeys': 'none',
                'authorizedkeysfile': '.ssh/authorized_keys', 'allowusers': 'root',
                'hostbasedauthentication': 'no'}
    if any(values.get(k) != v for k, v in expected.items()) or values.get('permitrootlogin') not in ('prohibit-password', 'without-password'):
        raise RuntimeError('Effective SSH policy differs from the reviewed key-only settings')


assert_key()
if phase == 'prepare':
    root.mkdir(parents=True, mode=0o700, exist_ok=False)
    candidate = root / 'candidate.conf'
    candidate.write_text(POLICY + '\n' + main.read_text()); candidate.chmod(0o600)
    invoke('/usr/sbin/sshd', '-t', '-f', str(candidate))
    validate(effective(candidate))
    (root / 'policy.conf').write_text(POLICY)
    (root / 'prepared.json').write_text(json.dumps({'main_sha256': hashlib.sha256(main.read_bytes()).hexdigest(), 'fingerprint': fingerprint}))
    print(json.dumps({'prepared': True, 'fingerprint': fingerprint, 'changes_applied': False, 'rollback_directory': str(root)}))
elif phase == 'apply':
    prepared = json.loads((root / 'prepared.json').read_text())
    if prepared['main_sha256'] != hashlib.sha256(main.read_bytes()).hexdigest():
        raise RuntimeError('SSH base configuration changed since preparation')
    if target.is_symlink():
        raise RuntimeError('Policy path is a symlink')
    if target.exists():
        shutil.copy2(target, root / 'previous.conf')
    else:
        (root / 'previous-absent').touch()
    rollback = root / 'rollback.py'
    rollback.write_text('''import os,subprocess
from pathlib import Path
root=Path(''' + repr(str(root)) + ''')
target=Path(''' + repr(str(target)) + ''')
if (root/'confirmed').exists():raise SystemExit(0)
if (root/'previous.conf').exists():
 temp=target.with_suffix('.rollback-new');temp.write_bytes((root/'previous.conf').read_bytes());temp.chmod(0o600);os.replace(temp,target)
elif (root/'previous-absent').exists():
 if target.exists():target.unlink()
subprocess.run(['/usr/sbin/sshd','-t'],check=True)
subprocess.run(['systemctl','reload','ssh'],check=True)
(root/'rolled-back').touch()
''')
    rollback.chmod(0o700)
    invoke('systemd-run', '--quiet', '--unit=' + unit, '--on-active=180s', '--timer-property=AccuracySec=1s', '/usr/bin/python3', str(rollback))
    try:
        temp = target.with_suffix('.new')
        temp.write_bytes((root / 'policy.conf').read_bytes()); temp.chmod(0o600); os.replace(temp, target)
        invoke('/usr/sbin/sshd', '-t')
        validate(effective())
        invoke('systemctl', 'reload', 'ssh')
    except Exception:
        subprocess.run(['/usr/bin/python3', str(rollback)], check=True)
        subprocess.run(['systemctl', 'stop', unit + '.timer'], capture_output=True)
        raise
    print(json.dumps({'applied': True, 'rollback_timer': unit + '.timer', 'automatic_rollback_seconds': 180, 'fingerprint': fingerprint}))
elif phase == 'confirm':
    validate(effective())
    if (root / 'rolled-back').exists():
        raise RuntimeError('Rollout already rolled back; do not confirm it')
    (root / 'confirmed').write_text(str(time.time()))
    invoke('systemctl', 'stop', unit + '.timer')
    print(json.dumps({'confirmed': True, 'password_ssh': False, 'public_key_only': True, 'fingerprint': fingerprint, 'rollback_directory': str(root)}))
else:
    raise RuntimeError('Unknown phase')
