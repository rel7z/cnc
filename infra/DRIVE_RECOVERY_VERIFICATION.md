# Drive recovery verification — 2026-11-03

- Current selected controller: DigitalOcean `143.198.214.173` through its existing host-verified SSH tunnel.
- Saved OAuth client settings applied through the authenticated dashboard API. Drive enabled; fixed callback retained at `http://localhost:8080/api/config/gdrive/callback`.
- Mac manager and recorded active deployment both have `drive_oauth_auto_restore: true` and `drive_oauth_managed: true`.
- Updated helper installed in the Mac's private application-support directory; SHA-256 matches the repository helper.
- Existing Mac auto-reconnect agent reinstalled successfully. Dashboard moved from localhost port 8113 to 8114; fixed callback remains healthy on 8080. Controller UI and API services are active.
- Eight isolated tests passed: callback port migration and state/cookie checks; fresh-controller session restoration before configuration; private backup; preserving deliberate disconnect; different-client rejection; atomic, retry-safe token-file restoration; preferred-account selection across controller changes.
- Initial wrong-account consent returned `403 access_denied`. Owner subsequently completed login with `zerot1335@gmail.com`; the controller now reports authorized and the expected connected account.
- Old Linode SSH was unreachable and local saved profiles had no refresh token. Existing server settings alone cannot recover the old Google session.
- No processor/tools access, program activation, checkpoint reset, or processing changes were performed.
- Owner selected `zerot1335@gmail.com`; this preference is saved in the private Mac recovery profile and automatically added to Google's login hint across deployments. Its renewable session was backed up successfully with file mode 0600; Drive is enabled and automatic recovery remains active.
- End-to-end real watcher proof: unique `cnc-drive-recovery-proof-20261103-c620ddba.txt` (40 bytes) uploaded automatically, and downloaded Drive bytes matched the local SHA-256. Drive file ID: `1N8AM7Qee3qSUmA58Vy7aNHvXCTwHVLjZ`. No manual upload endpoint was used.
- Google Cloud Audience was inspected as the owner account in project `calmflow-487804`: publishing status is Testing; Publish app is disabled and requests completion of Branding. Branding has no home-page, privacy-policy, or terms URLs. Existing app/domain metadata was not modified.
- The local launch agent encountered a transient bootstrap failure during its helper update. Subsequent bootstrap succeeded; launchctl confirmed it running and both dashboard (8114) and callback (8080) returned HTTP 200.

Authorization, private session backup and actual automatic upload are verified. Sustained operation still requires resolving the Google app's Testing-mode seven-day token expiry; publishing is currently blocked by incomplete Google Branding configuration. No publishing or app-access expansion was performed.
