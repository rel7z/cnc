# Central processor settings — production rollout, 22 October 2026

Deployed release `central-settings-1792664261` to the existing Linode controller and seven workers.
The processor and dashboard were updated; the CNC server stayed running.

- Migrated both existing BF and Divi definitions into the production central configuration.
- Preserved all three executable version pins, both input routes, scheduler settings,
  and seven pre-existing interrupted-run records. No new runs were created.
- Backed up and removed exactly `/opt/cnc-processor/tools/bf/config.json` and
  `/opt/cnc-processor/tools/divi/config.json`, with owner approval. Their bytes were
  copied for rollback only; no other private tool files were inspected or changed.
- Restarted the processor again after removal and verified both setups loaded.
- Updated all seven worker binaries with normal graceful restarts, preserving
  the installed drain guard. Every running core matches the release hash.
- Updated the controller's worker-download artifact and verified its HTTP checksum.
- Production Linux dashboard build, authenticated page/setup access, unauthenticated
  API rejection, browser navigation, service health and worker registration passed.
- Processing remains paused. Deployment did not activate or test private programs.

Processor SHA256: `d0b7dee478d7b8b6c46fe2d4ccd52f5cc71ad784bc4f3342f604ebb99c5e3373`.
Worker SHA256: `2081b644d34129c8951a2087b207d422b487b39a7c78c9b6ca4a2a24ccc6a960`.

Controller/UI/database/configuration rollback: `/opt/cnc-controller/releases/central-settings-1792664261/rollback`.
The removed JSON copies are under its `legacy-tool-configs/` directory.
Each worker retains its prior binary under `/opt/cnc/rollouts/central-settings-1792664261/cnc-worker.previous`.

Private deployment receipt: `.linode/central-settings-production.json`.
Remote final receipt: `/opt/cnc-controller/releases/central-settings-1792664261/final-verification.json`.
The normal CNC Manager connection settings were preserved; a temporary loopback
SSH tunnel on port 18111 was used for browser verification.
