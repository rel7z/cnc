#!/usr/bin/env python3
"""Provision the owner-approved private lab, without accessing other tools."""
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[2]
SSH = ["ssh", "-F", str(ROOT / ".linode/ssh_config"), "cnc-linode-server"]
REMOTE = r"""
import json, secrets, subprocess, time
from pathlib import Path
base = Path('/opt/cnc-processor-lab')
base.mkdir(mode=0o700, exist_ok=True)
(base/'ftp').mkdir(exist_ok=True)
(base/'ftp'/'lab-readme.txt').write_text('Authorized private CNC processor lab.\n')
policy_path = Path('/opt/cnc/processor-targets.json')
if not policy_path.exists():
    policy_path.write_text(json.dumps({'wordpress':['http://192.168.145.81:18081'],
        'ftp':[{'url':'ftp://192.168.145.81:2121','username':'cnc_lab','password':secrets.token_urlsafe(32)}]}, indent=2))
    policy_path.chmod(0o600)
def run(*args):
    return subprocess.run(args, check=True)
run('python3','-m','venv', str(base/'venv'))
run(str(base/'venv/bin/pip'),'install','--disable-pip-version-check','pyftpdlib==2.1.0')
unit = '''[Unit]
Description=CNC private read-only FTP test lab
After=network-online.target
Wants=network-online.target
[Service]
WorkingDirectory=/opt/cnc-processor-lab
ExecStart=/opt/cnc-processor-lab/venv/bin/python /opt/cnc-processor-lab/lab_ftp_server.py
Restart=on-failure
UMask=0077
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
[Install]
WantedBy=multi-user.target
'''
Path('/etc/systemd/system/cnc-processor-lab-ftp.service').write_text(unit)
run('systemctl','daemon-reload')
run('systemctl','enable','--now','cnc-processor-lab-ftp')
if subprocess.run(['docker','network','inspect','cnc-processor-lab'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode:
    run('docker','network','create','--internal','cnc-processor-lab')
if subprocess.run(['docker','network','inspect','cnc-processor-lab-front'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode:
    run('docker','network','create','cnc-processor-lab-front')
for image in ('mariadb:11.4','wordpress:6-php8.3-apache','wordpress:cli'):
    run('docker','pull',image)
db_env, wp_env = base/'db.env', base/'wp.env'
if not db_env.exists():
    password = secrets.token_urlsafe(32)
    db_env.write_text('MARIADB_DATABASE=wordpress\nMARIADB_USER=wordpress\nMARIADB_PASSWORD='+password+'\nMARIADB_ROOT_PASSWORD='+secrets.token_urlsafe(32)+'\n')
    wp_env.write_text('WORDPRESS_DB_HOST=cnc-processor-lab-db\nWORDPRESS_DB_NAME=wordpress\nWORDPRESS_DB_USER=wordpress\nWORDPRESS_DB_PASSWORD='+password+'\n')
    db_env.chmod(0o600)
    wp_env.chmod(0o600)
for name, image, envfile, extra in (
    ('cnc-processor-lab-db','mariadb:11.4', db_env, ['-v','cnc-processor-lab-db:/var/lib/mysql']),
    ('cnc-processor-lab-wp','wordpress:6-php8.3-apache', wp_env,
     ['-v','cnc-processor-lab-wp:/var/www/html','-p','192.168.145.81:18081:80'])):
    if subprocess.run(['docker','inspect',name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode:
        run('docker','run','-d','--name',name,'--restart','unless-stopped','--network','cnc-processor-lab',
            '--memory','512m','--env-file',str(envfile), *extra, image)
    else:
        run('docker','start',name)
networks = json.loads(subprocess.check_output(['docker','inspect','cnc-processor-lab-wp']))[0]['NetworkSettings']['Networks']
if 'cnc-processor-lab-front' not in networks:
    run('docker','network','connect','cnc-processor-lab-front','cnc-processor-lab-wp')
cli = ['docker','run','--rm','--network','cnc-processor-lab','--volumes-from','cnc-processor-lab-wp',
       '--env-file',str(wp_env),'--user','33:33','-e','HOME=/tmp','wordpress:cli','wp']
for _ in range(24):
    p = subprocess.run(cli+['core','is-installed'], capture_output=True)
    if p.returncode == 0:
        break
    p = subprocess.run(cli+['core','install','--url=http://192.168.145.81:18081',
        '--title=CNC Private Processor Lab','--admin_user=admin','--admin_password=pass',
        '--admin_email=lab@example.invalid','--skip-email'], capture_output=True)
    if p.returncode == 0:
        print('Real WordPress initialized')
        break
    time.sleep(2)
else:
    raise RuntimeError('WordPress initialization failed: '+p.stderr.decode()[-800:])
run(*cli,'core','version')
run('docker','ps','--filter','name=cnc-processor-lab','--format','{{.Names}} {{.Status}} {{.Ports}}')
print('Private lab ready; secrets remain in protected files')
"""
if __name__ == "__main__":
    source = ROOT / "processor/tools/3-tools/lab_ftp_server.py"
    subprocess.run(SSH + ["mkdir -p /opt/cnc-processor-lab"], check=True)
    subprocess.run(SSH + ["umask 077; cat > /opt/cnc-processor-lab/lab_ftp_server.py"], input=source.read_bytes(), check=True)
    subprocess.run(SSH + ["python3 -"], input=REMOTE.encode(), check=True)
