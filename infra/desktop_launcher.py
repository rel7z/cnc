#!/usr/bin/env python3
"""Desktop launcher for the single maintained CNC Manager implementation."""
import os
from pathlib import Path
import runpy
import sys

root = Path(os.environ.get('CNC_REPO', str(Path.home() / 'Documents/GitHub/cnc')))
entry = root / 'infra/cnc_manager.py'
if not entry.is_file():
    raise SystemExit('CNC repository missing. Set CNC_REPO to its folder.')
sys.argv[0] = str(entry)
runpy.run_path(str(entry), run_name='__main__')
