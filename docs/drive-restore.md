# Restore old CNC files from Google Drive

Use Google Drive Settings → Restore from Drive after connecting the account.
The manual restore imports missing files into the configured watch directory
(the CNC merged folder), showing progress and restored/skipped/failed counts.
The browser may be closed while it runs.

The saved Drive Folder ID selects the source folder. Without one, restore selects
CNC uploads in Drive root carrying the uploader's `SHA256: ` description marker.
It skips subfolders, native Google documents, shortcuts, unsafe names, existing
files, and symlinks. Same-named Drive copies are ordered newest first; only the
first missing name is restored. Local files are not merged or replaced.

Downloads stream to private hidden staging files, verify available size and MD5
metadata, and publish atomically without replacing concurrent worker output.
Permissions are 0600. A persisted SHA256 record prevents an unchanged restored
copy from being uploaded again or removed by Delete After Upload. Upload and
mirrored-deletion operations are serialized with restoration. Restore makes
only Drive read requests. Retrying skips already-present files. Disconnect
cancels the job; after a server restart, click Restore again for remaining files.

API: `POST /api/config/gdrive/restore` starts one background job (202); another
active job or missing connection returns 409. `GET` returns aggregate progress.
Existing dashboard authentication and controller access restrictions apply.
Status contains counts and generic errors, not credentials or file contents.

Validation: focused restore tests and existing Drive tests pass with race
detection. Coverage includes pagination/scope, long lines, existing files,
duplicate names, unsafe paths, symlinks, checksum mismatch, interrupted requests,
state write failure, duplicate jobs, and keep-local state across reload.
TypeScript and the production dashboard build pass. These tests use synthetic
Drive responses, not real account files.

Deployment is prepared in `infra/aws/deploy-drive-restore.py`: scoped Git release,
existing encrypted S3 bucket and SSM, explicit integration patches, remote tests,
isolated UI build, and retained rollback copies. Private processor tools are
excluded. The first deployment was held by automatic approval review; the owner
then explicitly approved it. Deployment and real restore are now verified below.

## AWS verification — 2026-10-15

- Git release `1a892f7eaa058199aaa71fd77987be61ea594897`, branch `codex/drive-restore-aws`.
- SSM deployment `0bd6a6fc-5c32-4acd-8f34-381ff2eadd7b` succeeded. Backend tests and isolated dashboard build passed on AWS before activation. Rollback is retained under `/opt/cnc/drive-restore-releases/1a892f7eaa058199aaa71fd77987be61ea594897/rollback`.
- The saved Google account was authorized, and its token had owner-only permissions. The configured Drive folder contained four missing regular files; none matched a processor route, so this restore did not launch processor tools.
- Clicked Restore from Drive in the real AWS dashboard through the existing localhost:8081 SSM tunnel. The dashboard reported completed: 4 restored, 0 skipped, 0 failed, 66,934,963 bytes.
- Independent verification `b364c2c8-6156-46a9-a2ae-123046fe9a91` compared all four local copies against Drive metadata: file sizes and MD5 matched, persisted keep-local SHA256 records matched local bytes, and local permissions were owner-only. Missing: 0; mismatched: 0. No private names or contents were included in the report.
- All three workers reconnected and remained online. Processor tools and worker programs were not modified or executed.

Open `http://localhost:8081/dashboard?connectDrive=1` and use Restore from Drive for future imports. Existing local names are skipped; this operation restores missing files rather than replacing local data.

