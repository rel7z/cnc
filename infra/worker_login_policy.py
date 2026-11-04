"""Deployment-owned worker login defaults; no secret values in source."""
import subprocess
from pathlib import Path


def default_worker_password(root):
    path = Path(root) / '.linode' / 'worker-root-password'
    if not path.exists():
        return None
    password = path.read_text().removesuffix('\n')
    if not password or len(password.encode()) > 1024 or any(c in password for c in '\r\n\0'):
        raise RuntimeError('Invalid private worker password policy')
    return password


def password_setup(password):
    result = subprocess.run(['openssl', 'passwd', '-6', '-stdin'], input=password + '\n', text=True, capture_output=True, check=True)
    hashed = result.stdout.strip()
    if not hashed.startswith('$6$') or any(c in hashed for c in "'\r\n"):
        raise RuntimeError('Cannot prepare worker login policy')
    return ("printf '%s\\n' 'root:" + hashed + "' | chpasswd -e\n"
            "install -d -m 755 /etc/ssh/sshd_config.d\n"
            "printf '%s\\n' 'PermitRootLogin prohibit-password' 'PubkeyAuthentication yes' 'PasswordAuthentication no' 'KbdInteractiveAuthentication no' 'AuthenticationMethods publickey' > /etc/ssh/sshd_config.d/00-cnc-worker-login.conf\n"
            "/usr/sbin/sshd -t\nsystemctl reload ssh\n")
