# Google Drive Integration Setup

The CNC server can automatically watch a folder and upload files to Google Drive with deduplication.

## Features

- **Automatic file watching** - Monitors a folder for new and modified files
- **In-Place File Updates** - Guarantees only one file per filename on Google Drive (updates existing file rather than creating duplicates)
- **Automatic Duplicate Cleanup** - Detects if multiple copies of a file exist on Drive, merges their contents, and removes the extra copies
- **Synchronous Two-Way Line Merging** - Combines remote Drive lines with new local lines, preserving order and removing duplicate lines
- **Empty Line Filtering** - Automatically cleans out blank and whitespace-only lines during line deduplication
- **Local File Sync** - Rewrites the local file with the merged, deduplicated content so local and Drive remain 1:1 identical
- **Mirrored file deletion** - Removing a previously synchronized file from Drive or the watch directory removes it from the other side
- **Deduplication & Caching** - Uses SHA256 hashes to prevent unnecessary re-uploads and re-sync loops
- **Resume support** - Remembers synced file hashes across restarts
- **Configurable Cleanup** - Optional local file deletion after upload (`delete_after_upload`)
- **Shared Drive Support** - Uses `SupportsAllDrives` and `IncludeItemsFromAllDrives` for full team/shared folder compatibility

## Setup Instructions

### 1. Create Google Cloud Project

