"""Reusable key-only SSH policy. Never writes private keys to a server."""
import re
import shlex

POLICY_NAME = '00-00-cnc-key-only.conf'


def policy_text(user='root'):
    if not re.fullmatch(r'[a-z_][a-z0-9_-]{0,31}', user):
        raise ValueError('Invalid SSH login user')
    return '\n'.join([
        '# CNC: deployment Mac key only; independent of changing client IPs',
        'PubkeyAuthentication yes', 'AuthenticationMethods publickey',
        'PasswordAuthentication no', 'KbdInteractiveAuthentication no',
        'HostbasedAuthentication no', 'AuthorizedKeysCommand none',
        'TrustedUserCAKeys none', 'AuthorizedKeysFile .ssh/authorized_keys',
        'PermitRootLogin ' + ('prohibit-password' if user == 'root' else 'no'),
        'AllowUsers ' + user, '',
    ])


def key_only_setup(user='root'):
    text = policy_text(user)
    return ("install -d -m 755 /etc/ssh/sshd_config.d\n"
            "printf '%s' " + shlex.quote(text) + " > /etc/ssh/sshd_config.d/" + POLICY_NAME + "\n"
            "/usr/sbin/sshd -t\nsystemctl reload ssh\n")
