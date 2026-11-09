# Processor workspace rollout verification

Verified on 2026-10-21 against controller `172.237.87.96`.

- Build source: `9eb53b5dabb88dd222fd0e7be0cfae24cecba698`, stored in the controller's private core Git repository.
- Workers 2–8: active/running, no pending restart, running binary and stop guard SHA-256 `41c4268602518af4c8cb779ae42d99402cdbee0791718ed2b7d695fdf4a188df`.
- All seven workers advertise `/root/processor` and are online in the deployed UI.
- Current registered BF bundle mechanically prepared at `/root/processor/bf` on all seven workers. No private program was run for verification. Divi's registered bundles were absent from worker caches; normal bundle installation prepares its workspace automatically.
- Original processing state restored to enabled. History preserved: 7 interrupted, 0 queued/running/completed/failed at verification; no checkpoint reset or run retry.
- Workspace tests passed on Linux: persistent working directory, serialization, cancellation, selected-output retention, symlink rejection, and input-file exclusion from export.
- Synthetic distributed fixture passed: 10,000 lines, 10 batches, six workers, renamed merged output, no missing/duplicate lines.
- Publication idempotency, editor/API compatibility, SSH command tests, TypeScript checks, and production UI build passed.
- Existing saved-key SSH command successfully opened worker 5's BF folder. Worker 7's UI login command verified.
- Deployed UI verified: separate sections, stable worker ordering, worker filename → merged filename controls, SSH instructions. Existing production output mappings retained.
- Source-only audit snapshots were copied to controller Git; no GitHub push was performed by this rollout.

Rollback copies remain in each worker's workspace release directories and controller release rollback directories. See `processor-workspaces.md` for operation details.
