# Linode merged directory and Drive synchronization

The migrated controller stores results at `/opt/cnc-controller/merged`. Drive
restore recovered 70 files (298,243,354 bytes), but the familiar `/root/merged`
path was missing. The processor and Drive were already configured to the same
storage directory; no processor route or private tool was changed.

Created `/root/merged` as a directory alias to the configured storage. Verified
all 70 file hashes before and after alias creation. File Manager now follows
directory aliases when identifying clickable folders and skips broken aliases.
The Linode installer creates the same alias on future deployments, uses one
watch-directory value for Drive and processor, and refuses conflicting existing
directories rather than overwriting data.

Explicit Drive sync completed: 70 uploads and zero pending before the backend
restart. The new backend loaded the saved synchronization state and retained
Drive authorization. Only the controller backend was restarted; the processor
remained paused. Historical AWS job data has not been recovered by this repair.

Validation:

- Four installer filesystem tests passed: shared storage, repeat setup,
  conflicting existing directory, dangling alias, and storage-symlink refusal.
- Seven Go tests passed for File Manager directory navigation, restore integrity,
  restore scope/pagination, duplicate restore rejection, and manual sync state.
- Linux amd64 backend build passed; uploaded artifact checksum verified.
- Deployment rollback: `/opt/cnc-controller/cnc-server.before-merged-1792554477782750326`.
- Remote receipts: `/opt/cnc-controller/merged-directory-receipt.json` and
  `/opt/cnc-controller/merged-backend-receipt.json`.
- Final local API verification: `.linode/merged-directory-verification.json`.

No passwords, tokens, file contents, or private tool files are included here.
