# Internet Map discovery ownership — 2026-11-22

Internet Map at `/opt/internet-map` owns the generator algorithm and persistent state. The catalog is `/opt/internet-map/data/catalog.db`; the candidate pool, discovery history and checkpoints are under `/opt/internet-map/data/discovery`. State was moved on the same volume, with the legacy CNC directory retained as a compatibility symlink. The old CNC generator is disabled; `internet-map-discovery` owns scheduling. Scanning remained paused during cutover.

CNC executes worker jobs through its private authenticated API. A harmless request from the migrated Internet Map coordinator (`job_1795358821_609`) completed with exact output and exit code zero on eight distinct workers. Internet Map's authenticated discovery status/workers/control APIs report ownership and worker capacity. The CNC dashboard's signed-in control route forwards to these APIs over loopback, using a server-only key. Authenticated status and pause requests were verified live.

Catalog reconciliation runs every 30 seconds and deduplicates classifications with explicit provenance. Legacy Internet Map workers receive no new leases after cutover; in-flight submissions remain accepted. Six-hour backup jobs create consistent SQLite snapshots, compress bounded parts and publish manifests to CNC's existing Drive uploader. Restore verifies part hashes, assembled hash and SQLite integrity before installing a missing destination. Full production backup upload/restore verification remains pending.

The CNC installer now detects the Internet Map ownership marker and does not reactivate its old scheduler on subsequent updates. Source rollback and the prior CNC UI build remain on the controller. The Internet Map installer includes a tested state-path rollback.

Validation: 65 migrated generator/index tests, 30 Internet Map tests, local TypeScript check and production Next.js build passed. Runtime secrets, databases, backups and datasets are excluded from commits. Internet Map source is in the owner's separate Desktop project and is published on `codex/internet-map-coordination`.
