#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."
export PATH="$PWD/.aws-cnc/bin:$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"
case "${1:-}" in
  --install) exec python3 infra/aws/dashboard-doctor.py --install ;;
  --check) exec python3 infra/aws/dashboard-doctor.py --check ;;
  --repair) exec python3 infra/aws/dashboard-doctor.py --repair --login ;;
  --browser-link) exec python3 infra/aws/dashboard-doctor.py --repair --login --browser-link ;;
  '') exec python3 infra/aws/dashboard-doctor.py --repair --login --open ;;
  *) echo 'Usage: open-dashboard.sh [--install|--check|--repair|--browser-link]' >&2; exit 2 ;;
esac
