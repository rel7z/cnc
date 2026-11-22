# DigitalOcean replacement deployment — 2026-11-22

Created one controller and eight workers in Singapore with the owner-supplied account token. All nine are 8 GB / 4 vCPU at US$48/month each (US$432/month total). Existing resources were not deleted.

Controller: `157.245.153.108`. CNC Manager selects the pinned new controller and serves the dashboard at `http://localhost:8119/dashboard`; automatic reconnection and the fixed Google callback remain configured. Saved Google Drive recovery was applied and verified enabled and authorized.

All nine running binary checksums and hardware/service metadata matched the release. Eight-worker harmless broadcast `job_1795342313_1` completed with exact output. Dummy spread `job_1795342392_2` retained eight chunk files under `/root`, with exact checksums verified after temporary task inputs were cleaned. Dashboard login returned 200; unauthenticated API returned 401. Processor remains paused with no private programs configured. The release has three built-in default version checks. No scanners or private processor tools were run for verification.

Private credentials and deployment inventory remain outside Git. Original connection settings were backed up before CNC Manager was switched.
