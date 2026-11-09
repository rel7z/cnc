"""Explicit, idempotent registration of saved default setups; never runs tools."""
import argparse
import json
from pathlib import Path
import urllib.request


DEFAULTS = {'bf': 'builtin-processor-bf-20261021', 'divi': 'builtin-processor-divi-20261021'}


def restore_defaults(call, pause=False):
    health = call('GET', '/v1/health')
    if health['enabled']:
        if not pause:
            raise RuntimeError('Pause processing before restoring default setups')
        call('PUT', '/v1/processing-state', {'enabled': False})
    health = call('GET', '/v1/health')
    if health['enabled'] or health['counts'].get('running', 0) or health['counts'].get('queued', 0):
        raise RuntimeError('Processing must be paused with no queued/running jobs')
    templates = {item['id']: item for item in call('GET', '/v1/processor-templates')}
    state = call('GET', '/v1/processor-setups')
    existing = {item['tool_id'] for item in state['definitions']} | set(state['external'])
    added = []
    # Check every required template before changing any registration.
    for tool, template_id in DEFAULTS.items():
        if tool in existing:
            continue
        item = templates.get(template_id)
        if not item or item['definition']['tool_id'] != tool or item['definition']['folder'] != 'tools/' + tool:
            raise RuntimeError('Saved default template missing or source folder changed: ' + tool)
    for tool, template_id in DEFAULTS.items():
        if tool in existing:
            continue  # Never overwrite an owner's active setup.
        state = call('PUT', '/v1/processor-setups', {'definition': templates[template_id]['definition'], 'revision': state['revision']})
        added.append(tool)
    health = call('GET', '/v1/health')
    if health['enabled'] or health['counts'].get('running', 0) or health['counts'].get('queued', 0):
        raise RuntimeError('Processing pause or empty queue could not be confirmed')
    return {'registered': added, 'existing_preserved': sorted(existing), 'processor_paused': True, 'programs_executed': False}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--apply', action='store_true', required=True)
    parser.add_argument('--pause', action='store_true')
    args = parser.parse_args()
    env = dict(line.split('=', 1) for line in Path('/opt/cnc-controller/processor.env').read_text().splitlines() if '=' in line)
    token = env['CNC_PROCESSOR_TOKEN']
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def call(method, path, data=None):
        request = urllib.request.Request('http://127.0.0.1:9191' + path,
            method=method, data=json.dumps(data).encode() if data is not None else None,
            headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'})
        with opener.open(request, timeout=60) as response:
            return json.load(response)
    print(json.dumps(restore_defaults(call, pause=args.pause)))


if __name__ == '__main__':
    main()
