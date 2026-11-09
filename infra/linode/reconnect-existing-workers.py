"""Switch existing infrastructure to one controller and seven idle workers."""
import argparse
import concurrent.futures
import json
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[2]
REMOTE = r'''
from pathlib import Path
import json,os,socket,subprocess,time
apply=APPLY
server=IS_SERVER
pid=int(subprocess.check_output(['systemctl','show','cnc-worker','--property=MainPID','--value'],text=True).strip() or '0')
children=Path('/proc/'+str(pid)+'/task/'+str(pid)+'/children')
child_count=len(children.read_text().split()) if pid and children.exists() else 0
if child_count:
    print(json.dumps({'ready':False,'reason':'existing_worker_has_child_processes','child_count':child_count}));raise SystemExit(2)
if not apply:
    print(json.dumps({'ready':True,'active_child_count':0,'intended_role':'server' if server else 'worker'}));raise SystemExit(0)
with socket.create_connection(('192.168.136.27',9090),timeout=5):pass
backup=Path('/opt/cnc/migration-backups')/str(time.time_ns());backup.mkdir(parents=True,mode=0o700)
config=Path('/opt/cnc/config.json');original=config.read_bytes()
(backup/'config.json').write_bytes(original);(backup/'config.json').chmod(0o600)
enabled=subprocess.run(['systemctl','is-enabled','cnc-worker'],capture_output=True,text=True).stdout.strip()
(backup/'service-state.json').write_text(json.dumps({'enabled':enabled,'old_controller':'172.31.4.149:9090'}))
if server:
    subprocess.run(['systemctl','disable','--now','cnc-worker'],check=True,capture_output=True)
    print(json.dumps({'role':'server','worker_service_disabled':True,'backup':str(backup)}));raise SystemExit(0)
value=json.loads(original)
if value.get('server_addr') not in ['172.31.4.149:9090','192.168.136.27:9090']:
    raise RuntimeError('Unexpected existing controller; configuration unchanged')
value['server_addr']='192.168.136.27:9090'
temp=config.with_suffix('.migration-next');temp.write_text(json.dumps(value,indent=2)+'\n');temp.chmod(0o600);os.replace(temp,config)
try:
    subprocess.run(['systemctl','restart','cnc-worker'],check=True,timeout=20)
    subprocess.run(['systemctl','is-active','--quiet','cnc-worker'],check=True,timeout=5)
except BaseException:
    config.write_bytes(original);subprocess.run(['systemctl','restart','cnc-worker'],timeout=20);raise
print(json.dumps({'role':'worker','worker_id':value['worker_id'],'server_addr':value['server_addr'],'service_active':True,'backup':str(backup)}))
'''


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--apply', action='store_true')
    args = parser.parse_args()

    def process(number):
        name = 'cnc-linode-worker-' + str(number)
        source = REMOTE.replace('APPLY', repr(args.apply)).replace('IS_SERVER', repr(number == 1))
        result = subprocess.run(['ssh', '-F', str(ROOT / '.linode/cross-cloud-ssh_config'),
            '-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=yes', '-o', 'ConnectTimeout=7',
            name, 'python3', '-'], input=source, capture_output=True, text=True, timeout=45)
        if result.returncode:
            return dict(node=name, success=False, exit_code=result.returncode,
                        error='Metadata check or connection cutover failed; inspect this node before retrying')
        return dict(node=name, success=True, **json.loads(result.stdout.strip().splitlines()[-1]))

    with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
        results = list(pool.map(process, range(1, 9)))
    print(json.dumps(results, indent=2))
    raise SystemExit(0 if all(row['success'] for row in results) else 1)


if __name__ == '__main__':
    main()
