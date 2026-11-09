#!/usr/bin/env python3
"""Record the narrow patch in Git and deploy the controller's enum package."""
import base64
import gzip
import json
import os
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[2]
NAMES = ['cnc-backend/enum/' + n for n in ('pipeline.go', 'plugin_rules_test.go', 'version/evidence.go', 'version/plugin_identity_test.go', 'version/pipeline_test.go')]
NAMES += ['infra/aws/verify-plugin-rules.py', 'infra/aws/deploy-plugin-verification.py', 'infra/aws/deploy-plugin-verification-remote.py']
files = {name: (ROOT / name).read_text() for name in NAMES}
with tempfile.TemporaryDirectory(prefix='cnc-plugin-git-') as directory:
    env = dict(os.environ, GIT_INDEX_FILE=str(Path(directory) / 'index'))
    def git(*args):
        return subprocess.check_output(['git', *args], cwd=ROOT, env=env, text=True).strip()
    git('read-tree', '--empty')
    git('add', '--', *NAMES)
    revision = git('commit-tree', git('write-tree'), '-m', 'Require plugin identity and matching version rules for clean text exports')
    git('update-ref', 'refs/heads/codex/plugin-verification-audit', revision)
payload = base64.b64encode(gzip.compress(json.dumps({'files': files, 'revision': revision}).encode())).decode()
script = "python3 - <<'PLUGIN_RELEASE'\n" + files['infra/aws/deploy-plugin-verification-remote.py'].replace('SOURCE_PAYLOAD', repr(payload)) + '\nPLUGIN_RELEASE\n'
path = Path('/private/tmp/cnc-deploy-plugin-verification.sh')
path.write_text(script)
subprocess.run(['python3', '/private/tmp/cnc-aws-ssm.py', str(path)], check=True)
