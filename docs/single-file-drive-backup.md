# Single-file Drive backup validation

Validated on 2026-11-23 (Asia/Jakarta).

The production publisher now creates one signed, compressed file per backup, containing the catalog, discovery state, candidate catalog, non-secret settings, pending lease inputs, and application source. It publishes by atomic rename and the Drive uploader streams bytes unchanged. Transport chunks do not become separate Drive files.

## Verified cloud recovery

- File: `internet-map-backup-1795379418-a605b06ee6a941308530670d2a1d5d15.tar.gz`
- Compressed size: 1,943,650,694 bytes.
- SHA-256: `a865dab3c0dbd7f704b474607c944eac73c8f74b0c74034d0fe7416bd362d39d`.
- The release-installed verifier downloaded the file from Drive into a missing, isolated directory; verified the manifest HMAC, every file hash and size, and all three SQLite databases; and exited successfully.
- Restored rows: 12,469,484 catalog sites; 5,000,000 ready candidates; 17,448,104 catalog candidates. Both the live generator and restored copy remained paused.
- Every archived application source file matched the deployed application. The dashboard proxy returned `ok` and the exact filename/checksum upload acknowledgement was present.
- All 234 legacy small pieces/manifests were moved to Drive Trash. A subsequent scoped Drive listing found zero remaining non-trashed legacy pieces. Two complete single-file backup generations were retained.

The cloud check downloaded real Drive bytes: it did not reuse an existing local archive or use a fallback signing key. The dashboard integrity button separately checks the local archive in the background. Backup creation returned in 0.054 seconds in a production API check; expensive work continued outside the request.

## Deployment and persistence

The saved private CNC Manager Drive profile remains renewable and enabled; the saved recovery key and profile have mode 0600. The installed Manager helper matches repository source. Manager transfers the key over host-verified SSH, never as a command-line argument, and starts the release-installed recovery service on a fresh controller. It preserves an existing application and retries an interrupted fresh recovery installation. Recovered discovery stays paused.

Fresh-controller service dispatch, private-key persistence, retry behavior, mismatch refusal, and installer rollback were exercised with isolated filesystem/service fixtures. Full cloud download and SQLite recovery used the actual production release helper. No additional paid controller was provisioned for this verification.

The six-hour backup timer is active again. Replaced server binaries, application files and release archives were retained for rollback. Deployment occurred with zero active CNC jobs; all eight workers reconnected. The downloadable worker artifact remained checksum-consistent and no worker binary or configuration changed for this update.

## Production artifact checksums

- Server: `6f64e877a6ec35f1952a3b088d92227bc3129bc1a325dc193faf873772712c9a`.
- Trusted recovery helper: `8d084157610ac897e4115a1a2bd1fcd6713aa8874177ea72f7f4ddcca3774359`.
- Release archive: `d5c18f4aaa1fafd4801f9a5e4d1c08cfef31be75364968d4afec32ead7cea5ac`.

These matched the installed server/helper, staged release, and controller's downloadable worker checksum references. The builder and installer include and verify the trusted recovery helper on future deployments.

Unique immutable bundle files now reuse their SHA-256 while identity, size and modification time remain unchanged. Replacement or content metadata changes invalidate that cache; mutable exports retain their complete content checks. A five-second process read sample fell from 1,146,003,159 to 319,730,171 bytes after deployment; ordinary mutable exports still require hashing.

## Focused validation

- Map API: 9 tests; bundle creation/recovery: 3; recovery installer/rollback: 2; coordination/SQLite snapshot checks: 8.
- CNC Drive OAuth and trusted recovery: 14 tests, including actual transfer-script execution against temporary paths and a service-command fixture.
- Go Drive tests covered exact bytes, MIME type, outage retry, internal-artifact exclusion, unchanged-name file replacement, and mutable-export hashing.
- Production UI built successfully, the frontend API proxy responded, and both production cloud restore checks completed successfully.

Runtime credentials, OAuth tokens, signing keys, databases and build caches are outside Git. Implementation source is published on `codex/cnc-single-file-recovery`; the Map source is published on `codex/internet-map-single-file-recovery` (code commit `1085dfb`). See [automatic recovery](../infra/DRIVE_RECOVERY.md) for operation and prerequisites.
