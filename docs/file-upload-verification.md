# File manager upload

Open the server dashboard, choose **File Manager**, navigate to an existing server directory, then choose **Upload files** or drop files onto the manager.

- Multiple selections upload sequentially with progress and individual results.
- Maximum file size: 90 MiB (shown as 90 MB in the UI).
- Existing filenames are rejected; rename the incoming file to upload another copy.
- Interrupted, oversized, invalid and conflicting uploads do not publish partial files.
- Uses the existing dashboard session and API proxy.

## Validation

Backend upload tests passed with the race detector, including binary/Unicode files, empty and boundary sizes, conflicts, concurrent uploads, malformed multipart input and symlink destinations. UI TypeScript passed. Local browser upload succeeded and refreshed the file listing.

Production release: `7592dcc7e1fbcb9bb6e83bc15748ab3bcd4d1f8f`.

Verified through http://172.236.141.170:

- Browser upload showed **Uploaded** and refreshed the file listing.
- 11 MiB binary upload passed through the public proxy; server SHA256 matched.
- Duplicate upload returned 409; unauthenticated upload returned 401.
- Login, bad-password rejection, cookie session protection and logout passed.
- All six workers were online.
- Existing processor cluster dependency and protected broker route remained available.

The deployment helper `infra/linode/deploy-file-upload.py` builds `cnc-api`, `cnc-ui` and the existing processor `cluster` package from permitted production sources plus the upload changes. It preserves unrelated local changes and excludes the restricted processor tools subtree. Production API sources were newer than the old RELEASE marker, so the helper preserves those deployed sources rather than relying on that marker alone.
