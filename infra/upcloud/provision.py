#!/usr/bin/env python3
"""Provision CNC on UpCloud; secrets come exclusively from UPCLOUD_TOKEN."""
import argparse
import json
import os
from pathlib import Path
import shlex
import subprocess
import time
import urllib.error
import urllib.request
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from ssh_identity import deployment_key, identity_lines
from ssh_policy import key_only_setup

ROOT = Path(__file__).resolve().parents[2]
BASE = 'https://api.upcloud.com/1.3/'


def api(path, payload=None):
    token = os.environ.get('UPCLOUD_TOKEN')
    if not token:
        raise RuntimeError('Set UPCLOUD_TOKEN in your environment')
    req = urllib.request.Request(BASE + path, data=None if payload is None else json.dumps(payload).encode(),
        headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'})
    for attempt in range(4 if payload is None else 1):
        try:
            with urllib.request.urlopen(req, timeout=45) as response:
                return json.load(response)
        except urllib.error.HTTPError as error:
            if payload is None and error.code in (429, 500, 502, 503, 504) and attempt < 3:
                time.sleep(2 ** attempt)
                continue
            raise RuntimeError(f'UpCloud {error.code}: {error.read().decode()}') from None
        except (urllib.error.URLError, TimeoutError):
            if payload is not None or attempt == 3:
                raise
            time.sleep(2 ** attempt)


def run(args, **kwargs):
    return subprocess.run(args, check=True, **kwargs)


def address(server, access):
    return next((ip['address'] for ip in server['ip_addresses']['ip_address']
                 if ip['access'] == access and ip['family'] == 'IPv4'), None)


def firewall_plan(nodes, names):
    operations = []
    server_name = names[0]
    flows = [(server_name, worker, 22) for worker in names[1:]]
    flows += [(worker, server_name, port) for worker in names[1:] for port in (8080, 9090)]
    for name in names:
        node = nodes[name]
        for source, destination, port in flows:
            if name not in (source, destination):
                continue
            peer_name = destination if name == source else source
            peer_ip = nodes[peer_name]['private_ip']
            for reply in (False, True):
                outbound = (name == source) != reply
                direction = 'out' if outbound else 'in'
                port_prefix = 'source' if reply else 'destination'
                marker = f'cnc-{direction}-{peer_ip}-{port}-{int(reply)}'
                rule = {'action': 'accept', 'direction': direction, 'family': 'IPv4',
                        'protocol': 'tcp', 'position': '1', 'comment': marker,
                        port_prefix + '_port_start': str(port), port_prefix + '_port_end': str(port)}
                address_prefix = 'destination' if outbound else 'source'
                rule[address_prefix + '_address_start'] = peer_ip
                rule[address_prefix + '_address_end'] = peer_ip
                operations.append({'server': name, 'uuid': node['uuid'], 'firewall_rule': rule})
    return operations


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--zone', default='sg-sin1')
    parser.add_argument('--plan', default='STARTER-1xCPU-4GB')
    parser.add_argument('--workers', type=int, default=2)
    parser.add_argument('--name', default='cnc-test')
    parser.add_argument('--provision-only', action='store_true', help='Create nodes and SSH configuration; deployment is handled by release.py')
    parser.add_argument('--configure-firewall', action='store_true', help='Apply private service firewall rules after operator authorization')
    parser.add_argument('--apply', action='store_true', help='Create billable resources and deploy')
    args = parser.parse_args()
    if args.workers < 1:
        parser.error('At least one worker is required')
    state_dir = ROOT / '.upcloud'
    state_dir.mkdir(mode=0o700, exist_ok=True)
    os.chmod(state_dir, 0o700)
    state_path = state_dir / 'state.json'
    state = json.loads(state_path.read_text()) if state_path.exists() else {'servers': {}}
    def save():
        temporary = state_path.with_suffix('.tmp')
        temporary.write_text(json.dumps(state, indent=2) + '\n')
        temporary.chmod(0o600)
        temporary.replace(state_path)
    plans = api('plan')['plans']['plan']
    plan = next(p for p in plans if p['name'] == args.plan and p['current_offering'] == 'yes')
    existing = api('server')['servers']['server']
    names = [args.name + '-server'] + [f'{args.name}-worker-{i}' for i in range(1, args.workers + 1)]
    missing = [name for name in names if not any(s['title'] == name for s in existing)]
    limits = api('account')['account']['resource_limits']
    memory = sum(int(s['memory_amount']) for s in existing) + len(missing) * int(plan['memory_amount'])
    cores = sum(int(s['core_number']) for s in existing) + len(missing) * int(plan['core_number'])
    if memory > limits['memory'] or cores > limits['cores']:
        raise RuntimeError(f'Quota insufficient: need {memory} MB RAM and {cores} cores')
    print(json.dumps({'zone': args.zone, 'plan': args.plan, 'server_count': len(names), 'new_servers': len(missing), 'total_ram_mb': memory, 'total_cores': cores}), flush=True)
    if not args.apply:
        return
    key = deployment_key(ROOT, state_dir)
    pubkey = key.with_suffix('.pub').read_text().strip()
    templates = api('storage/template')['storages']['storage']
    template = next(t for t in templates if 'Ubuntu Server 24.04' in t['title'])
    for index, name in enumerate(names):
        found = next((s for s in existing if s['title'] == name), None)
        if found:
            if found['zone'] != args.zone or int(found['memory_amount']) != int(plan['memory_amount']):
                raise RuntimeError('Existing node does not match requested zone or memory: ' + name)
            server = api('server/' + found['uuid'])['server']
        else:
            interfaces = [{'type': 'utility', 'ip_addresses': {'ip_address': [{'family': 'IPv4'}]}}]
            if index == 0:
                interfaces.insert(0, {'type': 'public', 'ip_addresses': {'ip_address': [{'family': 'IPv4'}]}})
            payload = {'server': {'zone': args.zone, 'title': name, 'hostname': name, 'plan': args.plan,
                'metadata': 'yes', 'login_user': {'username': 'root', 'create_password': 'no', 'ssh_keys': {'ssh_key': [pubkey]}},
                'user_data': '#!/bin/bash\nset -eu\n' + key_only_setup(),
                'storage_devices': {'storage_device': [{'action': 'clone', 'storage': template['uuid'],
                    'title': name + '-disk', 'size': max(20, int(plan['storage_size'])), 'tier': plan['storage_tier'] or 'standard'}]},
                'networking': {'interfaces': {'interface': interfaces}}}}
            server = api('server', payload)['server']
        state['servers'][name] = {'uuid': server['uuid'], 'public_ip': address(server, 'public'), 'private_ip': address(server, 'utility')}
        save()
        print('Provisioned ' + name + ': ' + server['uuid'], flush=True)
    deadline = time.monotonic() + 600
    for name in names:
        while True:
            server = api('server/' + state['servers'][name]['uuid'])['server']
            if server['state'] == 'error':
                raise RuntimeError('UpCloud reports an error for ' + name)
            if server['state'] == 'started':
                state['servers'][name].update(public_ip=address(server, 'public'), private_ip=address(server, 'utility'))
                save()
                break
            if time.monotonic() > deadline:
                raise RuntimeError('Server boot timed out; rerun to resume')
            time.sleep(5)
    plans = firewall_plan(state['servers'], names)
    (state_dir / 'firewall-plan.json').write_text(json.dumps(plans, indent=2) + '\n')
    if args.configure_firewall:
        for name in names:
            node = state['servers'][name]
            rules = api('server/' + node['uuid'] + '/firewall_rule')['firewall_rules']['firewall_rule']
            for operation in plans:
                rule = operation['firewall_rule']
                if operation['server'] == name and not any(existing.get('comment') == rule['comment'] for existing in rules):
                    api('server/' + node['uuid'] + '/firewall_rule', {'firewall_rule': rule})
        print('Configured private service firewall rules', flush=True)
    server_node = state['servers'][names[0]]
    ssh_config = state_dir / 'ssh_config'
    ssh_config.write_text('Host cnc-*\n  User root\n' + identity_lines(key, state_dir/'id_ed25519') + f'  IdentitiesOnly yes\n  BatchMode yes\n  IPQoS none\n  Compression yes\n  ConnectTimeout 10\n  StrictHostKeyChecking accept-new\n  UserKnownHostsFile {state_dir / "known_hosts"}\nHost {names[0]}\n  HostName {server_node["public_ip"]}\n' + ''.join(f'Host {name}\n  HostName {state["servers"][name]["private_ip"]}\n  ProxyJump {names[0]}\n' for name in names[1:]))
    ssh_config.chmod(0o600)
    if args.provision_only:
        return
    env = dict(os.environ, GOCACHE=str(state_dir / 'go-cache'), GOOS='linux', GOARCH='amd64', CGO_ENABLED='0')
    for kind in ['server', 'worker']:
        run(['go', 'build', '-ldflags=-s -w', '-o', str(state_dir / ('cnc-' + kind)), './cmd/' + kind], cwd=ROOT / 'cnc-api', env=env)
    for index, name in enumerate(names):
        kind = 'server' if index == 0 else 'worker'
        ssh = ['ssh', '-F', str(ssh_config), name]
        deadline = time.monotonic() + 300
        while subprocess.run(ssh + ['true'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode:
            if time.monotonic() > deadline:
                raise RuntimeError('SSH timed out for ' + name + '; rerun to resume')
            time.sleep(5)
        run(ssh + ['mkdir -p /opt/cnc'])
        run(['scp', '-C', '-F', str(ssh_config), str(state_dir / ('cnc-' + kind)), name + ':/opt/cnc/cnc-' + kind])
        config = {'http_addr': server_node['private_ip'] + ':8080', 'tcp_addr': server_node['private_ip'] + ':9090', 'data_dir': '/opt/cnc/data', 'gdrive': {'enabled': False}, 'telegram': {'enabled': False}} if index == 0 else {'server_addr': server_node['private_ip'] + ':9090', 'worker_id': name, 'max_tasks': 2, 'data_dir': '/opt/cnc/data'}
        service = f'[Unit]\nDescription=CNC {kind}\nAfter=network-online.target\nWants=network-online.target\n[Service]\nWorkingDirectory=/opt/cnc\nExecStart=/opt/cnc/cnc-{kind} -config=/opt/cnc/config.json\nRestart=on-failure\nRestartSec=5\n[Install]\nWantedBy=multi-user.target\n'
        script = 'set -eu\nchmod 700 /opt/cnc/cnc-' + kind + '\ncat > /opt/cnc/config.json <<\'CONFIG\'\n' + json.dumps(config) + '\nCONFIG\ncat > /etc/systemd/system/cnc-' + kind + '.service <<\'UNIT\'\n' + service + 'UNIT\nsystemctl daemon-reload\nsystemctl enable --now cnc-' + kind + '\nsystemctl restart cnc-' + kind + '\nsystemctl is-active cnc-' + kind + '\n'
        run(ssh + ['bash', '-s'], input=script, text=True)
        print('Deployed ' + name, flush=True)
    deadline = time.monotonic() + 120
    while True:
        result = run(['ssh', '-F', str(ssh_config), names[0], 'curl -fsS ' + shlex.quote('http://' + server_node['private_ip'] + ':8080/api/workers')], capture_output=True, text=True)
        workers = json.loads(result.stdout)
        if isinstance(workers, dict):
            workers = workers.get('workers', [])
        online = [w for w in workers if w.get('status') in ['online', 'busy'] and w.get('id') in names[1:]]
        if len(online) == args.workers:
            break
        if time.monotonic() > deadline:
            raise RuntimeError('Not all workers registered: ' + result.stdout)
        time.sleep(3)
    state['verified_workers'] = len(online)
    save()
    print(json.dumps({'server_ip': server_node['public_ip'], 'workers_online': len(online), 'ssh_config': str(ssh_config)}), flush=True)

if __name__ == '__main__':
    main()
