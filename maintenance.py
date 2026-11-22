"""Catalog reconciliation and immutable Drive backup publication."""
import argparse
import fcntl
import json
from pathlib import Path
from backup import publish_snapshot
from catalog_sync import sync_verified


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=('sync', 'backup'))
    parser.add_argument('--root', type=Path, default=Path('/opt/internet-map'))
    parser.add_argument('--drive-inbox', type=Path, default=Path('/opt/cnc/merged'))
    args = parser.parse_args()
    data = args.root / 'data'
    data.mkdir(parents=True, exist_ok=True)
    with (data / (args.action + '.lock')).open('a') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            return
        if args.action == 'sync':
            result = sync_verified(data / 'catalog.db', data / 'discovery/generator-state.db')
        else:
            result = {'snapshots': []}
            for path in (data / 'catalog.db', data / 'discovery/generator-state.db',
                         data / 'discovery/candidates.db'):
                if path.exists():
                    result['snapshots'].append(publish_snapshot(path, args.drive_inbox))
        output = data / (args.action + '-status.json')
        temporary = output.with_suffix('.tmp')
        temporary.write_text(json.dumps(result, indent=2) + '\n')
        temporary.replace(output)
        print(json.dumps(result))


if __name__ == '__main__':
    main()