1. Go to [Google Cloud Console](https://console.cloud.google.com/)
2. Create a new project or select existing one
3. Enable the **Google Drive API**:
   - Go to "APIs & Services" > "Library"
   - Search for "Google Drive API"
   - Click "Enable"

### 2. Create OAuth Credentials

1. Go to "APIs & Services" > "Credentials"
2. Click "Create Credentials" > "OAuth client ID"
3. Configure the OAuth consent screen if prompted:
   - User Type: External
   - Add your email as a test user
4. Application type: **Desktop app**
5. Give it a name (e.g., "CNC Uploader")
6. Download the credentials JSON file
7. Save it as `credentials.json` in the `cnc-api` directory

### 3. Configure CNC Server

Edit `server_config.json`:

```json
{
  "http_addr": ":8080",
  "tcp_addr": ":9090",
  "data_dir": "./cnc_data",
  "max_retries": 3,
  "heartbeat_ttl": "30s",
  "gdrive": {
    "enabled": true,
    "watch_dir": "./upload_queue",
    "credentials_path": "./credentials.json",
    "token_path": "./token.json",
    "upload_interval": "30s",
    "parent_folder_id": "",
    "state_file_path": "./upload_queue/.gdrive_state.json",
    "delete_after_upload": false
  }
}
```

**Configuration Options:**

- `enabled` - Set to `true` to enable Google Drive integration
- `watch_dir` - Directory to monitor for new files (default: `./upload_queue`)
- `credentials_path` - Path to your OAuth credentials JSON file
- `token_path` - Where to store the OAuth token after authentication
- `upload_interval` - How often to scan for new files (e.g., "30s", "1m", "5m")
- `parent_folder_id` - (Optional) Google Drive folder ID to upload into. Leave empty to upload to root.
- `state_file_path` - Where to store the list of uploaded file hashes
- `delete_after_upload` - Set to `true` to delete local files after successful upload

### Reconnect from a local dashboard tunnel

For the AWS deployment, install the macOS tunnel once with
`bash infra/aws/open-dashboard.sh --install`, then open
`http://localhost:8081/dashboard`. The login agent reconnects the tunnel after
terminal closure or network interruptions while the `cnc` AWS profile remains
valid. If the AWS login session expires, refresh that profile and the agent
will retry. The tunnel listens only on `127.0.0.1:8081`; its launcher and
Session Manager plugin are stored under
`~/Library/Application Support/CNC Dashboard Tunnel/` so macOS permits
background access. The dashboard's Connect button uses the same origin and
sets the callback to `http://localhost:8081/api/config/gdrive/callback`.
Add that exact URI to the Google OAuth web client's Authorized redirect URIs
once. The **Sync now** button queues a scan of the watch directory after the
Google account is connected.

The Linode deployment has a separate tunnel helper below.

The hosted dashboard is served by the CNC web front end, while port 8080 is the
private CNC API. Do not open `http://localhost:8080/dashboard`; that address
refers to a service on your own computer. From this repository, run:

```sh
./infra/linode/connect-drive-tunnel.sh
```

The helper starts a persistent background tunnel, verifies it, and opens
`http://localhost:8081/drive-local-start`. That route places the one-time OAuth
state cookie on the callback origin and continues directly to Google. You can
close the terminal; rerunning the command safely reuses a healthy tunnel. No
localhost dashboard login or duplicate settings form is required. The start
route is available only through the SSH tunnel; the public server returns HTTP
403. Stop the tunnel later with:

```sh
./infra/linode/connect-drive-tunnel.sh --stop
```

On macOS, install the tunnel as a supervised login service so it reconnects
after SSH, Wi-Fi, sleep, or network interruptions:

```sh
./infra/linode/connect-drive-tunnel.sh --install
```

The service listens only on `127.0.0.1:8081`. Remove it with
`./infra/linode/connect-drive-tunnel.sh --uninstall`. macOS login agents cannot
read SSH keys under `Documents`, so installation places a private tunnel-only
copy under `~/.ssh/cnc-drive-tunnel` with owner-only permissions. Uninstalling
removes that copy.

The server uses `http://localhost:8081/api/config/gdrive/callback`. If a Google
Web application client has never used that callback, Google may require that
exact URI once in its Authorized redirect URIs. The CNC dashboard does not
need the URI entered again.

### Mirrored file deletion

For files CNC has successfully synchronized, deleting the Drive copy moves the
matching file out of the watch directory, and deleting the local watch-directory
copy moves all matching Drive copies to Google Drive Trash. Local files removed
because their Drive copy was deleted are kept under the hidden
`.cnc-deleted/` recovery folder inside the watch directory. Drive deletions are
recoverable from Drive Trash.

The uploader tracks synchronized filenames in its state file. Existing hash-list
state is migrated automatically. Files that CNC has never synchronized are not
deleted by this mirror. With `delete_after_upload: true`, a missing local copy is
expected, so that setting keeps the Drive copy and disables local-delete
propagation for it.

This mirrors whole-file deletion. Text file contents continue to use the
existing append-and-deduplicate merge behavior; deleting individual lines is
not treated as a file deletion.

### 4. Get Google Drive Folder ID (Optional)

If you want to upload to a specific folder:

1. Open Google Drive in your browser
2. Navigate to the folder
3. The URL will look like: `https://drive.google.com/drive/folders/FOLDER_ID_HERE`
4. Copy the `FOLDER_ID_HERE` part
5. Paste it into `parent_folder_id` in your config

### 5. First Run - Authentication

1. Start the CNC server:
   ```bash
   ./cnc-server
   ```

2. If Google Drive is enabled, you'll see a URL in the console:
   ```
   Go to the following link in your browser then type the authorization code:
   https://accounts.google.com/o/oauth2/auth?...
   ```

3. Open the URL in your browser
4. Sign in with your Google account
5. Click "Allow" to grant permissions
6. Copy the authorization code
7. Paste it back in the terminal and press Enter

8. The server will save the token to `token.json` - you won't need to authenticate again unless you delete this file.

## Usage

### Manual Upload

Simply drop files into the watched directory:

```bash
cp myfile.txt ./upload_queue/
```

The server will automatically:
1. Detect the new file (within the configured interval)
2. Calculate its SHA256 hash
3. Check if it's already been uploaded
4. Upload it to Google Drive if it's new
5. Mark it as uploaded in the state file
6. Optionally delete the local file

### Worker Upload Integration

Workers can save their results directly to the watched directory:

```bash
# In broadcast mode - save results to upload queue
cnc broadcast "nmap -sV target.com > /path/to/upload_queue/scan_$(hostname).txt"
```

### Monitoring

Watch the server logs for upload activity:

```
[gdrive] Started watching ./upload_queue (scanning every 30s)
[gdrive] ✓ Uploaded: scan_worker1.txt (hash: a3f2c4d8b1e7...)
[gdrive] ✓ Uploaded: scan_worker2.txt (hash: b9e1a5c3d2f6...)
[gdrive] Deleted local file: scan_worker1.txt
```

## Line Deduplication & Synchronization Algorithm

The sync engine uses a hybrid line-merge and hash-caching model:

### 1. In-Place Update (Single File on Google Drive)
Google Drive allows multiple files with identical names in the same folder. To prevent clutter:
- The server checks whether a file with the same name already exists in the destination folder.
- If it exists, the server updates that file in-place (`Files.Update`) rather than creating duplicate files.
- If prior runs created multiple duplicate copies in Drive, the server merges all of their lines and automatically removes the extra copies so **only 1 file remains**.

### 2. Ordered Line Merging
For text files (e.g., `.txt`, `.csv`, `.log`, `.jsonl`), lines are combined synchronously:
1. **Google Drive lines** are placed first.
2. **New local lines** are placed second.
3. Blank / whitespace-only lines are filtered out.
4. Duplicate lines are removed while preserving first-seen line order.

### Example:
```text
Drive x.txt:
hello world

Local x.txt:
world2

Result on Drive & Local:
hello world
world2
```

### 3. Hash-Based State Tracking
- The SHA256 hashes of both pre-merge and post-merge files are saved in the state file.
- When the local file is rewritten with the deduplicated content, the watcher recognizes the hash and avoids infinite re-sync loops.

## Troubleshooting

**"Error: unable to read credentials file"**
- Make sure `credentials.json` exists in the path specified
- Check that the path in `server_config.json` is correct

**"Error: invalid credentials"**
- Download fresh credentials from Google Cloud Console
- Make sure you downloaded "OAuth client ID" not "Service Account"
- Application type should be "Desktop app"

**"Files not uploading"**
- Check server logs for errors
- Verify the watch directory exists and has proper permissions
- Make sure files aren't hidden (starting with `.`)
- Try reducing `upload_interval` to scan more frequently

**"Token expired" errors**
- Delete `token.json`
- Restart the server
- Re-authenticate when prompted

**Permission denied on Google Drive**
- Make sure you granted the app permission during OAuth flow
- Check that your Google account has access to the target folder (if using `parent_folder_id`)
- Try revoking and re-granting permissions in Google Account settings

## Security Notes

- **Keep `credentials.json` and `token.json` private** - they grant access to your Google Drive
- Add them to `.gitignore` to avoid committing them
- The OAuth token has limited scope (only Google Drive access)
- Tokens can be revoked at any time from your Google Account settings
- Consider using a dedicated Google account for automated uploads

## Advanced: Multiple Upload Queues

You can run multiple CNC servers, each watching different folders:

1. Create separate config files:
   - `server_config_site1.json` - watches `./upload_queue_site1`
   - `server_config_site2.json` - watches `./upload_queue_site2`

2. Run on different ports:
   ```bash
   ./cnc-server --config server_config_site1.json
   ./cnc-server --config server_config_site2.json
   ```

3. Workers can target specific queues based on their location/purpose

## Disabling Google Drive

Set `"enabled": false` in the `gdrive` section or remove the section entirely:

```json
{
  "http_addr": ":8080",
  "tcp_addr": ":9090",
  "data_dir": "./cnc_data",
  "max_retries": 3,
  "heartbeat_ttl": "30s",
  "gdrive": {
    "enabled": false
  }
}
```

The server will start normally without Google Drive integration.
