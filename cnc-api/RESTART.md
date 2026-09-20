# CNC Server Auto-Restart

## How It Works

The CNC server can restart itself via the UI without restarting your entire computer.

### Components

1. **Restart API Endpoint** (`/api/server/restart`)
   - Gracefully stops the server
   - Exits with code 42
   - Takes ~100ms to ensure response is sent

2. **Wrapper Script** (`run-server.sh`)
   - Catches exit code 42
   - Automatically restarts the server
   - Ctrl+C stops completely

3. **UI Button** (Settings dropdown)
   - Click "Restart Server"
   - Server reloads config automatically
   - UI reconnects in ~3 seconds

## Usage

### Start Server with Auto-Restart

You can now start the server directly using `./cnc-server`:

```bash
cd cnc-api
./cnc-server
```

Or with custom config:

```bash
./cnc-server -config=my-config.json
```

*(Optional)* The wrapper script `./run-server.sh` is also still supported.

### Restart from UI

1. Open Settings dropdown on dashboard
2. Make config changes (e.g., Google Drive settings)
3. Click "Save"
4. Click "Restart Server" button
5. Wait ~3 seconds - done!

### Stop Completely

Press `Ctrl+C` in the terminal where server is running.

## What Gets Restarted?

✅ **Restarts:**
- CNC server process
- HTTP API server
- TCP worker connections
- Google Drive uploader (if enabled)
- All config reloaded from disk

❌ **Does NOT restart:**
- Your computer
- Worker processes (they reconnect automatically)
- The UI (just refreshes connection)
- Any other processes

## Exit Codes

- `0` - Normal shutdown (Ctrl+C)
- `42` - Restart requested (auto-restarts)
- Other - Error (stops, shows error code)

## Troubleshooting

**Server won't restart?**
- Make sure you started it with `./run-server.sh`
- Check if wrapper script is executable: `chmod +x run-server.sh`

**UI shows "Server is restarting..."?**
- Normal! Refresh page after ~5 seconds
- Server might be applying config changes

**Workers disconnect?**
- They reconnect automatically within 30 seconds
- No action needed

## Example Session

```bash
$ ./run-server.sh
Starting CNC Server with auto-restart...
Press Ctrl+C to stop completely

CNC Server
  HTTP : :8080
  TCP  : :9000
  Data : ./cnc_data

[server running...]

[User clicks "Restart Server" in UI]

[restart] Restart requested via API, shutting down...
Stopping CNC Server...
[restart] Exiting with code 42 for restart

==========================================
Restarting server...
==========================================

CNC Server
  HTTP : :8080
  TCP  : :9000
  Data : ./cnc_data

[server running with new config...]
```
