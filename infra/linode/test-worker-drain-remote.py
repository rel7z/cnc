"""Isolated Linux proof: fake local controller, dummy task, TERM and legacy guard."""
from pathlib import Path
import json
import os
import queue
import shlex
import signal
import socket
import subprocess
import sys
import tempfile
import threading
import time

binary = Path(sys.argv[1]).resolve()
root = Path(tempfile.mkdtemp(prefix='cnc-drain-proof-'))
os.chmod(root,0o700)
listener = socket.socket()
listener.bind(('127.0.0.1',0)); listener.listen(1); listener.settimeout(10)
config = root/'config.json'
config.write_text(json.dumps({'worker_id':'drain-proof-dummy','server_addr':'127.0.0.1:'+str(listener.getsockname()[1]),'max_tasks':1,'data_dir':str(root/'data')}))
messages = queue.Queue()
connected = threading.Event()
errors = []


class SystemdWorker:
    """Subprocess-like handle for an isolated unit with production stop rules."""

    def __init__(self):
        self.returncode = None
        self.unit = root.name + '.service'
        self.path = Path('/run/systemd/system') / self.unit
        self.path.write_text('''[Unit]
Description=Isolated CNC worker drain proof
[Service]
Type=exec
WorkingDirectory={root}
ExecStart={binary} -config={config}
ExecStop={binary} -config={config} -drain-stop $MAINPID
TimeoutStopSec=infinity
KillMode=mixed
KillSignal=SIGTERM
StandardOutput=append:{root}/worker.log
StandardError=inherit
'''.format(root=root, binary=binary, config=config))
        subprocess.run(['systemctl', 'daemon-reload'], check=True)
        subprocess.run(['systemctl', 'start', self.unit], check=True)

    def poll(self):
        raw = subprocess.check_output(['systemctl', 'show', self.unit, '-p', 'ActiveState', '-p', 'Result'], text=True)
        state = dict(line.split('=', 1) for line in raw.splitlines())
        if state['ActiveState'] in ('active', 'activating', 'deactivating'):
            return None
        self.returncode = 0 if state['Result'] == 'success' else 1
        return self.returncode

    def send_signal(self, sig):
        assert sig == signal.SIGTERM
        subprocess.run(['systemctl', 'stop', '--no-block', self.unit], check=True)

    def wait(self, timeout=30):
        deadline = time.monotonic() + timeout
        while self.poll() is None:
            if time.monotonic() > deadline:
                raise RuntimeError('Isolated systemd drain did not finish')
            time.sleep(.1)
        return self.returncode

    def kill(self):
        # Only the uniquely named dummy fixture unit is eligible for cleanup.
        subprocess.run(['systemctl', 'kill', '--signal=SIGKILL', '--kill-whom=all', self.unit], check=True)

    def close(self):
        self.path.unlink()
        subprocess.run(['systemctl', 'daemon-reload'], check=True)
        subprocess.run(['systemctl', 'reset-failed', self.unit], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def controller():
    try:
        conn,_=listener.accept()
        with conn:
            connected.set()
            stream=conn.makefile('rb')
            sent=False
            for raw in stream:
                msg=json.loads(raw);messages.put(msg)
                if msg['type']=='register_worker':
                    conn.sendall(json.dumps({'type':'ack','payload':{'http_addr':'127.0.0.1:1'}}).encode()+b'\n')
                    if not sent:
                        sent=True
                        program="import pathlib,time;pathlib.Path("+repr(str(root/'started'))+").write_text('started');time.sleep(2);print('dummy-finished')"
                        task={'id':'drain-proof-task','type':'shell','payload':{'command':'python3 -c '+shlex.quote(program),'timeout_seconds':60}}
                        conn.sendall(json.dumps({'type':'assign_task','payload':{'task':task}}).encode()+b'\n')
    except Exception as error:
        errors.append(type(error).__name__)


thread=threading.Thread(target=controller,daemon=True);thread.start()
with (root/'worker.log').open('w') as log:
    worker=SystemdWorker() if '--systemd' in sys.argv[2:] else subprocess.Popen([str(binary),'-config='+str(config)],cwd=root,stdout=log,stderr=subprocess.STDOUT)
    try:
        deadline=time.monotonic()+15
        while not (root/'started').exists():
            if worker.poll() is not None:raise RuntimeError('Dummy task failed to start; inspect '+str(root/'worker.log'))
            if time.monotonic()>deadline:raise RuntimeError('Dummy startup timed out; inspect '+str(root/'worker.log'))
            time.sleep(.05)
        worker.send_signal(signal.SIGTERM)
        # The child must survive TERM and deliver its completed output.
        worker.wait(timeout=30)
        if worker.returncode!=0:raise RuntimeError('Worker did not drain successfully')
    finally:
        if worker.poll() is None:worker.kill();worker.wait()
        if isinstance(worker, SystemdWorker):worker.close()
listener.close();thread.join(timeout=2)
received=[]
while not messages.empty():received.append(messages.get())
if errors:raise RuntimeError('Fake controller failed: '+','.join(errors))
if not any(m['type']=='register_worker' and m['payload']['worker']['max_tasks']==0 for m in received):raise RuntimeError('Drain did not withdraw capacity')
results=[m['payload'] for m in received if m['type']=='task_result']
if len(results)!=1 or results[0].get('error') or results[0]['result']['exit_code']!=0 or 'dummy-finished' not in results[0]['result'].get('stdout',''):raise RuntimeError('Dummy was killed or final result lost')

# The stable stop guard also protects an old worker with no capability marker.
parent_code="import subprocess,signal,time;child=subprocess.Popen(['sleep','2']);signal.signal(signal.SIGTERM,lambda *args:exit(0));child.wait();time.sleep(60)"
parent=subprocess.Popen(['python3','-c',parent_code])
legacy=root/'legacy.json';legacy.write_text(json.dumps({'server_addr':'127.0.0.1:1','worker_id':'legacy-dummy','max_tasks':1,'data_dir':str(root/'legacy-data')}))
try:
    time.sleep(.2)
    start=time.monotonic()
    guard=subprocess.Popen([str(binary),'-config='+str(legacy),'-drain-stop',str(parent.pid)],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    time.sleep(.4)
    if parent.poll() is not None:raise RuntimeError('Legacy guard killed a running child')
    parent.wait(timeout=20)
    guard.wait(timeout=5)
    if guard.returncode!=0 or time.monotonic()-start<10:raise RuntimeError('Legacy idle guard did not wait')
finally:
    if parent.poll() is None:parent.kill();parent.wait()
print(json.dumps({'dummy_task_completed_after_sigterm':True,'systemd_stop_verified':'--systemd' in sys.argv[2:],'zero_capacity_advertised':True,'final_result_received':True,'legacy_busy_process_preserved':True,'proof_directory':str(root)}))
